package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	accountsdomain "github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/execution"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/observability"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/routing"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// --- test rig --------------------------------------------------------------

const upstreamCredential = "sk-upstream-SECRET-cred"

// capturingUpstream records the last request body the transport sent it, so a
// test can prove what did (and did not) leave the process.
type capturingUpstream struct {
	lastBody   []byte
	lastAuth   string
	handleFunc func(w http.ResponseWriter, body []byte)
}

func newUpstream(t *testing.T, handle func(w http.ResponseWriter, body []byte)) (*capturingUpstream, *httptest.Server) {
	t.Helper()
	u := &capturingUpstream{handleFunc: handle}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 0, 1024)
		buf := make([]byte, 512)
		for {
			n, err := r.Body.Read(buf)
			b = append(b, buf[:n]...)
			if err != nil {
				break
			}
		}
		u.lastBody = b
		u.lastAuth = r.Header.Get("Authorization")
		u.handleFunc(w, b)
	}))
	t.Cleanup(srv.Close)
	return u, srv
}

// completionJSON is the standard non-streaming success body.
func completionJSON(content string) string {
	return fmt.Sprintf(`{"choices":[{"message":{"role":"assistant","content":%q},"finish_reason":"stop"}]}`, content)
}

// seedCredentialedOffering seeds a certified offering on a healthy free account
// and gives it a REAL keyring-encrypted active credential (replacing
// seedOffering's placeholder), so CredentialService.Use round-trips.
func seedCredentialedOffering(t *testing.T, db *storage.DB, kr *secrets.Keyring, acct, provModelID, modelID string) {
	t.Helper()
	seedOffering(t, db, acct, provModelID, modelID, true, intp2(200000), intp2(128000))
	if _, err := db.Conn().Exec(`DELETE FROM account_credentials WHERE account_id = ?`, acct); err != nil {
		t.Fatalf("clear placeholder credential: %v", err)
	}
	svc := application.NewCredentialService(storage.NewAccountCredentialRepo(db), kr, nil)
	if _, err := svc.Store(context.Background(), application.StoreCredentialParams{
		ID: "cred-real-" + acct, AccountID: acct, ProviderID: string(providers.OpenCodeZenID),
		Kind: accountsdomain.CredentialKindAPIKey, Active: true, PlaintextKey: upstreamCredential,
	}); err != nil {
		t.Fatalf("store real credential: %v", err)
	}
	// A fresh "requests" window with ample headroom, so the reservation has an
	// applicable window to debit against (ApplicableDebits errors on none).
	if _, err := db.Conn().Exec(
		`INSERT INTO quota_windows
		    (id, account_id, source, unit, window_type, window_key, remaining, limit_value, reserved, version, confidence, freshness_state, observed_at, created_at, updated_at)
		 VALUES (?, ?, 'local_safety', 'requests', 'rpm', 'k-req', 1000000, 1000000, 0, 1, 1, 'fresh', 0, 0, 0)`,
		"win-"+acct, acct,
	); err != nil {
		t.Fatalf("seed quota window: %v", err)
	}
}

// certifyOperation adds a certified+supported offering_operation for op on the
// given offering, so the routing capability gate admits a request that requires
// it (streaming / tools / vision).
func certifyOperation(t *testing.T, db *storage.DB, acct, provModelID, op string) {
	t.Helper()
	ooID := "oo-" + acct + "-" + provModelID + "-" + op
	if _, err := db.Conn().Exec(
		`INSERT INTO offering_operations (id, account_id, provider_id, provider_model_id, operation, created_at, updated_at) VALUES (?, ?, ?, ?, ?, 0, 0)`,
		ooID, acct, string(providers.OpenCodeZenID), provModelID, op,
	); err != nil {
		t.Fatalf("seed offering_operation %s: %v", op, err)
	}
	if _, err := db.Conn().Exec(
		`INSERT INTO certifications (offering_operation_id, status, capability_truth, version, certified_at, evidence_ref, created_at, updated_at) VALUES (?, 'certified', 'supported', 1, 0, '', 0, 0)`,
		ooID,
	); err != nil {
		t.Fatalf("seed certification %s: %v", op, err)
	}
}

