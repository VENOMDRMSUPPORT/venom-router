package httpapi

// discovery_test.go exercises the P3a-CAPI-002 discovery-trigger and
// certification-read surface (internal/httpapi/discovery.go). Functional
// tests build a DiscoveryHandler directly over a fresh migrated DB and a
// test-local providers.Registry holding a deterministic fake
// ModelDiscoveryAdapter (never real network) — mirroring
// newTestEnrollmentHandler's posture in enrollment_test.go. Owner-gating is
// proved separately through the real ControlMux composition.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

func fixedDiscoveryClock() time.Time {
	return time.Date(2026, 7, 26, 15, 0, 0, 0, time.UTC)
}

// fakeDiscoveryAdapter is a deterministic, in-memory ModelDiscoveryAdapter —
// no real network call, no real provider.
type fakeDiscoveryAdapter struct {
	models []providers.DiscoveredModel
	err    error
	calls  int
}

func (a *fakeDiscoveryAdapter) DiscoverModels(_ context.Context, _ providers.StoredCredentials) ([]providers.DiscoveredModel, error) {
	a.calls++
	return a.models, a.err
}

// discoveryIDCounter returns a deterministic id generator ("disc-id-1",
// "disc-id-2", ...) for both job/run ids and DiscoveryRepo's internal row
// ids.
func discoveryIDCounter() func() string {
	n := 0
	return func() string {
		n++
		return fmt.Sprintf("disc-id-%d", n)
	}
}

type discoveryFixtureOpts struct {
	WithDiscovery  bool
	WithCredential bool
}

type discoveryFixture struct {
	handler      *DiscoveryHandler
	db           *storage.DB
	jobs         *storage.JobRepo
	catalog      *storage.CatalogRepo
	discAdapter  *fakeDiscoveryAdapter
	accountID    string
	providerID   string
	credentialID string
}

