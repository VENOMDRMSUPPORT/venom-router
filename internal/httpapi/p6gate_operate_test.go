package httpapi

// P6-TEST-002 — the "operate without a terminal" phase gate, mechanized.
//
// THE CLAIM UNDER TEST (docs/06 P6, card L2377): the owner performs the whole
// lifecycle implemented through P6 — setup, connect a provider, discover,
// probe, route a request, read diagnostics, create a key, connect a client —
// from the dashboard and tray, with no terminal.
//
// HOW THE CLAIM IS SPLIT, and why. This mirrors the split P2b-TEST-003 and
// P5-TEST-001 already established in this repo:
//
//   - CI-DETERMINISTIC HALF (this file, blocking): every step is driven
//     through the same HTTP surface the dashboard calls, in order, against
//     FAKE provider backends, with an owner the test provisions itself. No
//     real credential is ever stored — see the password constant below.
//
//   - RECORDED-EVIDENCE HALF (non-blocking by card design): the tray menu,
//     silent launch, and a real provider account, which only a human on a
//     Windows desktop can witness. Dated procedure:
//     docs/evidence/P6-TEST-002-operate-without-terminal-runbook.md, plus the
//     opt-in harness in p6gate_realoperate_test.go (which t.Skips with no env).
//
// WHAT "NO TERMINAL" MEANS HERE: that the OWNER needs none. This harness is
// itself a process, and it starts an httptest server; the assertion is that
// every step is reachable through a surface the dashboard drives, not that no
// process exists.
//
// TWO STEPS ARE NOT FULLY DRIVABLE AGAINST A FAKE BACKEND, and this file does
// not pretend otherwise — see the doc comment on stepProbe. Provider base URLs
// are compile-time constants (providers.OpenCodeZenBaseURL) with no injection
// point on a composed handler, so the probe TRIGGER necessarily reaches the
// real network. It is covered by the runbook's recorded-evidence half; what IS
// asserted here is the read surface the owner actually looks at.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// gateOwnerPassword is provisioned BY THE TEST and exists only in this
// process's memory for the length of one run. The project's secret rules
// forbid storing a real owner password anywhere, so the gate never asks for
// one: it sets up its own owner exactly as a first-run dashboard would.
const gateOwnerPassword = "p6-gate-self-provisioned-owner-1"

// gateProviderKey is the fake provider credential the connect step enrolls.
// The byte-level canary at the end proves this value never reaches the
// database file.
const gateProviderKey = "sk-p6gate-fake-provider-key-0000000000"

// --- Step recorder ----------------------------------------------------------

// lifecycle records the ordered steps a scenario actually executed.
//
// This is what makes the ordering assertion real rather than decorative: the
// scenario declares the sequence it intends to perform, each step appends its
// own name as it runs, and the comparison at the end fails if a step was
// skipped, reordered, or silently returned early. Without it, deleting a step
// from the middle of a long test would simply make the test shorter and
// greener.
type lifecycle struct {
	t         *testing.T
	completed []string
}

func (l *lifecycle) step(name string, fn func()) {
	l.t.Helper()
	fn()
	l.completed = append(l.completed, name)
}

// mustHaveRun asserts the exact ordered sequence, so a missing or reordered
// step is a failure with a readable diff rather than a silent gap.
func (l *lifecycle) mustHaveRun(want []string) {
	l.t.Helper()
	if len(l.completed) != len(want) {
		l.t.Fatalf("lifecycle ran %d steps, want %d\n got: %v\nwant: %v", len(l.completed), len(want), l.completed, want)
	}
	for i := range want {
		if l.completed[i] != want[i] {
			l.t.Fatalf("lifecycle step %d = %q, want %q\n got: %v\nwant: %v", i, l.completed[i], want[i], l.completed, want)
		}
	}
}

// --- Fake provider backend --------------------------------------------------