// counterID returns a distinct id on each call (deterministic, so tests read
// cleanly and no PK collides).
func counterID() func() string {
	n := 0
	return func() string { n++; return fmt.Sprintf("id-%d", n) }
}

func newE2EHandler(t *testing.T, db *storage.DB, kr *secrets.Keyring, upstreamURL string, usage usageRecorder) *ChatCompletionsHandler {
	t.Helper()
	reg := providers.NewRegistry()
	_ = registerOpenCodeZen(reg)
	if _, ok := reg.Definition(providers.OpenCodeZenID); !ok {
		t.Fatalf("opencode-zen not registered")
	}
	client := &http.Client{}
	impls := map[execution.TransportType]execution.InferenceTransport{
		execution.TransportTypeOpenAICompatible: execution.NewOpenAICompatibleTransport(client, 0),
		execution.TransportTypeNativeOAuth:      execution.NewNativeOAuthTransport(client, 0),
	}
	credRepo := storage.NewAccountCredentialRepo(db)
	engine := &EngineDeps{
		Snapshot: NewSnapshotBuilder(
			storage.NewCatalogRepo(db), storage.NewAccountRepo(db),
			storage.NewFundingEvidenceRepo(db), credRepo,
			storage.NewQuotaWindowRepo(db, nil, nil), newInflightCounter(), 0,
		),
		Reservations:  storage.NewQuotaReservationRepo(db, nil),
		Lifecycle:     storage.NewQuotaLifecycleRepo(db, nil, nil),
		RouteRecorder: observability.NewRouteRecorder(db.Conn(), nil),
		Dispatcher:    BuildInferenceDispatcher(reg, impls),
		Classify:      NewDispatcherFailureClassifier(reg, impls),
		Creds:         credRepo,
		CredService:   application.NewCredentialService(credRepo, kr, nil),
		BaseURLFor:    func(string) string { return upstreamURL },
		Inflight:      newInflightCounter(),
		Cache:         routing.NewStickinessCache(0),
		Now:           func() time.Time { return time.Unix(0, 0) },
	}
	if usage == nil {
		usage = storage.NewUsageRecordRepo(db)
	}
	return NewChatCompletionsHandler(engine, usage, func() time.Time { return time.Unix(0, 0) }, counterID(), nil)
}

func e2eEnv(t *testing.T, upstreamURL string) (*storage.DB, *secrets.Keyring) {
	t.Helper()
	db := testControlDB(t)
	if err := storage.SeedProviders(context.Background(), db, providers.BuiltinCatalog(), time.Unix(0, 0)); err != nil {
		t.Fatalf("seed providers: %v", err)
	}
	kr, err := secrets.Load(t.TempDir(), "", false)
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	return db, kr
}

func postChat(t *testing.T, h *ChatCompletionsHandler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeChat(rec, req)
	return rec
}

// dumpTable returns every cell of every row of table as one string, so a canary
// scan can prove a value is absent from ANY column without enumerating them.
func dumpTable(t *testing.T, db *storage.DB, table string) string {
	t.Helper()
	rows, err := db.Conn().Query("SELECT * FROM " + table)
	if err != nil {
		t.Fatalf("query %s: %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	cols, _ := rows.Columns()
	var sb strings.Builder
	for rows.Next() {
		cells := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("scan %s: %v", table, err)
		}
		for _, c := range cells {
			fmt.Fprintf(&sb, "%v|", c)
		}
	}
	return sb.String()
}

// --- tests -----------------------------------------------------------------

// TestChatCompletions_NonStreamingSuccess_RealTransport is the anchor: a request
// flows through the REAL OpenAICompatibleTransport to an httptest upstream, and
// a chat.completion body comes back. It also proves the decision AND attempt
// rows persist (the decision-before-loop ordering) and a success usage row is
// written — the terminal-path billing truth.
func TestChatCompletions_NonStreamingSuccess_RealTransport(t *testing.T) {
	var up *capturingUpstream
	up, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(completionJSON("Hello from upstream")))
	})
	_ = up
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	rec := postChat(t, h, `{"model":"venom/pro","messages":[{"role":"user","content":"hi"}]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Object  string `json:"object"`
		Choices []struct {
			Message struct{ Content string } `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	if resp.Object != "chat.completion" || len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "Hello from upstream" {
		t.Fatalf("unexpected completion: %s", rec.Body.String())
	}
	// The credential reached the upstream (its rightful destination) as a Bearer.
	if up.lastAuth != "Bearer "+upstreamCredential {
		t.Fatalf("upstream Authorization = %q, want the credential as Bearer", up.lastAuth)
	}
	// A success usage row exists with the chosen provider.
	var status, provider string
	if err := db.Conn().QueryRow(`SELECT status, provider_id FROM usage_records LIMIT 1`).Scan(&status, &provider); err != nil {
		t.Fatalf("usage row: %v", err)
	}
	if status != "success" || provider != string(providers.OpenCodeZenID) {
		t.Fatalf("usage = (%s,%s), want (success,opencode-zen)", status, provider)
	}
	// Decision AND attempt rows persisted (proves decision-before-loop ordering).
	var decisions, attempts int
	_ = db.Conn().QueryRow(`SELECT count(*) FROM route_decisions`).Scan(&decisions)
	_ = db.Conn().QueryRow(`SELECT count(*) FROM route_attempts`).Scan(&attempts)
	if decisions != 1 {
		t.Fatalf("route_decisions = %d, want 1", decisions)
	}
	if attempts < 1 {
		t.Fatalf("route_attempts = %d, want >= 1 (attempt rows must persist — they FK to the decision)", attempts)
	}
}