// newDiscoveryFixture seeds a provider + a connected account, optionally an
// active credential, and optionally a registered fake discovery adapter for
// that provider — the four combinations this handler's precondition checks
// need to exercise independently.
func newDiscoveryFixture(t *testing.T, clock func() time.Time, opts discoveryFixtureOpts) *discoveryFixture {
	t.Helper()
	db := testControlDB(t)

	const providerID = "prov-disc"
	const accountID = "acct-disc"

	if _, err := db.Conn().Exec(
		`INSERT INTO providers (id, display_name, auth_mode, funding_mode, funding_locked, created_at, updated_at) VALUES (?, ?, 'api_key', 'owner_policy', 0, 0, 0)`,
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
	kr := testKeyring(t)
	credSvc := application.NewCredentialService(credRepo, kr, clock)

	var credentialID string
	if opts.WithCredential {
		credentialID = "cred-disc-1"
		if _, err := credSvc.Store(context.Background(), application.StoreCredentialParams{
			ID:           credentialID,
			AccountID:    accountID,
			ProviderID:   providerID,
			Kind:         domain.CredentialKindAPIKey,
			Active:       true,
			PlaintextKey: "canary-secret-key-should-never-leak",
		}); err != nil {
			t.Fatalf("store credential: %v", err)
		}
	}

	reg := providers.NewRegistry()
	discAdapter := &fakeDiscoveryAdapter{}
	def := providers.Definition{ID: providers.ProviderID(providerID), AuthMode: providers.AuthModeAPIKey, Transport: providers.TransportKindOpenAICompatible, APIKey: newFakeAPIKeyAdapter()}
	if opts.WithDiscovery {
		def.Discovery = discAdapter
	}
	if err := reg.Register(def); err != nil {
		t.Fatalf("register fake adapter: %v", err)
	}

	accountRepo := storage.NewAccountRepo(db)
	catalogRepo := storage.NewCatalogRepo(db)
	jobRepo := storage.NewJobRepo(db)
	discoveryRepo := storage.NewDiscoveryRepo(db, discoveryIDCounter())
	audit := newAuditEmitter(db, nil)
	idem := newIdempotencyStore()

	h := NewDiscoveryHandler(accountRepo, credRepo, catalogRepo, jobRepo, discoveryRepo, reg, credSvc, audit, idem, discoveryIDCounter(), clock)

	return &discoveryFixture{
		handler:      h,
		db:           db,
		jobs:         jobRepo,
		catalog:      catalogRepo,
		discAdapter:  discAdapter,
		accountID:    accountID,
		providerID:   providerID,
		credentialID: credentialID,
	}
}

func newTestDiscoveryMux(h *DiscoveryHandler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/control/v1/accounts/{id}/discover", h.ServeDiscover)
	mux.HandleFunc("/api/control/v1/offerings/{id}/certification", h.ServeCertification)
	return mux
}

func discoveryRequest(method, path string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = testAllowedHost
	return req
}

// discoveryRequestWithCancel builds a request whose own context can be
// cancelled by the caller — used to simulate the real net/http server's
// behavior of cancelling a request's context the instant its handler
// returns, which a detached background run must survive.
func discoveryRequestWithCancel(method, path string) (*http.Request, context.CancelFunc) {
	req := discoveryRequest(method, path)
	ctx, cancel := context.WithCancel(req.Context())
	return req.WithContext(ctx), cancel
}

func decodeDiscoverResponse(t *testing.T, body []byte) discoverResponseJSON {
	t.Helper()
	var env struct {
		Data discoverResponseJSON `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode discover response: %v; body = %q", err, body)
	}
	return env.Data
}

// waitForJobTerminal polls jobs.GetByID in a bounded loop until jobID
// reaches a terminal status, failing the test if it never does within
// timeout — deterministic waiting for the detached background goroutine
// ServeDiscover spawns, with no fixed sleep-then-check-once race.
func waitForJobTerminal(t *testing.T, jobs *storage.JobRepo, jobID string, timeout time.Duration) storage.JobRow {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last storage.JobRow
	for {
		row, ok, err := jobs.GetByID(context.Background(), jobID)
		if err != nil {
			t.Fatalf("GetByID(%s): %v", jobID, err)
		}
		if ok {
			last = row
			switch row.Status {
			case storage.JobCompleted, storage.JobFailed, storage.JobExpired:
				return row
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s did not reach a terminal state within %v (last = %+v)", jobID, timeout, last)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func countRowsQuery(t *testing.T, db *storage.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.Conn().QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	return n
}

// --- POST /accounts/{id}/discover ---

func TestDiscover_Returns202WithJobAndStatusURL(t *testing.T) {
	clock := fixedDiscoveryClock()
	f := newDiscoveryFixture(t, func() time.Time { return clock }, discoveryFixtureOpts{WithDiscovery: true, WithCredential: true})
	mux := newTestDiscoveryMux(f.handler)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, discoveryRequest(http.MethodPost, "/api/control/v1/accounts/"+f.accountID+"/discover"))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %q", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if strings.Contains(body, `"models"`) || strings.Contains(body, "model-") {
		t.Fatalf("202 response contains inline discovery results, want only {job_id, status_url}: %s", body)
	}

	data := decodeDiscoverResponse(t, rec.Body.Bytes())
	if data.JobID == "" {
		t.Fatalf("job_id is empty")
	}
	if data.StatusURL != "/api/control/v1/jobs/"+data.JobID {
		t.Fatalf("status_url = %q, want /api/control/v1/jobs/%s", data.StatusURL, data.JobID)
	}

	// Polling the canonical shared surface returns this job.
	row, ok, err := f.jobs.GetByID(context.Background(), data.JobID)
	if err != nil || !ok {
		t.Fatalf("GetByID(%s): ok=%v err=%v", data.JobID, ok, err)
	}
	if row.Kind != "discovery" {
		t.Fatalf("Kind = %q, want discovery", row.Kind)
	}

	// Wait for the detached background run to finish before returning. The
	// goroutine ServeDiscover spawns outlives this handler call, and this
	// test's DB is a real file under t.TempDir(): letting the goroutine run
	// past the test would leave it querying a DB that t.Cleanup is closing
	// while TempDir's own cleanup tries to delete the still-open file — a
	// Windows-only teardown failure that surfaces on CI, not here.
	waitForJobTerminal(t, f.jobs, data.JobID, 2*time.Second)
}

func TestDiscover_RunsToCompletionAndSetsResultRef(t *testing.T) {
	clock := fixedDiscoveryClock()
	f := newDiscoveryFixture(t, func() time.Time { return clock }, discoveryFixtureOpts{WithDiscovery: true, WithCredential: true})
	f.discAdapter.models = []providers.DiscoveredModel{
		{ProviderModelID: "model-a", DisplayName: "Model A", Capabilities: []string{"chat"}},
		{ProviderModelID: "model-b", DisplayName: "Model B", Capabilities: []string{"chat"}},
	}
	mux := newTestDiscoveryMux(f.handler)

	// Simulate what the real net/http server does: cancel the request's
	// own context the instant the handler returns (the response has
	// already been written). ServeDiscover's background run MUST survive
	// this — it is proof the run uses a detached context, not r.Context()
	// directly.
	req, cancelReqCtx := discoveryRequestWithCancel(http.MethodPost, "/api/control/v1/accounts/"+f.accountID+"/discover")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	cancelReqCtx()
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %q", rec.Code, rec.Body.String())
	}
	data := decodeDiscoverResponse(t, rec.Body.Bytes())

	row := waitForJobTerminal(t, f.jobs, data.JobID, 2*time.Second)
	if row.Status != storage.JobCompleted {
		t.Fatalf("Status = %q, want completed (error = %+v)", row.Status, row.Error)
	}
	// The expected reference is written out LITERALLY rather than computed by
	// calling discoveryResultRef: deriving the expectation from the very
	// function under test would make this assertion tautological, so any
	// change to the reference's shape (e.g. appending run content to it)
	// would pass unnoticed.
	const wantRef = "/api/control/v1/models?account_id=acct-disc"
	if row.ResultRef != wantRef {
		t.Fatalf("ResultRef = %q, want the exact reference %q", row.ResultRef, wantRef)
	}
	// The two model ids this run actually discovered are the sharpest
	// available canaries for 09 §3.12's "a reference, never inline content".
	for _, canary := range []string{"model-a", "model-b", "Model A", "Model B"} {
		if strings.Contains(row.ResultRef, canary) {
			t.Fatalf("ResultRef = %q leaked discovered run content %q", row.ResultRef, canary)
		}
	}
	// started_at is set ONLY by MarkRunning: asserting it here is what proves
	// the production run really transitions pending -> running -> terminal
	// (09 §3.12) rather than jumping straight to a terminal status. The
	// storage-level lifecycle test drives the repo directly, so it cannot
	// prove the HANDLER performs that transition.
	if row.StartedAt == nil {
		t.Fatalf("StartedAt = nil, want the pending -> running transition to have been recorded")
	}
	// MarkTerminal always stores finishedAt+TTL, so a zero TTL still yields a
	// non-nil timestamp — only asserting the actual VALUE pins 09 §3.12's
	// bounded-retention contract at the production call site.
	if row.RetentionUntil == nil {
		t.Fatalf("RetentionUntil = nil, want the terminal write to carry the default retention TTL")
	}
	if row.FinishedAt == nil {
		t.Fatalf("FinishedAt = nil, want a terminal timestamp")
	}
	if got := row.RetentionUntil.Sub(*row.FinishedAt); got != storage.DefaultJobRetention {
		t.Fatalf("retention_until - finished_at = %v, want storage.DefaultJobRetention (%v)", got, storage.DefaultJobRetention)
	}

	offerings, _, err := f.catalog.ListOfferings(context.Background(), storage.CatalogListParams{AccountID: f.accountID, Limit: 10})
	if err != nil {
		t.Fatalf("ListOfferings: %v", err)
	}
	if len(offerings) != 2 {
		t.Fatalf("len(offerings) = %d, want 2 (readable via CatalogRepo after discovery applied)", len(offerings))
	}
}

func TestDiscover_AdapterFailureMarksJobFailedWithTypedError(t *testing.T) {
	clock := fixedDiscoveryClock()
	f := newDiscoveryFixture(t, func() time.Time { return clock }, discoveryFixtureOpts{WithDiscovery: true, WithCredential: true})
	const canary = "sk-live-CANARY-provider-secret-should-never-leak"
	f.discAdapter.err = fmt.Errorf("upstream 500: leaked credential %s", canary)
	mux := newTestDiscoveryMux(f.handler)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, discoveryRequest(http.MethodPost, "/api/control/v1/accounts/"+f.accountID+"/discover"))
	data := decodeDiscoverResponse(t, rec.Body.Bytes())

	row := waitForJobTerminal(t, f.jobs, data.JobID, 2*time.Second)
	if row.Status != storage.JobFailed {
		t.Fatalf("Status = %q, want failed", row.Status)
	}
	if row.Error == nil || row.Error.Code == "" {
		t.Fatalf("Error = %+v, want a typed non-empty code", row.Error)
	}
	// Pin BOTH halves of the typed error, not just "no canary": the code must
	// be intelligence's own reason vocabulary and the message must be a fixed
	// constant. This is what would catch a future edit that starts
	// interpolating an error value into either field (09 §3.12: the job error
	// is "a typed, user-safe {code, message}").
	if row.Error.Code != intelligence.ReasonDiscoveryFailed {
		t.Fatalf("Error.Code = %q, want the typed %q", row.Error.Code, intelligence.ReasonDiscoveryFailed)
	}
	if row.Error.Message != "discovery run failed" {
		t.Fatalf("Error.Message = %q, want the fixed secret-free constant", row.Error.Message)
	}
	if strings.Contains(row.Error.Code, canary) || strings.Contains(row.Error.Message, canary) {
		t.Fatalf("job error leaked the canary secret: %+v", row.Error)
	}
	if strings.Contains(rec.Body.String(), canary) {
		t.Fatalf("HTTP response leaked the canary secret: %s", rec.Body.String())
	}
}

func TestDiscover_NoDiscoveryCapability_409(t *testing.T) {
	f := newDiscoveryFixture(t, nil, discoveryFixtureOpts{WithDiscovery: false, WithCredential: true})
	mux := newTestDiscoveryMux(f.handler)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, discoveryRequest(http.MethodPost, "/api/control/v1/accounts/"+f.accountID+"/discover"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %q", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "discovery_unsupported" {
		t.Fatalf("error code = %q, want discovery_unsupported", code)
	}
	if n := countRowsQuery(t, f.db, `SELECT COUNT(*) FROM jobs`); n != 0 {
		t.Fatalf("jobs row count = %d, want 0 (nothing created)", n)
	}
	if n := countRowsQuery(t, f.db, `SELECT COUNT(*) FROM discovery_runs WHERE account_id = ?`, f.accountID); n != 0 {
		t.Fatalf("discovery_runs row count = %d, want 0 (nothing created)", n)
	}
}

func TestDiscover_NoActiveCredential_409(t *testing.T) {
	f := newDiscoveryFixture(t, nil, discoveryFixtureOpts{WithDiscovery: true, WithCredential: false})
	mux := newTestDiscoveryMux(f.handler)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, discoveryRequest(http.MethodPost, "/api/control/v1/accounts/"+f.accountID+"/discover"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %q", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "credential_unavailable" {
		t.Fatalf("error code = %q, want credential_unavailable", code)
	}
	if n := countRowsQuery(t, f.db, `SELECT COUNT(*) FROM jobs`); n != 0 {
		t.Fatalf("jobs row count = %d, want 0 (nothing created)", n)
	}
}

func TestDiscover_UnknownAccount_404(t *testing.T) {
	f := newDiscoveryFixture(t, nil, discoveryFixtureOpts{WithDiscovery: true, WithCredential: true})
	mux := newTestDiscoveryMux(f.handler)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, discoveryRequest(http.MethodPost, "/api/control/v1/accounts/does-not-exist/discover"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %q", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "not_found" {
		t.Fatalf("error code = %q, want not_found", code)
	}
}

// --- Gating (ControlMux composition) ---

// TestDiscover_IsOwnerGated proves both new routes are registered through
// `gated` (owner session + CSRF): no session -> 401 for both; a session
// with no CSRF token -> csrf_failed 403 on the mutating POST, before any
// job row is created.
func TestDiscover_IsOwnerGated(t *testing.T) {
	db := testControlDB(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))

	for _, req := range []*http.Request{
		newAuthRequest(t, http.MethodPost, "/api/control/v1/accounts/does-not-exist/discover", nil),
		newAuthRequest(t, http.MethodGet, "/api/control/v1/offerings/does-not-exist/certification", nil),
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s unauthenticated status = %d, want 401", req.Method, req.URL.Path, rec.Code)
		}
	}

	cookie, _ := setupOwnerWithCSRF(t, mux)
	req := newAuthRequest(t, http.MethodPost, "/api/control/v1/accounts/does-not-exist/discover", nil)
	req.AddCookie(cookie) // no X-CSRF-Token
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST discover without CSRF status = %d, want 403", rec.Code)
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "csrf_failed" {
		t.Fatalf("error code = %q, want csrf_failed", code)
	}
	if n := countRowsQuery(t, db, `SELECT COUNT(*) FROM jobs`); n != 0 {
		t.Fatalf("jobs row count = %d, want 0 (CSRF rejection happens before any side effect)", n)
	}
}

// --- GET /offerings/{id}/certification ---

func TestCertification_ReadsOfferingOperation(t *testing.T) {
	f := newDiscoveryFixture(t, nil, discoveryFixtureOpts{WithDiscovery: true, WithCredential: true})
	mux := newTestDiscoveryMux(f.handler)

	// Two offering-operations sharing the SAME provider_model_id but
	// different operations/ids — proves the read is keyed on
	// offering_operations.id, never provider_model_id (mutation #9).
	seedModelForCert(t, f.db, "cert-model-1")
	seedOfferingForCert(t, f.db, f.accountID, f.providerID, "cert-model-1", "cert-model-1")
	seedOfferingOperationForCert(t, f.db, "op-cert-chat", f.accountID, f.providerID, "cert-model-1", "chat", "certified", "supported", 3, "ev-x")
	seedOfferingOperationForCert(t, f.db, "op-cert-vision", f.accountID, f.providerID, "cert-model-1", "vision", "discovered", "unknown", 1, "")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, discoveryRequest(http.MethodGet, "/api/control/v1/offerings/op-cert-chat/certification"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}
	var env struct {
		Data certificationJSON `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v; body = %q", err, rec.Body.String())
	}
	if env.Data.OfferingOperationID != "op-cert-chat" || env.Data.Operation != "chat" {
		t.Fatalf("data = %+v, want op-cert-chat/chat (not op-cert-vision, despite the shared provider_model_id)", env.Data)
	}
	if env.Data.State != "certified" || env.Data.CapabilityTruth != "supported" || !env.Data.CertifiedAndSupported {
		t.Fatalf("data = %+v, want certified/supported/routable=true", env.Data)
	}
	if env.Data.EvidenceRef != "ev-x" {
		t.Fatalf("EvidenceRef = %q, want ev-x", env.Data.EvidenceRef)
	}

	// The OTHER offering-operation, same provider_model_id, must render
	// its OWN (non-routable) state.
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, discoveryRequest(http.MethodGet, "/api/control/v1/offerings/op-cert-vision/certification"))
	var env2 struct {
		Data certificationJSON `json:"data"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &env2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env2.Data.State != "discovered" || env2.Data.CertifiedAndSupported {
		t.Fatalf("data = %+v, want discovered/routable=false", env2.Data)
	}

	// Unknown id -> 404.
	rec3 := httptest.NewRecorder()
	mux.ServeHTTP(rec3, discoveryRequest(http.MethodGet, "/api/control/v1/offerings/does-not-exist/certification"))
	if rec3.Code != http.StatusNotFound {
		t.Fatalf("unknown id status = %d, want 404", rec3.Code)
	}
}

// TestCertificationRead_ReviewReasonsAreComputedNotFabricated proves
// review_reasons reports EXACTLY intelligence.AdmissionCapabilityNotCertified
// for a non-routable row, an empty (never null) array for a routable
// row, and never any of the OTHER admission reasons (funding/health/
// quota/cooldown) this read has no basis to assert (P3c-CAPI-001
// GOVERNOR DECISION).
func TestCertificationRead_ReviewReasonsAreComputedNotFabricated(t *testing.T) {
	f := newDiscoveryFixture(t, nil, discoveryFixtureOpts{WithDiscovery: true, WithCredential: true})
	mux := newTestDiscoveryMux(f.handler)

	seedModelForCert(t, f.db, "cert-model-reasons")
	seedOfferingForCert(t, f.db, f.accountID, f.providerID, "cert-model-reasons", "cert-model-reasons")
	seedOfferingOperationForCert(t, f.db, "op-reasons-routable", f.accountID, f.providerID, "cert-model-reasons", "chat", "certified", "supported", 3, "")
	seedOfferingOperationForCert(t, f.db, "op-reasons-blocked", f.accountID, f.providerID, "cert-model-reasons", "vision", "discovered", "unknown", 1, "")

	readReasons := func(id string) []string {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, discoveryRequest(http.MethodGet, "/api/control/v1/offerings/"+id+"/certification"))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body = %q", rec.Code, rec.Body.String())
		}
		var env struct {
			Data certificationJSON `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode: %v; body = %q", err, rec.Body.String())
		}
		if env.Data.ReviewReasons == nil {
			t.Fatalf("review_reasons for %q = nil, want a non-nil (possibly empty) slice", id)
		}
		return env.Data.ReviewReasons
	}

	routable := readReasons("op-reasons-routable")
	if len(routable) != 0 {
		t.Fatalf("review_reasons for a routable row = %v, want []", routable)
	}

	blocked := readReasons("op-reasons-blocked")
	if len(blocked) != 1 || blocked[0] != string(intelligence.AdmissionCapabilityNotCertified) {
		t.Fatalf("review_reasons for a non-routable row = %v, want exactly [%q]", blocked, intelligence.AdmissionCapabilityNotCertified)
	}
	for _, forbidden := range []string{"funding_unknown", "no_healthy_account", "quota_exhausted", "quota_insufficient", "cooling_down"} {
		for _, got := range blocked {
			if got == forbidden {
				t.Fatalf("review_reasons contains fabricated reason %q — this read has no basis to assert it", forbidden)
			}
		}
	}
}

