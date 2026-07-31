package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/routing"
)

// publicErrorBody is the decoded public error envelope (05 §5).
type publicErrorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
	Retryable bool   `json:"retryable"`
}

func decodePublicError(t *testing.T, body []byte) publicErrorBody {
	t.Helper()
	var got struct {
		Error publicErrorBody `json:"error"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode public error: %v; body=%q", err, body)
	}
	return got.Error
}

// TestPublicEnvelope_AllCodes proves every public failure renders the full
// four-field envelope with the right status, request_id == X-Venom-Request-Id,
// a Retry-After on the 429 codes, and the correct retryable value.
//
// P6-M1: omit request_id ⇒ the request_id assertions RED.
// P6-M2: hardcode retryable:true ⇒ the false-retryable rows RED.
// P6-M3: drop Retry-After on a 429 code ⇒ the retry-after assertion RED.
func TestPublicEnvelope_AllCodes(t *testing.T) {
	render := func(fn func(w http.ResponseWriter)) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		fn(rec)
		return rec
	}
	h := &ChatCompletionsHandler{} // writePublicRoutingError needs no engine

	cases := []struct {
		name          string
		render        func(w http.ResponseWriter)
		wantCode      string
		wantStatus    int
		wantRetryable bool
		wantRetryHdr  bool
	}{
		{"free_capacity_exhausted", func(w http.ResponseWriter) {
			h.writePublicRoutingError(w, &routing.NoEligibleOfferingError{RetryAfter: 3 * time.Second})
		}, CodeFreeCapacityExhausted, http.StatusTooManyRequests, true, true},
		{"no_eligible_offering", func(w http.ResponseWriter) {
			h.writePublicRoutingError(w, &routing.NoEligibleOfferingError{})
		}, CodeNoEligibleOffering, http.StatusServiceUnavailable, true, false},
		{"context_exceeds_tier", func(w http.ResponseWriter) {
			h.writePublicRoutingError(w, routing.ErrContextExceedsTier)
		}, CodeContextExceedsTier, http.StatusBadRequest, false, false},
		{"capability_unsupported", func(w http.ResponseWriter) {
			h.writePublicRoutingError(w, &routing.CapabilityUnsupportedError{Capability: "vision", TierStructural: true})
		}, CodeCapabilityUnsupported, http.StatusBadRequest, false, false},
		{"invalid_extension", func(w http.ResponseWriter) {
			h.writePublicRoutingError(w, routing.ErrInvalidExtension)
		}, CodeInvalidExtension, http.StatusBadRequest, false, false},
		{"invalid_api_key", func(w http.ResponseWriter) {
			writePublicError(w, http.StatusUnauthorized, publicErrInvalidAPIKey, "invalid API key")
		}, publicErrInvalidAPIKey, http.StatusUnauthorized, false, false},
		{"rate_limited", func(w http.ResponseWriter) {
			w.Header().Set("Retry-After", "60")
			writePublicError(w, http.StatusTooManyRequests, publicErrRateLimited, "rate limit exceeded")
		}, publicErrRateLimited, http.StatusTooManyRequests, true, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := render(c.render)
			if rec.Code != c.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, c.wantStatus)
			}
			body := decodePublicError(t, rec.Body.Bytes())
			if body.Code != c.wantCode {
				t.Fatalf("code = %q, want %q", body.Code, c.wantCode)
			}
			if body.Message == "" {
				t.Fatalf("message must be present")
			}
			if body.RequestID == "" {
				t.Fatalf("request_id must be present (P6-M1)")
			}
			if body.RequestID != rec.Header().Get("X-Venom-Request-Id") {
				t.Fatalf("request_id %q != X-Venom-Request-Id %q (must correlate)", body.RequestID, rec.Header().Get("X-Venom-Request-Id"))
			}
			if body.Retryable != c.wantRetryable {
				t.Fatalf("retryable = %v, want %v (P6-M2)", body.Retryable, c.wantRetryable)
			}
			if c.wantRetryHdr && rec.Header().Get("Retry-After") == "" {
				t.Fatalf("Retry-After must be set for %s (P6-M3)", c.name)
			}
		})
	}
}

// TestPublicEnvelope_PrivacySchema proves — structurally — that the ONLY tables
// the request path writes cannot hold prompt/response/error/credential content.
func TestPublicEnvelope_PrivacySchema(t *testing.T) {
	db := testControlDB(t)
	forbidden := []string{"prompt", "response", "message", "content", "body", "error", "authorization", "credential", "secret", "token", "cipher", "plaintext"}
	// Correlation ids + numeric COUNT columns are allowed: tokens_in/tokens_out
	// are integer counts (the X-Venom-Tokens metrics), never token text (M7/00011
	// comments say so explicitly).
	allow := map[string]bool{
		"request_id": true, "provider_id": true, "provider_model_id": true, "account_id": true,
		"api_key_id": true, "route_decision_id": true, "offering_operation_id": true, "reservation_id": true,
		"tokens_in": true, "tokens_out": true,
	}
	for _, table := range []string{"usage_records", "route_decisions", "route_attempts"} {
		rows, err := db.Conn().Query("SELECT name FROM pragma_table_info('" + table + "')")
		if err != nil {
			t.Fatalf("table_info %s: %v", table, err)
		}
		for rows.Next() {
			var col string
			_ = rows.Scan(&col)
			lc := strings.ToLower(col)
			if allow[lc] {
				continue
			}
			for _, f := range forbidden {
				if strings.Contains(lc, f) {
					t.Fatalf("%s has content-shaped column %q (matches %q) — the request path must not persist content (05 §7)", table, col, f)
				}
			}
		}
		_ = rows.Close()
	}
}

// TestChat_CancellationRecordsUsageNoSecondResponse proves the cancellation
// contract: a client disconnect aborts the in-flight provider call, yet the
// accounting write still completes (detached context), and only ONE response is
// produced.
//
// P6-M5: use the request context for the accounting write ⇒ the write is
// cancelled with the client ⇒ zero usage rows ⇒ RED.
func TestChat_CancellationRecordsUsageNoSecondResponse(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) {
		close(started)
		<-release // block until the test releases (simulating a slow provider)
		_, _ = w.Write([]byte(completionJSON("late")))
	})
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"venom/pro","messages":[{"role":"user","content":"hi"}]}`)).WithContext(ctx)
	rec := &countingRecorder{ResponseRecorder: httptest.NewRecorder()}

	done := make(chan struct{})
	go func() { h.ServeChat(rec, r); close(done) }()

	<-started // provider call is in flight
	cancel()  // client disconnects
	close(release)
	<-done

	if n := countRows(t, db, "usage_records"); n != 1 {
		t.Fatalf("usage_records = %d, want 1 — accounting must survive client cancellation", n)
	}
	if rec.writeHeaderCalls > 1 {
		t.Fatalf("WriteHeader called %d times — a second response after the first is forbidden", rec.writeHeaderCalls)
	}
}

