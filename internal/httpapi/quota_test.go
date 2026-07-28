package httpapi

// quota_test.go exercises the P3b-CAPI-001 quota-refresh trigger
// (internal/httpapi/quota.go). Functional tests build a QuotaHandler
// directly over a fresh migrated DB and a test-local providers.Registry
// holding a deterministic fake QuotaAdapter (never real network) —
// mirroring newDiscoveryFixture's posture in discovery_test.go.
// Owner-gating is proved separately through the real ControlMux
// composition.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

func fixedQuotaHandlerClock() time.Time {
	return time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
}

func qf64(v float64) *float64 { return &v }

// fakeQuotaAdapter is a deterministic, in-memory QuotaAdapter — no real
// network call, no real provider.
type fakeQuotaAdapter struct {
	result providers.QuotaResult
	err    error
	calls  int
}

func (a *fakeQuotaAdapter) FetchQuota(_ context.Context, _ providers.StoredCredentials) (providers.QuotaResult, error) {
	a.calls++
	return a.result, a.err
}

func quotaIDCounter() func() string {
	n := 0
	return func() string {
		n++
		return fmt.Sprintf("quota-http-id-%d", n)
	}
}

type quotaFixtureOpts struct {
	WithQuota      bool
	WithCredential bool
}

type quotaFixture struct {
	handler    *QuotaHandler
	db         *storage.DB
	jobs       *storage.JobRepo
	windows    *storage.QuotaWindowRepo
	adapter    *fakeQuotaAdapter
	accountID  string
	providerID string
}