// --- Fast-lane usability trigger (task-8, spec 2026-08-05) ---

// usabilityTriggerCall records one fast-lane invocation for assertions.
type usabilityTriggerCall struct {
	providerID string
	accountID  string
}

// newRecordingUsabilityTrigger builds a fast-lane hook that records every
// call it receives, safe for concurrent use — the production trigger fires
// from a detached goroutine, so the test reading calls() races with it by
// design and must synchronize.
func newRecordingUsabilityTrigger() (trigger func(ctx context.Context, providerID, accountID string), calls func() []usabilityTriggerCall) {
	var mu sync.Mutex
	var recorded []usabilityTriggerCall
	trigger = func(_ context.Context, providerID, accountID string) {
		mu.Lock()
		defer mu.Unlock()
		recorded = append(recorded, usabilityTriggerCall{providerID: providerID, accountID: accountID})
	}
	calls = func() []usabilityTriggerCall {
		mu.Lock()
		defer mu.Unlock()
		out := make([]usabilityTriggerCall, len(recorded))
		copy(out, recorded)
		return out
	}
	return trigger, calls
}

// waitForUsabilityTriggerCalls polls calls() in a bounded loop until it has
// recorded at least `want` invocations, failing the test if it never does
// within timeout — deterministic waiting for the fast lane's own detached
// goroutine, mirroring waitForJobTerminal's approach to the discovery run's
// goroutine.
func waitForUsabilityTriggerCalls(t *testing.T, calls func() []usabilityTriggerCall, want int, timeout time.Duration) []usabilityTriggerCall {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		got := calls()
		if len(got) >= want {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("usability trigger calls = %d after %v, want >= %d", len(got), timeout, want)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestDiscover_SuccessFiresUsabilityTriggerExactlyOnce proves runDiscovery's
// ONE success point fires the fast-lane trigger with the run's own
// provider+account ids, exactly once. This drives the PRODUCTION handler
// path (ServeDiscover -> serveDiscover -> runDiscovery) rather than a
// test-owned assembly of runDiscovery's pieces — the wrapper-hole shape that
// has recurred twice in this project (a wrapper that fires the trigger sits
// beside, not inside, the code the mutation test actually deletes from).
func TestDiscover_SuccessFiresUsabilityTriggerExactlyOnce(t *testing.T) {
	clock := fixedDiscoveryClock()
	f := newDiscoveryFixture(t, func() time.Time { return clock }, discoveryFixtureOpts{WithDiscovery: true, WithCredential: true})
	f.discAdapter.models = []providers.DiscoveredModel{
		{ProviderModelID: "model-a", DisplayName: "Model A", Capabilities: []string{"chat"}},
	}
	trigger, calls := newRecordingUsabilityTrigger()
	f.handler.SetUsabilityTrigger(trigger)
	mux := newTestDiscoveryMux(f.handler)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, discoveryRequest(http.MethodPost, "/api/control/v1/accounts/"+f.accountID+"/discover"))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %q", rec.Code, rec.Body.String())
	}
	data := decodeDiscoverResponse(t, rec.Body.Bytes())

	row := waitForJobTerminal(t, f.jobs, data.JobID, 2*time.Second)
	if row.Status != storage.JobCompleted {
		t.Fatalf("Status = %q, want completed", row.Status)
	}

	got := waitForUsabilityTriggerCalls(t, calls, 1, 2*time.Second)
	if len(got) != 1 {
		t.Fatalf("usability trigger fired %d times, want exactly 1: %+v", len(got), got)
	}
	if got[0].providerID != f.providerID || got[0].accountID != f.accountID {
		t.Fatalf("usability trigger call = %+v, want provider=%s account=%s", got[0], f.providerID, f.accountID)
	}
}