// countingRecorder counts WriteHeader calls to prove no second response is sent.
type countingRecorder struct {
	*httptest.ResponseRecorder
	writeHeaderCalls int
}

func (c *countingRecorder) WriteHeader(status int) {
	c.writeHeaderCalls++
	c.ResponseRecorder.WriteHeader(status)
}

// TestChat_IdempotencyKeyNotHonored proves an Idempotency-Key is IGNORED on the
// chat endpoint: two identical requests carrying the same key both execute and
// both record usage (inference is not replay-idempotent, 05 §5 / P5-PAPI-006).
func TestChat_IdempotencyKeyNotHonored(t *testing.T) {
	up, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) { _, _ = w.Write([]byte(completionJSON("ok"))) })
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	body := `{"model":"venom/pro","messages":[{"role":"user","content":"hi"}]}`
	for i := 0; i < 2; i++ {
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		r.Header.Set("Idempotency-Key", "same-key-123")
		rec := httptest.NewRecorder()
		h.ServeChat(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d status %d body %s", i, rec.Code, rec.Body.String())
		}
	}
	if up.calls != 2 {
		t.Fatalf("provider calls = %d, want 2 — the same Idempotency-Key must NOT replay (both execute)", up.calls)
	}
	if n := countRows(t, db, "usage_records"); n != 2 {
		t.Fatalf("usage_records = %d, want 2 — both requests record usage", n)
	}
}

// TestPublicEnvelope_RequestIDCorrelatesWithRecords is the governor-review
// addition that makes request_id actually useful. The delivered code minted a
// FRESH id inside writePublicError, so on a failure the id in the body (and in
// X-Venom-Request-Id) matched NO row in usage_records / route_decisions /
// route_attempts — a client quoting it could never be traced, which is the whole
// reason 05 §5 puts it in the envelope. The prior test only compared the body id
// against the header id, and both came from the same fresh mint, so it passed.
//
// Mutation: mint a new id in writePublicErrorRetryable instead of reusing the
// stamped one → the id stops matching the recorded rows → RED.
func TestPublicEnvelope_RequestIDCorrelatesWithRecords(t *testing.T) {
	// An upstream that always fails, so the request takes a terminal FAILURE path
	// (the path whose request_id most needs to be traceable).
	_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream down"}}`))
	})
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "model-a", "m-a")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	rec := postChat(t, h, `{"model":"venom/pro","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code == http.StatusOK {
		t.Fatalf("expected a failure status, got 200")
	}

	env := decodePublicError(t, rec.Body.Bytes())
	if env.RequestID == "" {
		t.Fatalf("failure envelope carries no request_id")
	}
	if got := rec.Result().Header.Get("X-Venom-Request-Id"); got != env.RequestID {
		t.Fatalf("header request id %q != body request_id %q", got, env.RequestID)
	}

	// THE POINT: that id must be findable in what the request actually recorded.
	var usage int
	if err := db.Conn().QueryRow(
		`SELECT COUNT(*) FROM usage_records WHERE request_id = ?`, env.RequestID,
	).Scan(&usage); err != nil {
		t.Fatalf("query usage: %v", err)
	}
	if usage != 1 {
		t.Fatalf("usage_records rows for request_id %q = %d, want 1 — the id a client is told must resolve to the rows the request wrote", env.RequestID, usage)
	}
	var decisions int
	if err := db.Conn().QueryRow(
		`SELECT COUNT(*) FROM route_decisions WHERE request_id = ?`, env.RequestID,
	).Scan(&decisions); err != nil {
		t.Fatalf("query decisions: %v", err)
	}
	if decisions != 1 {
		t.Fatalf("route_decisions rows for request_id %q = %d, want 1", env.RequestID, decisions)
	}
}
