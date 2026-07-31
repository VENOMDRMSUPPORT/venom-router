package httpapi

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/execution"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/observability"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/routing"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// P5-TEST-003 — usage recorded on EVERY terminal path (docs/05 §4 + §7).
// PAPI-002 covered success / exhaustion / provider-failure / cancellation; this
// unit adds the reconcile-verdict classes and the invariants across all paths.

func reservationState(t *testing.T, db *storage.DB) string {
	t.Helper()
	var st string
	if err := db.Conn().QueryRow(`SELECT state FROM quota_reservations LIMIT 1`).Scan(&st); err != nil {
		t.Fatalf("read reservation state: %v", err)
	}
	return st
}

func usageStatusOf(t *testing.T, db *storage.DB) string {
	t.Helper()
	var s string
	if err := db.Conn().QueryRow(`SELECT status FROM usage_records LIMIT 1`).Scan(&s); err != nil {
		t.Fatalf("read usage status: %v", err)
	}
	return s
}

// failStatusUpstream returns an upstream that always responds with the given
// status + optional scope header (to drive a chosen reconcile verdict).
func failStatusUpstream(t *testing.T, status int, scopeHeader string) (*capturingUpstream, *httptest.Server) {
	return newUpstream(t, func(w http.ResponseWriter, _ []byte) {
		if scopeHeader != "" {
			w.Header().Set("X-RateLimit-Scope", scopeHeader)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":{"message":"x"}}`))
	})
}

// TestP5Gate_UsagePreConsumptionReleased drives a 401 (account scope, auth
// class) → the real classifier returns VerdictPreConsumptionFailure → the
// reservation is RELEASED, and usage + a decision row are still recorded.
//
// T3-M1: write usage only when the verdict is success ⇒ the usage assertion RED.
func TestP5Gate_UsagePreConsumptionReleased(t *testing.T) {
	_, srv := failStatusUpstream(t, http.StatusUnauthorized, "")
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	rec := postChat(t, h, `{"model":"venom/pro","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code == http.StatusOK {
		t.Fatalf("a 401 from the sole account must not succeed")
	}
	if n := countRows(t, db, "usage_records"); n != 1 {
		t.Fatalf("usage_records = %d, want 1 (recorded on the pre-consumption path)", n)
	}
	if n := countRows(t, db, "route_decisions"); n != 1 {
		t.Fatalf("route_decisions = %d, want 1", n)
	}
	if st := reservationState(t, db); st != "released" {
		t.Fatalf("reservation state = %q, want released (pre-consumption failure frees the reservation)", st)
	}
}

// TestP5Gate_UsageUnknownConsumptionPending drives a 500 (provider scope, server
// class) → VerdictUnknownConsumption → the reservation is marked
// reconciliation_pending (headroom kept), and usage + decision are recorded.
func TestP5Gate_UsageUnknownConsumptionPending(t *testing.T) {
	_, srv := failStatusUpstream(t, http.StatusInternalServerError, "")
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	rec := postChat(t, h, `{"model":"venom/pro","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code == http.StatusOK {
		t.Fatalf("a 500 from the sole provider must not succeed")
	}
	if n := countRows(t, db, "usage_records"); n != 1 {
		t.Fatalf("usage_records = %d, want 1 (recorded on the unknown-consumption path)", n)
	}
	if st := reservationState(t, db); st != "reconciliation_pending" {
		t.Fatalf("reservation state = %q, want reconciliation_pending (unknown consumption keeps headroom)", st)
	}
}

// TestP5Gate_UsageRequestScopeFailure drives a 400 (request scope) → the loop
// STOPS with a failure, the reservation is released, and usage is recorded with
// a typed FAILURE status (never success).
//
// T3-M2: stamp status="success" unconditionally ⇒ the typed-status assertion RED.
func TestP5Gate_UsageRequestScopeFailure(t *testing.T) {
	_, srv := failStatusUpstream(t, http.StatusBadRequest, "")
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	rec := postChat(t, h, `{"model":"venom/pro","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code == http.StatusOK {
		t.Fatalf("a request-scope 400 must not succeed")
	}
	if n := countRows(t, db, "usage_records"); n != 1 {
		t.Fatalf("usage_records = %d, want 1", n)
	}
	if s := usageStatusOf(t, db); s == "success" {
		t.Fatalf("usage status = %q, must be a typed FAILURE, never success", s)
	}
}

// TestP5Gate_PartialConsumptionUnreachable PROVES (does not fake) that the real
// classifier never emits VerdictPartialConsumption: VerdictForTypedFailure maps
// every (scope × class) combination to RequestScope / PreConsumptionFailure /
// UnknownConsumption only. Partial consumption is therefore an UNREACHABLE
// terminal path on the request-path classifier and cannot be asserted end to end.
func TestP5Gate_PartialConsumptionUnreachable(t *testing.T) {
	scopes := []execution.FailureScope{
		execution.FailureScopeRequest, execution.FailureScopeAccount, execution.FailureScopeOffering,
		execution.FailureScopeProvider, execution.FailureScopeTransientTransport, execution.FailureScope(""),
	}
	classes := []execution.FailureClass{
		execution.FailureClassAuth, execution.FailureClassNotFound, execution.FailureClassInvalidRequest,
		execution.FailureClassQuota, execution.FailureClassRateLimit, execution.FailureClassServer,
		execution.FailureClassNetwork, execution.FailureClass(""),
	}
	for _, sc := range scopes {
		for _, cl := range classes {
			v := VerdictForTypedFailure(execution.TypedFailure{Scope: sc, FailureClass: cl})
			if v == routing.VerdictPartialConsumption {
				t.Fatalf("scope=%q class=%q mapped to VerdictPartialConsumption — unexpected", sc, cl)
			}
		}
	}
}

// TestP5Gate_UsageAttributedToKey proves the usage row is attributed to the
// authenticated api key.
//
// T3-M3: drop the api-key attribution ⇒ RED.
func TestP5Gate_UsageAttributedToKey(t *testing.T) {
	_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) { _, _ = w.Write([]byte(completionJSON("ok"))) })
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	// usage_records.api_key_id FKs venom_api_keys(id); seed the key.
	if _, err := db.Conn().Exec(
		`INSERT INTO venom_api_keys (id, label, key_hash, key_prefix, created_at) VALUES ('key-1','l',?, 'vk_live_', 0)`,
		strings.Repeat("a", 64)); err != nil {
		t.Fatalf("seed key: %v", err)
	}
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"venom/pro","messages":[{"role":"user","content":"hi"}]}`)).
		WithContext(context.WithValue(context.Background(), apiKeyContextKey{}, "key-1"))
	rec := httptest.NewRecorder()
	h.ServeChat(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var apiKeyID string
	if err := db.Conn().QueryRow(`SELECT api_key_id FROM usage_records LIMIT 1`).Scan(&apiKeyID); err != nil {
		t.Fatalf("read api_key_id: %v", err)
	}
	if apiKeyID != "key-1" {
		t.Fatalf("usage api_key_id = %q, want key-1 (attribution)", apiKeyID)
	}
}

// TestP5Gate_UsageNoDoubleCount proves exactly ONE usage row per REQUEST even
// when the engine made several attempts (attempts live in route_attempts).
//
// T3-M4: write one usage row per ATTEMPT ⇒ RED.
func TestP5Gate_UsageNoDoubleCount(t *testing.T) {
	up, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) {})
	up.handleFunc = func(w http.ResponseWriter, _ []byte) {
		if up.calls == 1 {
			w.Header().Set("X-RateLimit-Scope", "account")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"quota"}}`))
			return
		}
		_, _ = w.Write([]byte(completionJSON("ok")))
	}
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	seedCredentialedOfferingWithKey(t, db, kr, "acct-2", "prov/model-a", "model-a", "sk-upstream-SECRET-cred-2")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	rec := postChat(t, h, `{"model":"venom/pro","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("fallback should serve: status %d body %s", rec.Code, rec.Body.String())
	}
	if got := rec.Result().Header.Get("X-Venom-Fallback-Attempts"); got != "2" {
		t.Fatalf("expected 2 attempts, got X-Venom-Fallback-Attempts=%q", got)
	}
	if n := countRows(t, db, "usage_records"); n != 1 {
		t.Fatalf("usage_records = %d, want 1 (one row per REQUEST, not per attempt)", n)
	}
}