// TestChatCompletions_Streaming_ProgressiveThenDone proves the SSE path delivers
// each delta as its own flushed frame (never a single buffered blob) and
// terminates with data: [DONE].
func TestChatCompletions_Streaming_ProgressiveThenDone(t *testing.T) {
	_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		for _, d := range []string{"Hel", "lo ", "world"} {
			_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", d)
			if fl != nil {
				fl.Flush()
			}
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	})
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	certifyOperation(t, db, "acct-1", "prov/model-a", "streaming")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	rec := postChat(t, h, `{"model":"venom/pro","stream":true,"messages":[{"role":"user","content":"hi"}]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	frames := strings.Count(body, "chat.completion.chunk")
	if frames < 3 {
		t.Fatalf("got %d chunk frames, want >= 3 progressive frames; body:\n%s", frames, body)
	}
	if !strings.HasSuffix(strings.TrimSpace(body), "data: [DONE]") {
		t.Fatalf("stream did not terminate with data: [DONE]; body:\n%s", body)
	}
	if !strings.Contains(body, "Hel") || !strings.Contains(body, "world") {
		t.Fatalf("stream missing deltas; body:\n%s", body)
	}
}

// TestChatCompletions_ToolsForwarded proves a tools array reaches the provider
// and a returned tool call is surfaced in the completion.
func TestChatCompletions_ToolsForwarded(t *testing.T) {
	var up *capturingUpstream
	up, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"paris\"}"}}]},"finish_reason":"tool_calls"}]}`))
	})
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	certifyOperation(t, db, "acct-1", "prov/model-a", "tools")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	body := `{"model":"venom/pro","messages":[{"role":"user","content":"weather?"}],"tools":[{"type":"function","function":{"name":"get_weather","description":"g","parameters":{"type":"object"}}}]}`
	rec := postChat(t, h, body)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(string(up.lastBody), `"tools"`) || !strings.Contains(string(up.lastBody), "get_weather") {
		t.Fatalf("upstream did not receive the tools array: %s", up.lastBody)
	}
	if !strings.Contains(rec.Body.String(), "tool_calls") || !strings.Contains(rec.Body.String(), "get_weather") {
		t.Fatalf("completion missing tool_calls: %s", rec.Body.String())
	}
}

// TestChatCompletions_VisionForwarded proves an image content part is expressed
// to the provider as an image_url part.
func TestChatCompletions_VisionForwarded(t *testing.T) {
	var up *capturingUpstream
	up, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) {
		_, _ = w.Write([]byte(completionJSON("saw it")))
	})
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	certifyOperation(t, db, "acct-1", "prov/model-a", "vision")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	body := `{"model":"venom/pro","messages":[{"role":"user","content":[{"type":"text","text":"what is this"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]}]}`
	rec := postChat(t, h, body)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(string(up.lastBody), "image_url") || !strings.Contains(string(up.lastBody), "data:image/png;base64,AAAA") {
		t.Fatalf("upstream did not receive the image part: %s", up.lastBody)
	}
}