// newQuotaFixture seeds a provider + a connected account, optionally an
// active credential, and optionally a registered fake quota adapter for
// that provider — the combinations this handler's precondition checks
// need to exercise independently.
func newQuotaFixture(t *testing.T, clock func() time.Time, opts quotaFixtureOpts) *quotaFixture {
	t.Helper()
	db := testControlDB(t)

	const providerID = "prov-quota"
	const accountID = "acct-quota"

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

	if opts.WithCredential {
		if _, err := credSvc.Store(context.Background(), application.StoreCredentialParams{
			ID:           "cred-quota-1",
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
	adapter := &fakeQuotaAdapter{}
	def := providers.Definition{ID: providers.ProviderID(providerID), AuthMode: providers.AuthModeAPIKey, Transport: providers.TransportKindOpenAICompatible, APIKey: newFakeAPIKeyAdapter()}
	if opts.WithQuota {
		def.Quota = adapter
	}
	if err := reg.Register(def); err != nil {
		t.Fatalf("register fake adapter: %v", err)
	}

	accountRepo := storage.NewAccountRepo(db)
	jobRepo := storage.NewJobRepo(db)
	lifecycle := storage.NewQuotaLifecycleRepo(db, clock, nil)
	reconciliation := storage.NewReconciliationRepo(db, clock, quota.DefaultReconciliationPolicy(), lifecycle, nil)
	audit := newAuditEmitter(db, nil)
	idem := newIdempotencyStore()

	h := NewQuotaHandler(accountRepo, credRepo, jobRepo, reconciliation, reg, credSvc, audit, idem, quotaIDCounter(), clock)

	return &quotaFixture{
		handler:    h,
		db:         db,
		jobs:       jobRepo,
		windows:    storage.NewQuotaWindowRepo(db, nil, clock),
		adapter:    adapter,
		accountID:  accountID,
		providerID: providerID,
	}
}

func newTestQuotaMux(h *QuotaHandler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/control/v1/accounts/{id}/quota", h.ServeQuotaRefresh)
	return mux
}

func quotaRequest(method, path string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = testAllowedHost
	return req
}

func decodeQuotaRefreshResponse(t *testing.T, body []byte) quotaRefreshResponseJSON {
	t.Helper()
	var env struct {
		Data quotaRefreshResponseJSON `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode quota refresh response: %v; body = %q", err, body)
	}
	return env.Data
}

// --- POST /accounts/{id}/quota ---

func TestQuotaRefresh_Returns202WithJobAndStatusURL(t *testing.T) {
	clock := fixedQuotaHandlerClock()
	f := newQuotaFixture(t, func() time.Time { return clock }, quotaFixtureOpts{WithQuota: true, WithCredential: true})
	f.adapter.result = providers.QuotaResult{Windows: []providers.QuotaWindow{
		{Unit: "requests", WindowType: "rolling_5h", WindowKey: "5h", Used: qf64(10), Remaining: qf64(90), Total: qf64(100), Confidence: 0.9},
	}}
	mux := newTestQuotaMux(f.handler)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, quotaRequest(http.MethodPost, "/api/control/v1/accounts/"+f.accountID+"/quota"))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %q", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if strings.Contains(body, `"windows"`) || strings.Contains(body, `"remaining"`) {
		t.Fatalf("202 response contains an inline quota snapshot, want only {job_id, status_url}: %s", body)
	}

	data := decodeQuotaRefreshResponse(t, rec.Body.Bytes())
	if data.JobID == "" {
		t.Fatalf("job_id is empty")
	}
	if data.StatusURL != "/api/control/v1/jobs/"+data.JobID {
		t.Fatalf("status_url = %q, want /api/control/v1/jobs/%s", data.StatusURL, data.JobID)
	}

	row, ok, err := f.jobs.GetByID(context.Background(), data.JobID)
	if err != nil || !ok {
		t.Fatalf("GetByID(%s): ok=%v err=%v", data.JobID, ok, err)
	}
	if row.Kind != string(storage.JobKindQuotaSync) {
		t.Fatalf("Kind = %q, want quota_sync", row.Kind)
	}

	// Wait for the detached background run to finish before returning —
	// mirrors TestDiscover_Returns202WithJobAndStatusURL's rationale: this
	// test's DB is a real file under t.TempDir(), and letting the
	// goroutine outlive the test races t.Cleanup's Close/delete.
	waitForJobTerminal(t, f.jobs, data.JobID, 2*time.Second)
}

func TestQuotaRefresh_UnsupportedProviderIsRejectedBeforeAnyJob(t *testing.T) {
	unsupported := newQuotaFixture(t, nil, quotaFixtureOpts{WithQuota: false, WithCredential: true})
	mux := newTestQuotaMux(unsupported.handler)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, quotaRequest(http.MethodPost, "/api/control/v1/accounts/"+unsupported.accountID+"/quota"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %q", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "quota_unsupported" {
		t.Fatalf("error code = %q, want quota_unsupported", code)
	}
	if n := countRowsQuery(t, unsupported.db, `SELECT COUNT(*) FROM jobs`); n != 0 {
		t.Fatalf("jobs row count = %d, want 0 (nothing created for an unsupported provider)", n)
	}

	// The SUPPORTED case, by contrast, does create exactly one job — both
	// directions of the precondition-ordering guarantee.
	clock := fixedQuotaHandlerClock()
	supported := newQuotaFixture(t, func() time.Time { return clock }, quotaFixtureOpts{WithQuota: true, WithCredential: true})
	mux2 := newTestQuotaMux(supported.handler)
	rec2 := httptest.NewRecorder()
	mux2.ServeHTTP(rec2, quotaRequest(http.MethodPost, "/api/control/v1/accounts/"+supported.accountID+"/quota"))
	if rec2.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 for a supported provider; body = %q", rec2.Code, rec2.Body.String())
	}
	data := decodeQuotaRefreshResponse(t, rec2.Body.Bytes())
	waitForJobTerminal(t, supported.jobs, data.JobID, 2*time.Second)
	if n := countRowsQuery(t, supported.db, `SELECT COUNT(*) FROM jobs`); n != 1 {
		t.Fatalf("jobs row count = %d, want 1 for a supported provider", n)
	}
}

// TestQuotaRefresh_RequiresOwnerSessionAndCSRF is this route's mandatory
// through-the-real-mux assertion (constraint #11): ControlMux's own
// shared provider registry has no live QuotaAdapter (opencode-zen/
// antigravity register neither), so the functional refresh-success path
// above is exercised against a local test-only registry, exactly like
// discovery_test.go's split between newDiscoveryFixture (behavior) and
// TestDiscover_IsOwnerGated (gating through ControlMux). This test proves
// the real composed mux enforces owner-session + CSRF before the handler
// ever runs, and that a CSRF rejection creates no job row.
func TestQuotaRefresh_RequiresOwnerSessionAndCSRF(t *testing.T) {
	db := testControlDB(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))

	req := newAuthRequest(t, http.MethodPost, "/api/control/v1/accounts/does-not-exist/quota", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", rec.Code)
	}

	cookie, _ := setupOwnerWithCSRF(t, mux)
	req2 := newAuthRequest(t, http.MethodPost, "/api/control/v1/accounts/does-not-exist/quota", nil)
	req2.AddCookie(cookie) // no X-CSRF-Token
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("no-CSRF status = %d, want 403", rec2.Code)
	}
	if code := decodeErrorCode(t, rec2.Body.Bytes()); code != "csrf_failed" {
		t.Fatalf("error code = %q, want csrf_failed", code)
	}
	if n := countRowsQuery(t, db, `SELECT COUNT(*) FROM jobs`); n != 0 {
		t.Fatalf("jobs row count = %d, want 0 (CSRF rejection happens before any side effect)", n)
	}
}

func TestQuotaRefresh_JobMessageIsAFixedSecretFreeConstant(t *testing.T) {
	clock := fixedQuotaHandlerClock()
	f := newQuotaFixture(t, func() time.Time { return clock }, quotaFixtureOpts{WithQuota: true, WithCredential: true})
	const canary = "sk-live-CANARY-provider-secret-should-never-leak"
	f.adapter.err = fmt.Errorf("upstream 500: leaked credential %s", canary)
	mux := newTestQuotaMux(f.handler)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, quotaRequest(http.MethodPost, "/api/control/v1/accounts/"+f.accountID+"/quota"))
	data := decodeQuotaRefreshResponse(t, rec.Body.Bytes())

	row := waitForJobTerminal(t, f.jobs, data.JobID, 2*time.Second)
	if row.Status != storage.JobFailed {
		t.Fatalf("Status = %q, want failed", row.Status)
	}
	if row.Error == nil || row.Error.Code == "" {
		t.Fatalf("Error = %+v, want a typed non-empty code", row.Error)
	}
	if row.Error.Code != "quota_fetch_failed" {
		t.Fatalf("Error.Code = %q, want the typed quota_fetch_failed", row.Error.Code)
	}
	if row.Error.Message != "quota refresh failed" {
		t.Fatalf("Error.Message = %q, want the fixed secret-free constant", row.Error.Message)
	}
	if strings.Contains(row.Error.Code, canary) || strings.Contains(row.Error.Message, canary) {
		t.Fatalf("job error leaked the canary secret: %+v", row.Error)
	}
	if strings.Contains(rec.Body.String(), canary) {
		t.Fatalf("HTTP response leaked the canary secret: %s", rec.Body.String())
	}

	var rawErrText string
	if err := f.db.Conn().QueryRow(`SELECT error FROM jobs WHERE id = ?`, data.JobID).Scan(&rawErrText); err != nil {
		t.Fatalf("read raw jobs.error column: %v", err)
	}
	if strings.Contains(rawErrText, canary) {
		t.Fatalf("jobs.error column leaked the canary secret: %s", rawErrText)
	}

	// The WHOLE row, not just the error column. result_ref is the other
	// free-form text column on jobs, and it is written on both the failure
	// and success paths — checking only `error` leaves a leak path that no
	// test can see: moving the provider's raw text into result_ref keeps
	// every other assertion here green.
	var rawRow string
	if err := f.db.Conn().QueryRow(
		`SELECT COALESCE(kind,'') || '|' || COALESCE(result_ref,'') || '|' || COALESCE(error,'')
		   FROM jobs WHERE id = ?`, data.JobID,
	).Scan(&rawRow); err != nil {
		t.Fatalf("read whole jobs row: %v", err)
	}
	if strings.Contains(rawRow, canary) {
		t.Fatalf("the jobs row leaked the canary secret somewhere outside error: %s", rawRow)
	}

	n := countRowsQuery(t, f.db, `SELECT COUNT(*) FROM audit_events WHERE reason_code LIKE '%' || ? || '%' OR entity_id LIKE '%' || ? || '%'`, canary, canary)
	if n != 0 {
		t.Fatalf("audit_events leaked the canary secret")
	}
}

func TestQuotaRefresh_ConvergesOnProviderTruth(t *testing.T) {
	clock := fixedQuotaHandlerClock()
	f := newQuotaFixture(t, func() time.Time { return clock }, quotaFixtureOpts{WithQuota: true, WithCredential: true})
	mux := newTestQuotaMux(f.handler)

	f.adapter.result = providers.QuotaResult{Windows: []providers.QuotaWindow{
		{Unit: "requests", WindowType: "rolling_5h", WindowKey: "5h", Remaining: qf64(90), Total: qf64(100), Confidence: 0.9},
	}}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, quotaRequest(http.MethodPost, "/api/control/v1/accounts/"+f.accountID+"/quota"))
	data := decodeQuotaRefreshResponse(t, rec.Body.Bytes())
	waitForJobTerminal(t, f.jobs, data.JobID, 2*time.Second)

	f.adapter.result = providers.QuotaResult{Windows: []providers.QuotaWindow{
		{Unit: "requests", WindowType: "rolling_5h", WindowKey: "5h", Remaining: qf64(42), Total: qf64(100), Confidence: 0.95},
	}}
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, quotaRequest(http.MethodPost, "/api/control/v1/accounts/"+f.accountID+"/quota"))
	data2 := decodeQuotaRefreshResponse(t, rec2.Body.Bytes())
	waitForJobTerminal(t, f.jobs, data2.JobID, 2*time.Second)

	windows, err := f.windows.ListByAccount(context.Background(), f.accountID)
	if err != nil {
		t.Fatalf("ListByAccount: %v", err)
	}
	var found bool
	for _, w := range windows {
		if w.Source == quota.SourceProviderEvidence && w.Unit == quota.UnitRequests {
			found = true
			if w.Remaining == nil || *w.Remaining != 42 {
				t.Fatalf("Remaining = %v, want 42 (the SECOND refresh's result, proving convergence)", w.Remaining)
			}
		}
	}
	if !found {
		t.Fatalf("no provider_evidence requests window found after two refreshes")
	}
}
