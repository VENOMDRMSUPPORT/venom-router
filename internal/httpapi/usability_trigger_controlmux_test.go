package httpapi

// usability_trigger_controlmux_test.go proves the ControlMux/boot wiring
// seam for the fast lane (task-8 fix round 1, issue 2): every OTHER trigger
// test in this package (usability_wiring_test.go, discovery_test.go's
// TestDiscover_*UsabilityTrigger* tests) injects the trigger directly onto a
// directly-constructed *DiscoveryHandler, bypassing ControlMux's own
// `discoveryHandler.SetUsabilityTrigger(o.usabilityTrigger)` wiring line
// entirely. Deleting that line (or boot.go's WithUsabilityTrigger(...) call)
// would silently kill the fast lane in production while every one of those
// tests stayed green — this file's tests drive the trigger through the
// REAL ControlMux composition instead, so that specific wiring line is what
// actually gets exercised. The first test pins the wiring line itself (with a
// recording trigger); the second runs the WHOLE chain end to end with the REAL
// UsabilityService as the trigger, so a freshly discovered model must actually
// come out certified/supported.
//
// Getting a genuine 202->completed discovery run through the REAL ControlMux
// requires opencode-zen's REAL provider registry entry (newProviderRegistry,
// provider_registry.go) to succeed — and discovery_acceptance_test.go's own
// doc comment explains why ControlMux deliberately exposes NO
// adapter-injection seam for tests: adding one purely for test convenience
// would be a production change this package's own history forbids. Instead
// of touching production code, this test swaps the process-wide
// http.DefaultTransport — which openCodeZenHTTPClient (opencode_zen_seams.go)
// resolves to, since it sets no Transport of its own — for the duration of
// ONE test, restoring it via t.Cleanup. That intercepts the two outbound
// requests OpenCodeZenAdapter.DiscoverModels actually makes (GET
// {baseURL}/v1/models and GET the public models.dev dataset) with canned,
// in-process responses (plus the chat-completions probe the end-to-end test's
// real UsabilityService then issues): no socket ever opens, no production code
// changes, and the REAL registry + REAL discovery adapter + REAL ControlMux
// wiring all run untouched. This package's tests never use t.Parallel, so the
// global swap cannot race another test in the same binary.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// usabilityTriggerFastLaneFreeModelID is the one model this test's fake
// opencode-zen responses agree exists and is free — it must appear
// identically in both canned bodies below, since production intersects them.
const usabilityTriggerFastLaneFreeModelID = "fast-lane-wiring-free-model"

// usabilityTriggerFakeZenTransport is an in-process http.RoundTripper that
// answers ONLY the two exact requests OpenCodeZenAdapter.DiscoverModels
// makes, with fixed, minimal, successful bodies — anything else fails the
// test loudly rather than silently falling through to a real socket.
type usabilityTriggerFakeZenTransport struct {
	t *testing.T
}

func (rt usabilityTriggerFakeZenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	switch req.URL.String() {
	case providers.OpenCodeZenBaseURL + "/v1/models":
		return usabilityTriggerFakeJSONResponse(req, fmt.Sprintf(`{"data":[{"id":%q}]}`, usabilityTriggerFastLaneFreeModelID)), nil
	case "https://models.dev/api.json":
		return usabilityTriggerFakeJSONResponse(req, fmt.Sprintf(
			`{"opencode":{"models":{%q:{"cost":{"input":0,"output":0},"tool_call":false,"modalities":{"input":[]},"limit":{"context":4096}}}}}`,
			usabilityTriggerFastLaneFreeModelID,
		)), nil
	case providers.OpenCodeZenBaseURL + "/v1/chat/completions":
		// The chat-usability probe itself (probeOpenCodeZenChatUsability). A
		// well-formed completion with a `choices` array is what
		// classifyOpenCodeZenChatUsability reads as zenChatUsable — the ONLY
		// input that ends in certified/supported. The end-to-end test below is
		// the one that reaches here; the recorder-trigger test never probes.
		return usabilityTriggerFakeJSONResponse(req, `{"choices":[{"message":{"role":"assistant","content":"pong"}}]}`), nil
	default:
		rt.t.Fatalf("unexpected outbound HTTP request during the ControlMux fast-lane wiring test: %s %s", req.Method, req.URL.String())
		return nil, fmt.Errorf("unexpected request: %s %s", req.Method, req.URL.String())
	}
}