// TestP5Gate_UsageStreamingWriteFailureLogged proves a usage-write failure on the
// STREAMING path (headers already flushed) is LOGGED with the request id, never
// silently swallowed.
//
// T3-M5: swallow the streaming usage-write failure ⇒ RED.
func TestP5Gate_UsageStreamingWriteFailureLogged(t *testing.T) {
	_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) {
		fl, _ := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n"))
		if fl != nil {
			fl.Flush()
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	})
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	certifyOperation(t, db, "acct-1", "prov/model-a", "streaming")
	h := newE2EHandler(t, db, kr, srv.URL, failingUsage{})
	var buf bytes.Buffer
	h.log = observability.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	rec := postChat(t, h, `{"model":"venom/pro","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("streaming response should still complete: status %d", rec.Code)
	}
	logged := buf.String()
	if !strings.Contains(logged, "usage") || !strings.Contains(logged, "request_id") {
		t.Fatalf("a streaming usage-write failure must be LOGGED with the request id; log:\n%s", logged)
	}
}

// TestP5Gate_UsageContentFree proves no usage/decision/attempt row on any path
// holds prompt or response content.
func TestP5Gate_UsageContentFree(t *testing.T) {
	const marker = "USAGECANARYcontent"
	_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) { _, _ = w.Write([]byte(completionJSON(marker + "-resp"))) })
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	rec := postChat(t, h, `{"model":"venom/pro","messages":[{"role":"user","content":"`+marker+`-prompt"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	for _, table := range []string{"usage_records", "route_decisions", "route_attempts"} {
		if strings.Contains(dumpTable(t, db, table), marker) {
			t.Fatalf("content marker leaked into %s", table)
		}
	}
}