// TestDiscover_FailedRunFiresUsabilityTriggerZeroTimes proves a failed
// discovery run (the adapter itself erroring) never reaches runDiscovery's
// success point, so the fast lane never fires. Unlike the success case this
// needs no bounded wait: the production code path never launches the trigger
// goroutine at all on failure, so there is nothing async to race with —
// asserting zero calls right after the job reaches its terminal (failed)
// state is already deterministic.
func TestDiscover_FailedRunFiresUsabilityTriggerZeroTimes(t *testing.T) {
	clock := fixedDiscoveryClock()
	f := newDiscoveryFixture(t, func() time.Time { return clock }, discoveryFixtureOpts{WithDiscovery: true, WithCredential: true})
	f.discAdapter.err = fmt.Errorf("upstream discovery failure")
	trigger, calls := newRecordingUsabilityTrigger()
	f.handler.SetUsabilityTrigger(trigger)
	mux := newTestDiscoveryMux(f.handler)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, discoveryRequest(http.MethodPost, "/api/control/v1/accounts/"+f.accountID+"/discover"))
	data := decodeDiscoverResponse(t, rec.Body.Bytes())

	row := waitForJobTerminal(t, f.jobs, data.JobID, 2*time.Second)
	if row.Status != storage.JobFailed {
		t.Fatalf("Status = %q, want failed", row.Status)
	}
	if got := calls(); len(got) != 0 {
		t.Fatalf("usability trigger fired %d times on a failed run, want 0: %+v", len(got), got)
	}
}