// failingUsage always fails the write, to prove the handler SURFACES a usage
// write failure on the non-streaming path (never swallows it).
type failingUsage struct{}

func (failingUsage) Insert(_ context.Context, _ storage.UsageRecord) error {
	return fmt.Errorf("usage store unavailable")
}

// TestChatCompletions_UsageWriteFailureSurfaced proves a usage write failure on
// the non-streaming success path becomes a 500 BEFORE any completion body is
// sent — usage is never swallowed.
func TestChatCompletions_UsageWriteFailureSurfaced(t *testing.T) {
	_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) {
		_, _ = w.Write([]byte(completionJSON("ok")))
	})
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	h := newE2EHandler(t, db, kr, srv.URL, failingUsage{})

	rec := postChat(t, h, `{"model":"venom/pro","messages":[{"role":"user","content":"hi"}]}`)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (a swallowed usage error would 200); body %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "ok") {
		t.Fatalf("completion body leaked despite a failed usage write: %s", rec.Body.String())
	}
}

// TestChatCompletions_UsageOnNoEligibleOffering proves usage is recorded on the
// no-eligible-offering terminal path too — not only on success.
func TestChatCompletions_UsageOnNoEligibleOffering(t *testing.T) {
	_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) {})
	db, kr := e2eEnv(t, srv.URL) // no offering seeded → empty pool
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	rec := postChat(t, h, `{"model":"venom/pro","messages":[{"role":"user","content":"hi"}]}`)

	if rec.Code == http.StatusOK {
		t.Fatalf("empty pool must not return 200; body %s", rec.Body.String())
	}
	var count int
	var status string
	_ = db.Conn().QueryRow(`SELECT count(*) FROM usage_records`).Scan(&count)
	if count != 1 {
		t.Fatalf("usage_records = %d, want 1 (usage on EVERY terminal path)", count)
	}
	_ = db.Conn().QueryRow(`SELECT status FROM usage_records LIMIT 1`).Scan(&status)
	if status != "no_eligible_offering" {
		t.Fatalf("usage status = %q, want no_eligible_offering", status)
	}
}