func usabilityTriggerFakeJSONResponse(req *http.Request, body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Request:    req,
	}
}

// TestControlMux_DiscoverySuccess_FiresUsabilityTriggerThroughRealWiring is
// this file's one test: build the REAL ControlMux with
// WithUsabilityTrigger(recorder), POST a real discovery request through it
// for a real opencode-zen account, and assert the recorder fires exactly
// once with the run's own provider+account ids — proving
// `WithUsabilityTrigger` actually reaches the mux-constructed
// DiscoveryHandler, not just a directly-built one a test happens to hold a
// pointer to.
func TestControlMux_DiscoverySuccess_FiresUsabilityTriggerThroughRealWiring(t *testing.T) {
	// GUARD: this swaps a PROCESS-WIDE global. It is safe only because no test
	// in this package calls t.Parallel() — the moment one does, a parallel test
	// running alongside this one would see the fake transport (or lose it to the
	// Cleanup mid-flight). Keep this package serial, or give these tests their
	// own injected client instead.
	originalTransport := http.DefaultTransport
	http.DefaultTransport = usabilityTriggerFakeZenTransport{t: t}
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	db := testControlDB(t)
	kr := testKeyring(t)

	const providerID = "opencode-zen"
	const accountID = "acct-fast-lane-real-wiring"
	seedFastLaneZenAccount(t, db, kr, accountID)

	trigger, calls := newRecordingUsabilityTrigger()
	mux := ControlMux(testAllowedHost, fakeSPA(), db, kr, WithUsabilityTrigger(trigger))
	cookie, csrfToken := setupOwnerWithCSRF(t, mux)

	req := newAuthRequest(t, http.MethodPost, "/api/control/v1/accounts/"+accountID+"/discover", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrfToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %q", rec.Code, rec.Body.String())
	}
	data := decodeDiscoverResponse(t, rec.Body.Bytes())

	jobs := storage.NewJobRepo(db)
	row := waitForJobTerminal(t, jobs, data.JobID, 5*time.Second)
	if row.Status != storage.JobCompleted {
		t.Fatalf("Status = %q, want completed (error = %+v) — the fake zen transport must have answered as expected", row.Status, row.Error)
	}

	got := waitForUsabilityTriggerCalls(t, calls, 1, 2*time.Second)
	if len(got) != 1 {
		t.Fatalf("usability trigger fired %d times through the REAL ControlMux wiring, want exactly 1: %+v", len(got), got)
	}
	if got[0].providerID != providerID || got[0].accountID != accountID {
		t.Fatalf("usability trigger call = %+v, want provider=%s account=%s", got[0], providerID, accountID)
	}
}

// seedFastLaneZenAccount inserts a connected+healthy opencode-zen account with
// an active API-key credential — the minimum state both tests in this file need
// before a discovery run through the REAL ControlMux can succeed and the fast
// lane can lease anything.
func seedFastLaneZenAccount(t *testing.T, db *storage.DB, kr *secrets.Keyring, accountID string) {
	t.Helper()
	const providerID = "opencode-zen"

	if _, err := db.Conn().Exec(
		`INSERT OR IGNORE INTO providers (id, display_name, auth_mode, funding_mode, funding_locked, created_at, updated_at) VALUES (?, ?, 'api_key', 'owner_policy', 0, 0, 0)`,
		providerID, providerID,
	); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if _, err := db.Conn().Exec(
		`INSERT INTO accounts (id, provider_id, external_id, auth_type, connection_state, health_state, created_at, updated_at)
		 VALUES (?, ?, ?, 'api_key', 'connected', 'healthy', 0, 0)`,
		accountID, providerID, accountID,
	); err != nil {
		t.Fatalf("seed account: %v", err)
	}

	credRepo := storage.NewAccountCredentialRepo(db)
	credSvc := application.NewCredentialService(credRepo, kr, nil)
	if _, err := credSvc.Store(context.Background(), application.StoreCredentialParams{
		ID:           "cred-" + accountID,
		AccountID:    accountID,
		ProviderID:   providerID,
		Kind:         domain.CredentialKindAPIKey,
		Active:       true,
		PlaintextKey: "fake-opencode-zen-key-never-sent-to-a-real-host",
	}); err != nil {
		t.Fatalf("store credential: %v", err)
	}
}