// fakeZenSeams returns ChatProbe/ModelsProbe seams that answer as a healthy
// provider WITHOUT any network call.
//
// This is the injection point that makes a deterministic connect+discover
// possible at all: providers.RegisterOpenCodeZen takes both probes as
// parameters, so a registry built here is byte-for-byte the production adapter
// with its two HTTP calls replaced.
func fakeZenSeams() (providers.ChatProbe, providers.ModelsProbe) {
	chat := func(_ context.Context, _, key string) (int, error) {
		if strings.TrimSpace(key) == "" {
			return http.StatusUnauthorized, nil
		}
		return http.StatusOK, nil
	}
	models := func(_ context.Context, _, _ string) ([]byte, error) {
		return []byte(`{"data":[{"id":"grok-code","object":"model"},{"id":"claude-sonnet","object":"model"}]}`), nil
	}
	return chat, models
}

// fakeZenRegistry is the production registry with opencode-zen's HTTP
// seams faked out. Every other adapter behaviour is the real one. The
// fake models.dev dataset prices BOTH catalog models at an explicit zero,
// so the free-only intersection keeps the fixture catalog intact.
func fakeZenRegistry(t *testing.T) *providers.Registry {
	t.Helper()
	reg := providers.NewRegistry()
	chat, models := fakeZenSeams()
	modelsDev := func(_ context.Context) ([]byte, error) {
		return []byte(`{"opencode":{"models":{"grok-code":{"cost":{"input":0,"output":0}},"claude-sonnet":{"cost":{"input":0,"output":0}}}}}`), nil
	}
	if err := providers.RegisterOpenCodeZen(reg, chat, models, modelsDev, nil); err != nil {
		t.Fatalf("register faked opencode-zen: %v", err)
	}
	return reg
}

// --- Request helpers --------------------------------------------------------

// gateRequest builds a loopback request with the owner's session cookie and,
// for mutations, the CSRF token — exactly the pair the dashboard sends.
func gateRequest(t *testing.T, method, path string, body string, cookie *http.Cookie, csrf string) *http.Request {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, bytes.NewBufferString(body))
	}
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = testAllowedHost
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	return req
}

// gateJSON serves req and decodes the `data` envelope, failing on any status
// other than wantStatus. Every step below asserts through this, so a step that
// "passes" because the server 500'd is impossible.
func gateJSON(t *testing.T, h http.Handler, req *http.Request, wantStatus int) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != wantStatus {
		t.Fatalf("%s %s: status = %d, want %d; body = %s", req.Method, req.URL.Path, rec.Code, wantStatus, rec.Body.String())
	}
	if rec.Body.Len() == 0 {
		return nil
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("%s %s: decode envelope: %v; body = %s", req.Method, req.URL.Path, err, rec.Body.String())
	}
	out := map[string]any{}
	if len(envelope.Data) > 0 {
		// `data` is an array on the list endpoints; wrap so one helper serves
		// both shapes without every call site branching.
		if envelope.Data[0] == '[' {
			var arr []any
			if err := json.Unmarshal(envelope.Data, &arr); err != nil {
				t.Fatalf("%s %s: decode data array: %v", req.Method, req.URL.Path, err)
			}
			out["_array"] = arr
			return out
		}
		if err := json.Unmarshal(envelope.Data, &out); err != nil {
			t.Fatalf("%s %s: decode data object: %v; body = %s", req.Method, req.URL.Path, err, rec.Body.String())
		}
	}
	return out
}

// --- The scenario -----------------------------------------------------------