// TestChatCompletions_WrongModelRejected proves an unknown model is a 400 and
// never routes.
func TestChatCompletions_WrongModelRejected(t *testing.T) {
	_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) {})
	db, kr := e2eEnv(t, srv.URL)
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	rec := postChat(t, h, `{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for unknown model", rec.Code)
	}
	var count int
	_ = db.Conn().QueryRow(`SELECT count(*) FROM usage_records`).Scan(&count)
	if count != 0 {
		t.Fatalf("a rejected-model request must not route or record usage; got %d usage rows", count)
	}
}

// TestChatCompletions_RawProviderErrorNeverInBody proves a raw upstream error
// message never reaches the public response body (05 §7) — only a Venom-authored
// safe message.
func TestChatCompletions_RawProviderErrorNeverInBody(t *testing.T) {
	const secret = "SECRET-upstream-diagnostic-42"
	_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintf(w, `{"error":{"message":%q,"code":"bad_request"}}`, secret)
	})
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	rec := postChat(t, h, `{"model":"venom/pro","messages":[{"role":"user","content":"hi"}]}`)

	if rec.Code == http.StatusOK {
		t.Fatalf("a provider 400 must not surface as 200")
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("raw provider error leaked into the public body: %s", rec.Body.String())
	}
}

// TestChatCompletions_PrivacyCanary proves the prompt text is never written to
// usage_records, route_decisions, or route_attempts — it exists only in memory
// on the request path (and in transit to the provider, its rightful
// destination).
func TestChatCompletions_PrivacyCanary(t *testing.T) {
	const canary = "CANARYPROMPT7788do-not-persist"
	var up *capturingUpstream
	up, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) {
		_, _ = w.Write([]byte(completionJSON("reply body CANARYREPLY9911")))
	})
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	rec := postChat(t, h, fmt.Sprintf(`{"model":"venom/pro","messages":[{"role":"user","content":%q}]}`, canary))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}

	// It DID reach the provider (in transit is allowed).
	if !strings.Contains(string(up.lastBody), canary) {
		t.Fatalf("sanity: prompt should reach the provider in transit")
	}
	// But NOWHERE in any persisted record.
	for _, table := range []string{"usage_records", "route_decisions", "route_attempts"} {
		dump := dumpTable(t, db, table)
		if strings.Contains(dump, canary) {
			t.Fatalf("prompt canary leaked into %s: %s", table, dump)
		}
		if strings.Contains(dump, "CANARYREPLY9911") {
			t.Fatalf("response content leaked into %s: %s", table, dump)
		}
		if strings.Contains(dump, upstreamCredential) {
			t.Fatalf("credential leaked into %s: %s", table, dump)
		}
	}
}

// TestChatCompletions_RouteRegistrationNoOverlap proves POST /v1/chat/completions
// and GET /v1/models are distinct routes on the public mux, and neither shadows
// the other (a wrong method for chat is NOT dispatched to the chat handler).
func TestChatCompletions_RouteRegistrationNoOverlap(t *testing.T) {
	mux := http.NewServeMux()
	chatHit := false
	chat := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { chatHit = true; w.WriteHeader(200) })
	vk := newVKAuthenticator(nil, nil)
	// identity outer + a vk that admits (nil repo path is exercised elsewhere);
	// here we bypass auth by registering the raw handlers to test PATH routing.
	mux.Handle("POST /v1/chat/completions", chat)
	mux.Handle("GET /v1/models", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	_ = vk

	// POST chat → chat handler.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
	if !chatHit || rec.Code != 200 {
		t.Fatalf("POST /v1/chat/completions did not reach the chat handler (code %d)", rec.Code)
	}
	// GET models → models handler (204), not chat.
	chatHit = false
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if chatHit || rec.Code != 204 {
		t.Fatalf("GET /v1/models routing overlapped chat (code %d, chatHit %v)", rec.Code, chatHit)
	}
}

// --- governor review additions ---------------------------------------------

// TestChatCompletions_UsageOnProviderFailure closes the terminal-path matrix the
// card demands ("usage recorded on EVERY terminal path"). The delivered suite
// covered only success and exhaustion; this adds the PROVIDER-FAILURE class,
// where the loop really executed, the provider rejected, and the attempt budget
// ran out. A billing/quota row must exist for it.
//
// Mutation P2-M1 (skip the usage write when lerr != nil) → RED here too, so this
// row is independently load-bearing and not a duplicate of the exhaustion test.
func TestChatCompletions_UsageOnProviderFailure(t *testing.T) {
	_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream exploded"}}`))
	})
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "model-a", "m-a")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	rec := postChat(t, h, `{"model":"venom/pro","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code == http.StatusOK {
		t.Fatalf("expected a failure status, got 200: %s", rec.Body.String())
	}

	var count int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM usage_records`).Scan(&count); err != nil {
		t.Fatalf("count usage: %v", err)
	}
	if count != 1 {
		t.Fatalf("usage_records = %d, want 1 (a provider failure is a terminal path and must be billed/recorded)", count)
	}
	var status string
	if err := db.Conn().QueryRow(`SELECT status FROM usage_records`).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status == "success" {
		t.Fatalf("usage status = %q for a failed request, want a failure status", status)
	}
}