func seedModelForCert(t *testing.T, db *storage.DB, modelID string) {
	t.Helper()
	if _, err := db.Conn().Exec(
		`INSERT INTO models (id, canonical_key_sha256, created_at, updated_at) VALUES (?, ?, 0, 0)`,
		modelID, modelID+"-key",
	); err != nil {
		t.Fatalf("seed model %s: %v", modelID, err)
	}
}

func seedOfferingForCert(t *testing.T, db *storage.DB, accountID, providerID, providerModelID, modelID string) {
	t.Helper()
	if _, err := db.Conn().Exec(
		`INSERT INTO account_model_offerings (account_id, provider_id, provider_model_id, model_id, availability, first_seen_at, last_seen_at)
		 VALUES (?, ?, ?, ?, 'available', 0, 0)`,
		accountID, providerID, providerModelID, modelID,
	); err != nil {
		t.Fatalf("seed offering (%s,%s): %v", accountID, providerModelID, err)
	}
}

func seedOfferingOperationForCert(t *testing.T, db *storage.DB, id, accountID, providerID, providerModelID, operation, status, truth string, version int, evidenceRef string) {
	t.Helper()
	if _, err := db.Conn().Exec(
		`INSERT INTO offering_operations (id, account_id, provider_id, provider_model_id, operation, created_at, updated_at) VALUES (?, ?, ?, ?, ?, 0, 0)`,
		id, accountID, providerID, providerModelID, operation,
	); err != nil {
		t.Fatalf("seed offering_operation %s: %v", id, err)
	}
	if _, err := db.Conn().Exec(
		`INSERT INTO certifications (offering_operation_id, status, capability_truth, version, evidence_ref, created_at, updated_at) VALUES (?, ?, ?, ?, ?, 0, 0)`,
		id, status, truth, version, evidenceRef,
	); err != nil {
		t.Fatalf("seed certification for %s: %v", id, err)
	}
}