// chatCertificationOf reads the (status, capability_truth) of accountID's chat
// offering-operation for modelID, plus whether the row exists at all.
func chatCertificationOf(t *testing.T, db *storage.DB, accountID, modelID string) (status, truth string, found bool) {
	t.Helper()
	row := db.Conn().QueryRow(
		`SELECT c.status, c.capability_truth
		   FROM offering_operations oo
		   JOIN certifications c ON c.offering_operation_id = oo.id
		  WHERE oo.account_id = ? AND oo.provider_model_id = ? AND oo.operation = 'chat'`,
		accountID, modelID,
	)
	switch err := row.Scan(&status, &truth); {
	case errors.Is(err, sql.ErrNoRows):
		return "", "", false
	case err != nil:
		t.Fatalf("read chat certification: %v", err)
	}
	return status, truth, true
}

// TestControlMux_DiscoverySuccess_FastLaneCertifiesFreshChatModelEndToEnd is
// the end-to-end the plan never had, and finding 1's regression pin.
//
// It runs the WHOLE chain in one process with no sockets: a real discovery run
// through the REAL ControlMux seeds a brand-new model's chat offering-operation
// at `observed`, the REAL UsabilityService (BuildUsabilityService, wired in as
// the trigger exactly as boot.go wires it) fires on discovery success, and the
// model must come out `certified`/`supported`.
//
// The row is `observed`, not `probing`, at the instant the fast lane runs — the
// probe_drain scheduler tick is NOT running in this test, and in production it
// has not ticked yet either. So this test fails outright unless the fast lane
// drives the observed -> probing edge itself: with the sweep's probing-only
// pass the lister returns zero rows, nothing is probed, and the model stays
// exactly as discovery left it.
func TestControlMux_DiscoverySuccess_FastLaneCertifiesFreshChatModelEndToEnd(t *testing.T) {
	// GUARD: process-wide global swap — safe only while this package has zero
	// t.Parallel() calls. See the sibling test above.
	originalTransport := http.DefaultTransport
	http.DefaultTransport = usabilityTriggerFakeZenTransport{t: t}
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	db := testControlDB(t)
	kr := testKeyring(t)

	const accountID = "acct-fast-lane-end-to-end"
	seedFastLaneZenAccount(t, db, kr, accountID)

	// The REAL service, not a recorder: this is the production composition root.
	usability, err := BuildUsabilityService(db, kr, nil)
	if err != nil {
		t.Fatalf("BuildUsabilityService: %v", err)
	}
	mux := ControlMux(testAllowedHost, fakeSPA(), db, kr, WithUsabilityTrigger(usability.VerifyAccount))
	cookie, csrfToken := setupOwnerWithCSRF(t, mux)

	req := newAuthRequest(t, http.MethodPost, "/api/control/v1/accounts/"+accountID+"/discover", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrfToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %q", rec.Code, rec.Body.String())
	}
	data := decodeDiscoverResponse(t, rec.Body.Bytes())

	jobs := storage.NewJobRepo(db)
	row := waitForJobTerminal(t, jobs, data.JobID, 5*time.Second)
	if row.Status != storage.JobCompleted {
		t.Fatalf("discovery Status = %q, want completed (error = %+v)", row.Status, row.Error)
	}

	// Discovery must have seeded the chat op at `observed` — the precondition
	// the whole finding is about. If this ever reads `probing`, something else
	// started driving the edge and this test stopped proving anything.
	status, truth, found := chatCertificationOf(t, db, accountID, usabilityTriggerFastLaneFreeModelID)
	if !found {
		t.Fatalf("discovery created no chat offering-operation for %q", usabilityTriggerFastLaneFreeModelID)
	}
	if status != "observed" && status != "certified" {
		t.Fatalf("chat certification status right after discovery = %q, want observed (or already certified if the fast lane won the race)", status)
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		status, truth, found = chatCertificationOf(t, db, accountID, usabilityTriggerFastLaneFreeModelID)
		if found && status == "certified" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("chat certification never reached certified: status = %q truth = %q — the fast lane did not verify the freshly discovered model", status, truth)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if truth != "supported" {
		t.Fatalf("chat capability_truth = %q, want supported — the fake transport answered with a working completion", truth)
	}
}