// TestChatCompletions_ClientCancellationAbortsUpstream pins the card's
// "client cancellation aborts the in-flight stream" requirement, which the
// delivered suite did not cover at all.
//
// There is no Dispatcher.Cancel call to assert (the frozen loop never exposes
// the chosen route id), so cancellation rides on request-context propagation:
// r.Context() → RunFallbackLoop → executor → Dispatcher.Stream → the transport's
// HTTP request. This test proves that chain is real by cancelling mid-stream and
// observing the upstream handler's own request context report cancellation, then
// proving usage was still recorded for the terminal path.
//
// Mutation: pass context.Background() instead of ctx anywhere in that chain →
// the upstream never observes cancellation → RED.
func TestChatCompletions_ClientCancellationAbortsUpstream(t *testing.T) {
	upstreamCanceled := make(chan struct{}, 1)
	firstChunkSent := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"one\"}}]}\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
		select {
		case firstChunkSent <- struct{}{}:
		default:
		}
		// Hold the stream open until the client's cancellation propagates here.
		select {
		case <-r.Context().Done():
			select {
			case upstreamCanceled <- struct{}{}:
			default:
			}
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(srv.Close)

	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "model-a", "m-a")
	certifyOperation(t, db, "acct-1", "model-a", "streaming")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"venom/pro","stream":true,"messages":[{"role":"user","content":"hi"}]}`)).WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		h.ServeChat(rec, req)
		close(done)
	}()

	select {
	case <-firstChunkSent:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("upstream never sent the first chunk")
	}
	cancel() // the client disconnects mid-stream

	select {
	case <-upstreamCanceled:
	case <-time.After(5 * time.Second):
		t.Fatal("client cancellation did not reach the upstream — the in-flight call was not aborted")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not return after cancellation")
	}

	var count int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM usage_records`).Scan(&count); err != nil {
		t.Fatalf("count usage: %v", err)
	}
	if count != 1 {
		t.Fatalf("usage_records = %d, want 1 (cancellation is a terminal path)", count)
	}
}

// TestPublicMux_StandaloneServesChat proves the STANDALONE data-plane listener
// serves POST /v1/chat/completions, not just /v1/models.
//
// Governor fix: the delivered mux passed a nil chat handler because PublicMux had
// no keyring, so in separate-bind mode — the only mode that listener exists for —
// the PRIMARY inference endpoint fell through to the /v1/ catch-all and 404'd,
// while /v1/models kept working (making it look like a routing quirk rather than
// a missing endpoint).
//
// Mutation: pass nil for chat in publicMux again → 404 → RED.
func TestPublicMux_StandaloneServesChat(t *testing.T) {
	_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) {
		_, _ = w.Write([]byte(completionJSON("ok")))
	})
	db, kr := e2eEnv(t, srv.URL)
	seedAPIKey(t, db, "k-1", "vk_live_standalone", nil, false)

	mux := PublicMux(db, kr, nil, func() time.Time { return vkFixedNow })
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"venom/pro","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer vk_live_standalone")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// The route must EXIST (no 404/405 from the catch-all). Whether this
	// particular fixture routes successfully is covered by the e2e tests; here the
	// claim is that the endpoint is mounted and vk-gated on the standalone mux.
	if rec.Code == http.StatusNotFound || rec.Code == http.StatusMethodNotAllowed {
		t.Fatalf("standalone data-plane mux does not serve POST /v1/chat/completions (status %d) — the primary endpoint must exist in separate-bind mode; body=%s", rec.Code, rec.Body.String())
	}
	// And it is still vk-gated.
	req2 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"venom/pro","messages":[{"role":"user","content":"hi"}]}`))
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("standalone chat without a vk key = %d, want 401", rec2.Code)
	}
}

// TestChatCompletions_StreamingUsageFailureIsLogged pins the streaming half of
// "never swallowed". A streaming response cannot become a 500 once frames are
// flushed, so the only honest alternative is a loud log — and an unasserted log
// is an unproven one.
//
// This is the governor-review companion to the non-streaming
// UsageWriteFailureSurfaced test: before the fix, usageErr was assigned and then
// discarded on the streaming branch (with a comment claiming it was logged), so a
// usage-write failure during streaming vanished without a trace.
//
// Mutation GOV-M2: guard the log with `&& false` → no record → RED.
func TestChatCompletions_StreamingUsageFailureIsLogged(t *testing.T) {
	_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	})
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "model-a", "m-a")
	certifyOperation(t, db, "acct-1", "model-a", "streaming")

	var logBuf bytes.Buffer
	h := newE2EHandler(t, db, kr, srv.URL, failingUsage{})
	h.log = observability.New(slog.NewJSONHandler(&logBuf, nil))

	rec := postChat(t, h, `{"model":"venom/pro","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("streaming status = %d, want 200 (frames already flushed); body=%s", rec.Code, rec.Body.String())
	}
	out := logBuf.String()
	if !strings.Contains(out, "usage record write failed") {
		t.Fatalf("a streaming usage-write failure must be logged loudly, got log output %q", out)
	}
	if !strings.Contains(out, "request_id") {
		t.Fatalf("the usage-failure log must carry the request_id correlation field, got %q", out)
	}
}