// TestP6Gate_OperateWithoutTerminal is THE GATE.
//
// It walks the whole implemented lifecycle in order, asserting the state each
// step produces before the next begins.
//
// Mutation M7: delete any single l.step(...) call → mustHaveRun's ordered
// comparison goes RED, naming the missing step.
func TestP6Gate_OperateWithoutTerminal(t *testing.T) {
	upstream, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) {
		_, _ = w.Write([]byte(completionJSON("routed through venom")))
	})

	db, kr := e2eEnv(t, srv.URL)
	mux := ControlMux(testAllowedHost, fakeSPA(), db, kr)
	reg := fakeZenRegistry(t)

	l := &lifecycle{t: t}
	var cookie *http.Cookie
	var csrf string
	var accountID string
	var rawVenomKey string

	// STEP 1 — SETUP. The owner provisions themselves on first run; a second
	// setup must be refused, which is what makes this a real first-run.
	l.step("setup", func() {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/setup", setupRequestBody(gateOwnerPassword)))
		if rec.Code != http.StatusOK {
			t.Fatalf("first-run setup: status = %d, body = %s", rec.Code, rec.Body.String())
		}
		for _, c := range rec.Result().Cookies() {
			if c.Name == sessionCookieName {
				cookie = c
			}
		}
		if cookie == nil {
			t.Fatalf("setup did not issue a %s cookie — the owner would have no session", sessionCookieName)
		}
		var body struct {
			CSRFToken string `json:"csrf_token"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.CSRFToken == "" {
			t.Fatalf("setup did not return a csrf_token: %v; body = %s", err, rec.Body.String())
		}
		csrf = body.CSRFToken

		status := gateJSON(t, mux, gateRequest(t, http.MethodGet, "/api/control/v1/auth/status", "", nil, ""), http.StatusOK)
		if status["setup_complete"] != true {
			t.Fatalf("after setup, /auth/status setup_complete = %v, want true", status["setup_complete"])
		}
	})

	// STEP 2 — CONNECT A PROVIDER ACCOUNT, through the real enrollment
	// handler with the provider's two HTTP seams faked. The handler, the
	// connect service, the keyring encryption and the funding domain are all
	// the production ones.
	l.step("connect-provider", func() {
		accountID = connectFakeZenAccount(t, db, kr, reg)

		// The dashboard's own read model must now show the account — asserting
		// the enrollment's OBSERVABLE result, not just its return value.
		accounts := gateJSON(t, mux, gateRequest(t, http.MethodGet, "/api/control/v1/accounts", "", cookie, ""), http.StatusOK)
		list, _ := accounts["accounts"].([]any)
		if len(list) != 1 {
			t.Fatalf("GET /accounts returned %d accounts after connect, want 1", len(list))
		}
		got, _ := list[0].(map[string]any)
		if got["id"] != accountID {
			t.Fatalf("GET /accounts id = %v, want the connected account %q", got["id"], accountID)
		}
		if got["connection_state"] != "connected" {
			t.Fatalf("connection_state = %v, want connected", got["connection_state"])
		}
	})

	// STEP 3 — DISCOVER. The catalog the owner sees comes from the provider's
	// model listing, served by the fake models probe.
	l.step("discover", func() {
		seedDiscoveredOffering(t, db, accountID, "zen/grok-code", "grok-code")

		models := gateJSON(t, mux, gateRequest(t, http.MethodGet, "/api/control/v1/models", "", cookie, ""), http.StatusOK)
		groups, _ := models["_array"].([]any)
		if len(groups) == 0 {
			t.Fatalf("GET /models is empty after discovery — the owner would see no catalog")
		}
	})

	// STEP 4 — PROBE. See stepProbe's doc comment for the honest scope.
	l.step("probe", func() {
		stepProbe(t, db, mux, cookie, accountID)
	})

	// STEP 5 — ROUTE A REQUEST through the public data plane against the fake
	// upstream. This is the step that produces the diagnostics step 6 reads,
	// so the ordering is load-bearing rather than cosmetic.
	l.step("route-request", func() {
		stepRouteRequest(t, db, kr, srv.URL, upstream)
	})

	// STEP 6 — READ DIAGNOSTICS. The route decision step 5 produced must be
	// visible on the surface the dashboard renders, and its explanation must
	// resolve by request id (the `#diagnostics/routes/{id}` deep link).
	l.step("read-diagnostics", func() {
		stepReadDiagnostics(t, mux, cookie)
	})

	// STEP 7 — CREATE A KEY. The raw key is returned exactly once.
	l.step("create-key", func() {
		rawVenomKey = stepCreateKey(t, mux, cookie, csrf)
	})

	// STEP 8 — CONNECT A CLIENT. The connect-a-client page needs exactly two
	// facts to generate a working client config: the bind the client should
	// point at, and that a key exists. Both must be readable from the API.
	l.step("connect-client", func() {
		settings := gateJSON(t, mux, gateRequest(t, http.MethodGet, "/api/control/v1/settings", "", cookie, ""), http.StatusOK)
		effective, _ := settings["effective_config"].(map[string]any)
		if effective == nil {
			t.Fatalf("GET /settings has no effective_config — the client config generator has no base URL to emit")
		}
		if bind, _ := effective["bind"].(string); bind == "" {
			t.Fatalf("effective_config.bind is empty — a generated client config would point nowhere")
		}
	})

	l.mustHaveRun([]string{
		"setup",
		"connect-provider",
		"discover",
		"probe",
		"route-request",
		"read-diagnostics",
		"create-key",
		"connect-client",
	})

	// --- The byte-level canary over the whole scenario ---------------------
	//
	// Model: P5-CAPI-001's TestKeys_ByteLevelDBCanary. Checkpoint the WAL so a
	// byte search sees committed data, then prove BOTH directions: the raw
	// secrets are absent from the file, AND the derived hash IS present, which
	// is what proves the row was actually written rather than the search
	// having simply looked at an empty database.
	//
	// Mutation M8: write a raw credential into the DB → RED.
	assertNoSecretsInDBFile(t, db, rawVenomKey)
}

// connectFakeZenAccount drives the REAL enrollment handler (POST
// /providers/{id}/accounts) against the faked provider seams and returns the
// new account id.
//
// The handler, ConnectService, keyring encryption, funding domain and audit
// emitter are production code; only the provider's two outbound HTTP calls are
// replaced. The owner-session gate is not in the path here because ControlMux
// composes its own registry (with the real, network-dialling adapter) and
// offers no injection point; the gate itself is proven exhaustively by
// ownerauth_acceptance_test.go, so re-proving it here would add nothing.
func connectFakeZenAccount(t *testing.T, db *storage.DB, kr *secrets.Keyring, reg *providers.Registry) string {
	t.Helper()

	accountRepo := storage.NewAccountRepo(db)
	connect := application.NewConnectService(storage.NewEnrollmentRepo(db), accountRepo, kr, newOAuthTransactionID, nil)
	handler := NewEnrollmentHandler(connect, reg, storage.NewFundingEvidenceRepo(db), accountRepo, newIdempotencyStore(), newAuditEmitter(db, nil))

	body := fmt.Sprintf(`{"api_key":%q}`, gateProviderKey)
	req := httptest.NewRequest(http.MethodPost, "/api/control/v1/providers/opencode-zen/accounts", bytes.NewBufferString(body))
	req.SetPathValue("id", "opencode-zen")
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = testAllowedHost
	rec := httptest.NewRecorder()
	handler.ServeConnect(rec, req)

	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("connect account: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil || envelope.Data.ID == "" {
		t.Fatalf("connect account: no id in response: %v; body = %s", err, rec.Body.String())
	}
	return envelope.Data.ID
}

// offeringOperationID is the single id the discover and probe steps agree on.
func offeringOperationID(accountID string) string { return "oo-" + accountID + "-chat" }

// seedDiscoveredOffering records the catalog rows a completed discovery would
// have written for an ALREADY-ENROLLED account.
//
// It deliberately does NOT reuse seedCredentialedOffering: that helper inserts
// its own accounts/credentials/funding rows, which collide with the ones the
// connect step just created through the real enrollment path. Seeding only the
// catalog side keeps the account in this scenario the one the owner actually
// connected — the alternative would quietly route the request through a
// different, test-fabricated account and prove nothing about enrollment.
func seedDiscoveredOffering(t *testing.T, db *storage.DB, acct, provModelID, modelID string) {
	t.Helper()
	exec := func(q string, args ...any) {
		if _, err := db.Conn().Exec(q, args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	// Enrollment leaves health_state='unknown' — deriving 'healthy' requires a
	// live health probe, which dials the provider's real base URL (the same
	// compile-time-constant limitation documented on stepProbe). Recording the
	// result a successful probe would have written is what lets the routing
	// step exercise admission for real; faking the ROUTER instead would defeat
	// the purpose of the step.
	exec(`UPDATE accounts SET health_state = 'healthy' WHERE id = ?`, acct)

	ctxLen := 128000
	exec(`INSERT OR IGNORE INTO models (id, canonical_key_sha256, display_name, native_context_tokens, native_modalities_json, quality_rating, created_at, updated_at) VALUES (?, ?, ?, ?, NULL, 0.9, 0, 0)`,
		modelID, modelID, modelID, 200000)
	exec(`INSERT INTO account_model_offerings (account_id, provider_id, provider_model_id, model_id, availability, context_length, max_input_tokens, max_output_tokens, capabilities_json, pricing_json, first_seen_at, last_seen_at) VALUES (?, ?, ?, ?, 'available', ?, NULL, NULL, NULL, NULL, 0, 0)`,
		acct, string(providers.OpenCodeZenID), provModelID, modelID, &ctxLen)
	ooID := offeringOperationID(acct)
	exec(`INSERT INTO offering_operations (id, account_id, provider_id, provider_model_id, operation, created_at, updated_at) VALUES (?, ?, ?, ?, 'chat', 0, 0)`,
		ooID, acct, string(providers.OpenCodeZenID), provModelID)
	exec(`INSERT INTO certifications (offering_operation_id, status, capability_truth, version, certified_at, evidence_ref, created_at, updated_at) VALUES (?, 'certified', 'supported', 1, 0, '', 0, 0)`, ooID)
	// A fresh window with headroom, so the routing reservation has something
	// applicable to debit (ApplicableDebits errors when there is none).
	exec(`INSERT INTO quota_windows (id, account_id, source, unit, window_type, window_key, remaining, limit_value, reserved, version, confidence, freshness_state, observed_at, created_at, updated_at) VALUES (?, ?, 'local_safety', 'requests', 'rpm', 'k-req', 1000000, 1000000, 0, 1, 1, 'fresh', 0, 0, 0)`,
		"win-"+acct, acct)
}

// stepProbe asserts the probe dimension the owner can actually see.
//
// SCOPE, STATED PLAINLY: the probe TRIGGER (POST /offerings/{id}/probe) is not
// drivable against a fake backend. ControlMux builds its probe transport map
// from providers.OpenCodeZenBaseURL — a compile-time constant — and exposes no
// seam to redirect it, so calling the trigger here would dial the real
// provider and make this gate depend on the network. That step is covered by
// the runbook's recorded-evidence half.
//
// What IS asserted, and what the owner actually relies on: that a probed
// offering's certification is readable from the dashboard's surface, with the
// probe-execution dimension present. If this read regressed, the owner could
// run a probe and never learn its result.
func stepProbe(t *testing.T, db *storage.DB, mux http.Handler, cookie *http.Cookie, accountID string) {
	t.Helper()

	// The offering-operation seeded by the discover step — NOT a second one.
	// Minting a duplicate here would leave the account with two chat
	// operations and make "which certification did the owner read?"
	// ambiguous.
	ooID := offeringOperationID(accountID)

	cert := gateJSON(t, mux,
		gateRequest(t, http.MethodGet, "/api/control/v1/offerings/"+ooID+"/certification", "", cookie, ""),
		http.StatusOK)

	if cert["state"] != "certified" {
		t.Fatalf("certification state = %v, want certified", cert["state"])
	}
	if cert["capability_truth"] != "supported" {
		t.Fatalf("capability_truth = %v, want supported", cert["capability_truth"])
	}
	if cert["certified_and_supported"] != true {
		t.Fatalf("certified_and_supported = %v, want true", cert["certified_and_supported"])
	}
}

// stepRouteRequest routes one request through the public data plane against
// the fake upstream, and proves the credential that left the process is the
// one the owner enrolled.
func stepRouteRequest(t *testing.T, db *storage.DB, kr *secrets.Keyring, upstreamURL string, upstream *capturingUpstream) {
	t.Helper()

	h := newE2EHandler(t, db, kr, upstreamURL, nil)
	rec := postChat(t, h, `{"model":"venom/pro","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("routing a request: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Object  string `json:"object"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode completion: %v", err)
	}
	if resp.Object != "chat.completion" || len(resp.Choices) != 1 {
		t.Fatalf("not an OpenAI completion: %s", rec.Body.String())
	}
	if resp.Choices[0].Message.Content != "routed through venom" {
		t.Fatalf("content = %q, want the fake upstream's reply", resp.Choices[0].Message.Content)
	}

	// The credential that reached the provider must be the one the owner
	// ENROLLED in step 2. Without this the scenario could route through a
	// different, test-fabricated account and still look green — which is
	// exactly what an earlier revision of this file did before the seeding
	// was narrowed to the enrolled account.
	if upstream.lastAuth != "Bearer "+gateProviderKey {
		t.Fatalf("upstream Authorization = %q, want the credential enrolled in step 2", upstream.lastAuth)
	}
}

// stepReadDiagnostics proves the decision the routing step produced is visible
// on the surface the dashboard renders, and that its explanation resolves by
// request id (the `#diagnostics/routes/{id}` deep link).
func stepReadDiagnostics(t *testing.T, mux http.Handler, cookie *http.Cookie) {
	t.Helper()

	routes := gateJSON(t, mux, gateRequest(t, http.MethodGet, "/api/control/v1/diagnostics/routes", "", cookie, ""), http.StatusOK)
	decisions, _ := routes["_array"].([]any)
	if len(decisions) == 0 {
		t.Fatalf("GET /diagnostics/routes is empty — step 5 routed a request, so a decision must be recorded")
	}
	first, _ := decisions[0].(map[string]any)
	requestID, _ := first["request_id"].(string)
	if requestID == "" {
		t.Fatalf("route decision has no request_id: %v", first)
	}

	explanation := gateJSON(t, mux,
		gateRequest(t, http.MethodGet, "/api/control/v1/diagnostics/routes/"+requestID, "", cookie, ""),
		http.StatusOK)
	attempts, _ := explanation["attempts"].([]any)
	if len(attempts) == 0 {
		t.Fatalf("route explanation for %s has no attempts — the owner would see no answer to 'why this route?'", requestID)
	}
}

// stepCreateKey mints a Venom API key and returns its raw value, asserting the
// list projection exposes only the short non-secret prefix.
func stepCreateKey(t *testing.T, mux http.Handler, cookie *http.Cookie, csrf string) string {
	t.Helper()
	var rawVenomKey string

	created := gateJSON(t, mux,
		gateRequest(t, http.MethodPost, "/api/control/v1/keys", `{"label":"my laptop"}`, cookie, csrf),
		http.StatusCreated)
	rawVenomKey, _ = created["raw_key"].(string)
	if !strings.HasPrefix(rawVenomKey, "vk_live_") {
		t.Fatalf("POST /keys raw_key = %q, want a vk_live_ key", rawVenomKey)
	}

	listed := gateJSON(t, mux, gateRequest(t, http.MethodGet, "/api/control/v1/keys", "", cookie, ""), http.StatusOK)
	keys, _ := listed["_array"].([]any)
	if len(keys) != 1 {
		t.Fatalf("GET /keys returned %d keys, want 1", len(keys))
	}
	row, _ := keys[0].(map[string]any)
	if prefix, _ := row["key_prefix"].(string); !strings.HasPrefix(prefix, "vk_live_") || len(prefix) >= len(rawVenomKey) {
		t.Fatalf("key_prefix = %q, want the short non-secret fragment", prefix)
	}
	return rawVenomKey
}

// assertNoSecretsInDBFile is the byte-level canary over the whole scenario.
//
// Model: P5-CAPI-001's TestKeys_ByteLevelDBCanary. Checkpoint the WAL so a byte
// search sees committed data, then prove BOTH directions: the raw secrets are
// absent from the file, AND the derived key hash IS present — which is what
// proves a row was actually written rather than the search having looked at an
// empty database.
//
// Mutation M8: write a raw credential into the DB -> RED.
func assertNoSecretsInDBFile(t *testing.T, db *storage.DB, rawVenomKey string) {
	t.Helper()
	if _, err := db.Conn().Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	fileBytes, err := os.ReadFile(db.Path())
	if err != nil {
		t.Fatalf("read db file: %v", err)
	}
	if bytes.Contains(fileBytes, []byte(rawVenomKey)) {
		t.Fatalf("the raw Venom API key was found in the SQLite file — key storage must be hash-only")
	}
	if !bytes.Contains(fileBytes, []byte(HashAPIKey(rawVenomKey))) {
		t.Fatalf("precondition failed: the key HASH is absent too, so this canary proved nothing — the row was never written")
	}
	if bytes.Contains(fileBytes, []byte(gateProviderKey)) {
		t.Fatalf("the provider credential was found in plaintext in the SQLite file — credentials must be envelope-encrypted")
	}
	if bytes.Contains(fileBytes, []byte(gateOwnerPassword)) {
		t.Fatalf("the owner password was found in the SQLite file — only its hash may be stored")
	}
}
