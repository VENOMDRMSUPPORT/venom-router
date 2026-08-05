package httpapi

// probe_test.go exercises the P3c-CAPI-001 probe-trigger surface
// (internal/httpapi/probe.go). Gating is proved through the REAL
// composed ControlMux (TestProbe_OwnerGatedThroughTheRealMux) — the P3b
// lesson that a local mux never actually proves a mutating route's
// gating. Every other test builds a ProbeHandler directly over a fresh
// migrated DB with a fake, controllable probeTransport (ControlMux's own
// production transport is an honest stub that always refuses, so a
// successful 202->completed run can only be exercised this way — mirrors
// discovery_acceptance_test.go's Layer A/Layer B split).

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// fakeProbeTransport is a controllable, in-memory probeTransport (never
// real network) — result/err/available are all settable per test.
type fakeProbeTransport struct {
	mu        sync.Mutex
	available bool
	result    intelligence.ProbeResult
	err       error
	calls     int
	// onProbe, when set, runs INSIDE Probe — i.e. at the one instant a
	// probe is genuinely in flight — so a test can interrogate live state
	// that only holds for the duration of the transport call.
	onProbe func()
}

func (f *fakeProbeTransport) Available(_ string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.available
}

func (f *fakeProbeTransport) Probe(_ context.Context, _ intelligence.ProbeRequest) (intelligence.ProbeResult, error) {
	f.mu.Lock()
	hook := f.onProbe
	f.calls++
	result, err := f.result, f.err
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	return result, err
}

func (f *fakeProbeTransport) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// generousLocalSafetySpecs returns local-safety window specs with very
// high limits, so a probe fixture's account never spuriously hits the
// concurrency/consumption caps this test file is not exercising.
func generousLocalSafetySpecs(t *testing.T) []quota.WindowSpec {
	t.Helper()
	specs, err := quota.LocalSafetyPolicy{
		MaxConcurrency:             1000,
		EstimatedConsumptionUnit:   quota.UnitRequests,
		EstimatedConsumptionLimit:  1_000_000,
		EstimatedConsumptionWindow: time.Hour,
	}.MandatoryWindows()
	if err != nil {
		t.Fatalf("MandatoryWindows: %v", err)
	}
	return specs
}

// probeIDCounter returns a deterministic id generator ("probe-id-1",
// "probe-id-2", ...) mirroring discoveryIDCounter's shape.
func probeIDCounter() func() string {
	n := 0
	return func() string {
		n++
		return "probe-id-" + strconv.Itoa(n)
	}
}

type probeFixtureOpts struct {
	Operation          string
	CertState          string
	CertTruth          string
	TransportAvailable bool
	Policy             *intelligence.ProbeSafetyPolicy
	NoCredential       bool
}

type probeFixture struct {
	db         *storage.DB
	handler    *ProbeHandler
	jobs       *storage.JobRepo
	certs      *storage.CertificationRepo
	probeRuns  *storage.ProbeRunRepo
	discovery  *storage.DiscoveryRepo
	transport  *fakeProbeTransport
	audit      *auditEmitter
	accountID  string
	providerID string
	opID       string
	modelID    string
	clockNow   time.Time
}

// newProbeFixture builds a full real-DB fixture: provider + connected
// account (+ active credential unless NoCredential) + one offering-
// operation + its certification row + generous local-safety quota
// windows + every repo/adapter ProbeHandler needs, wired over a fake,
// controllable transport.
// staticProbePolicy adapts a fixed ProbeSafetyPolicy to the per-request
// provider NewProbeHandler now takes (P6-CAPI-001 made the caps
// owner-configurable). Tests that do not exercise owner configuration keep
// asserting against exactly the policy they built.
func staticProbePolicy(p intelligence.ProbeSafetyPolicy) func(context.Context) intelligence.ProbeSafetyPolicy {
	return func(context.Context) intelligence.ProbeSafetyPolicy { return p }
}

func newProbeFixture(t *testing.T, opts probeFixtureOpts) *probeFixture {
	t.Helper()
	if opts.Operation == "" {
		opts.Operation = "tools"
	}
	if opts.CertState == "" {
		opts.CertState = "observed"
	}
	if opts.CertTruth == "" {
		opts.CertTruth = "unknown"
	}

	db := testControlDB(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	const accountID = "acct-probe"
	const providerID = "prov-probe"
	p3aSeedAccount(t, db, accountID, providerID)

	if !opts.NoCredential {
		credRepo := storage.NewAccountCredentialRepo(db)
		credSvc := application.NewCredentialService(credRepo, testKeyring(t), clock)
		if _, err := credSvc.Store(ctx, application.StoreCredentialParams{
			ID: "cred-probe", AccountID: accountID, ProviderID: providerID,
			Kind: domain.CredentialKindAPIKey, Active: true, PlaintextKey: "not-a-real-credential",
		}); err != nil {
			t.Fatalf("store credential: %v", err)
		}
	}

	seedModelForCert(t, db, "model-probe")
	seedOfferingForCert(t, db, accountID, providerID, "pm-probe", "model-probe")
	opID := "op-probe"
	seedOfferingOperationForCert(t, db, opID, accountID, providerID, "pm-probe", opts.Operation, opts.CertState, opts.CertTruth, 1, "")

	windowRepo := storage.NewQuotaWindowRepo(db, nil, clock)
	if err := windowRepo.EnsureLocalSafetyWindows(ctx, accountID, generousLocalSafetySpecs(t)); err != nil {
		t.Fatalf("EnsureLocalSafetyWindows: %v", err)
	}

	accountRepo := storage.NewAccountRepo(db)
	credentialRepo := storage.NewAccountCredentialRepo(db)
	catalogRepo := storage.NewCatalogRepo(db)
	jobRepo := storage.NewJobRepo(db)
	certRepo := storage.NewCertificationRepo(db, clock)
	probeRunRepo := storage.NewProbeRunRepo(db, clock, 7*24*time.Hour)
	discoveryRepo := storage.NewDiscoveryRepo(db, probeIDCounter())
	reservationRepo := storage.NewQuotaReservationRepo(db, clock)
	reserver := newProbeReserverAdapter(reservationRepo)

	audit := newAuditEmitter(db, nil)
	certAuditor := newCertificationAuditorAdapter(audit)
	driver, err := intelligence.NewCertificationDriver(certRepo, certAuditor, intelligence.DefaultProbeRetryBudget, clock)
	if err != nil {
		t.Fatalf("NewCertificationDriver: %v", err)
	}

	transport := &fakeProbeTransport{available: opts.TransportAvailable}
	policy := intelligence.DefaultProbeSafetyPolicy()
	if opts.Policy != nil {
		policy = *opts.Policy
	}

	idem := newIdempotencyStore()
	handler := NewProbeHandler(
		accountRepo, credentialRepo, catalogRepo, jobRepo, certRepo, probeRunRepo,
		reserver, transport, driver, discoveryRepo, staticProbePolicy(policy), audit, idem, probeIDCounter(), clock, nil,
	)

	return &probeFixture{
		db: db, handler: handler, jobs: jobRepo, certs: certRepo, probeRuns: probeRunRepo,
		discovery: discoveryRepo, transport: transport, audit: audit, accountID: accountID,
		providerID: providerID, opID: opID, modelID: "model-probe", clockNow: now,
	}
}

func newTestProbeMux(h *ProbeHandler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/control/v1/offerings/{id}/probe", h.ServeProbe)
	return mux
}

func probeRequest(method, path string, body []byte) *http.Request {
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, strings.NewReader(string(body)))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = testAllowedHost
	return req
}

func decodeProbeResponse(t *testing.T, body []byte) probeResponseJSON {
	t.Helper()
	var env struct {
		Data probeResponseJSON `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode probe response: %v; body = %q", err, body)
	}
	return env.Data
}

// --- TestProbe_OwnerGatedThroughTheRealMux ---

func TestProbe_OwnerGatedThroughTheRealMux(t *testing.T) {
	db := testControlDB(t)
	p3aSeedAccount(t, db, "acct-gate", "prov-gate")
	seedModelForCert(t, db, "model-gate")
	seedOfferingForCert(t, db, "acct-gate", "prov-gate", "pm-gate", "model-gate")
	seedOfferingOperationForCert(t, db, "op-gate", "acct-gate", "prov-gate", "pm-gate", "tools", "observed", "unknown", 1, "")

	mux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))

	// No session at all -> 401. Nothing created.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, newAuthRequest(t, http.MethodPost, "/api/control/v1/offerings/op-gate/probe", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", rec.Code)
	}
	if n := countRowsQuery(t, db, `SELECT COUNT(*) FROM jobs`); n != 0 {
		t.Fatalf("jobs row count after unauthenticated request = %d, want 0", n)
	}

	cookie, _ := setupOwnerWithCSRF(t, mux)

	// Session present but NO CSRF token -> 403. Nothing created.
	req := newAuthRequest(t, http.MethodPost, "/api/control/v1/offerings/op-gate/probe", nil)
	req.AddCookie(cookie)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("no-CSRF status = %d, want 403", rec2.Code)
	}
	if code := decodeErrorCode(t, rec2.Body.Bytes()); code != "csrf_failed" {
		t.Fatalf("no-CSRF error code = %q, want csrf_failed", code)
	}
	if n := countRowsQuery(t, db, `SELECT COUNT(*) FROM jobs`); n != 0 {
		t.Fatalf("jobs row count after CSRF-less request = %d, want 0", n)
	}
}

// --- TestProbe_PreconditionsCreateNothing ---

func TestProbe_PreconditionsCreateNothing(t *testing.T) {
	f := newProbeFixture(t, probeFixtureOpts{TransportAvailable: false})
	mux := newTestProbeMux(f.handler)

	// 1. Unknown offering-operation id -> 404.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, probeRequest(http.MethodPost, "/api/control/v1/offerings/does-not-exist/probe", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id status = %d, want 404; body = %q", rec.Code, rec.Body.String())
	}

	// 2. A requested operation that does not match this offering-
	// operation's own operation ("tools") -> 422.
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, probeRequest(http.MethodPost, "/api/control/v1/offerings/"+f.opID+"/probe", []byte(`{"operations":["vision"]}`)))
	if rec2.Code != http.StatusUnprocessableEntity {
		t.Fatalf("mismatched operation status = %d, want 422; body = %q", rec2.Code, rec2.Body.String())
	}

	// 3. No probe transport available for this provider -> 409
	// probe_unsupported (fixture built with TransportAvailable: false).
	rec3 := httptest.NewRecorder()
	mux.ServeHTTP(rec3, probeRequest(http.MethodPost, "/api/control/v1/offerings/"+f.opID+"/probe", nil))
	if rec3.Code != http.StatusConflict {
		t.Fatalf("no-transport status = %d, want 409; body = %q", rec3.Code, rec3.Body.String())
	}
	if code := decodeErrorCode(t, rec3.Body.Bytes()); code != "probe_unsupported" {
		t.Fatalf("no-transport error code = %q, want probe_unsupported", code)
	}

	if n := countRowsQuery(t, f.db, `SELECT COUNT(*) FROM jobs`); n != 0 {
		t.Fatalf("jobs row count after every rejection = %d, want 0", n)
	}
}

// TestProbe_NoCredentialIsAPrecondition proves the credential_unavailable
// precondition also creates nothing (a fourth precondition beyond the
// three the spec enumerates by status code, but the same "nothing
// written" contract applies).
func TestProbe_NoCredentialIsAPrecondition(t *testing.T) {
	f := newProbeFixture(t, probeFixtureOpts{TransportAvailable: true, NoCredential: true})
	mux := newTestProbeMux(f.handler)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, probeRequest(http.MethodPost, "/api/control/v1/offerings/"+f.opID+"/probe", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %q", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "credential_unavailable" {
		t.Fatalf("error code = %q, want credential_unavailable", code)
	}
	if n := countRowsQuery(t, f.db, `SELECT COUNT(*) FROM jobs`); n != 0 {
		t.Fatalf("jobs row count = %d, want 0", n)
	}
}

// --- TestProbe_Returns202WithJobAndStatusURL ---

// TestProbe_InFlightSlotIsHeldAcrossTheTransportCall pins the ONE thing
// that makes 04 §2's "max 1 in-flight probe per provider" cap real: the
// probe_runs row must exist, unfinished, WHILE the transport call is in
// progress. A row written only after the probe returns would leave
// InFlightProbes reading zero for the entire duration of every probe, so
// two concurrent probes to one provider would both be admitted and the
// cap would bound nothing at all. The fake transport interrogates the
// live repository from inside Probe(), which is the only moment that
// question can be asked honestly.
func TestProbe_InFlightSlotIsHeldAcrossTheTransportCall(t *testing.T) {
	f := newProbeFixture(t, probeFixtureOpts{TransportAvailable: true})
	f.transport.result = intelligence.ProbeResult{HTTPStatus: 200, Witness: intelligence.WitnessToolCall}

	var seenInFlight int
	var seenErr error
	f.transport.onProbe = func() {
		seenInFlight, seenErr = f.probeRuns.InFlightProbes(context.Background(), f.providerID)
	}

	mux := newTestProbeMux(f.handler)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, probeRequest(http.MethodPost, "/api/control/v1/offerings/"+f.opID+"/probe", nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %q", rec.Code, rec.Body.String())
	}
	data := decodeProbeResponse(t, rec.Body.Bytes())
	row := waitForJobTerminal(t, f.jobs, data.JobID, 2*time.Second)
	if row.Status != storage.JobCompleted {
		t.Fatalf("job Status = %q, want completed (error = %+v)", row.Status, row.Error)
	}

	if seenErr != nil {
		t.Fatalf("InFlightProbes from inside the transport call: %v", seenErr)
	}
	if seenInFlight != 1 {
		t.Fatalf("in-flight probes DURING the transport call = %d, want 1 — the run row must be open across the call or the per-provider cap bounds nothing", seenInFlight)
	}

	// Positive control on the other side: once the probe is done the slot
	// is released, so a later probe is never blocked by a ghost row.
	after, err := f.probeRuns.InFlightProbes(context.Background(), f.providerID)
	if err != nil {
		t.Fatalf("InFlightProbes after: %v", err)
	}
	if after != 0 {
		t.Fatalf("in-flight probes after completion = %d, want 0", after)
	}
}

// TestProbe_ConcurrentProbeOnTheSameProviderIsRefused drives the composed
// path with another provider-scoped run already open: gate 5 must refuse
// this one with probe_concurrency, and the transport must never be called.
func TestProbe_ConcurrentProbeOnTheSameProviderIsRefused(t *testing.T) {
	f := newProbeFixture(t, probeFixtureOpts{TransportAvailable: true})
	f.transport.result = intelligence.ProbeResult{HTTPStatus: 200, Witness: intelligence.WitnessToolCall}

	// An unfinished run for the SAME provider, as a crashed or concurrent
	// process would leave behind.
	if err := f.probeRuns.Start(context.Background(), storage.ProbeRunParams{
		ID: "other-run", OfferingOperationID: f.opID, AccountID: f.accountID, ProviderID: f.providerID,
		Operation: "tools", Class: intelligence.ProbeStandard,
		Allocations: []quota.Allocation{{Unit: quota.UnitRequests, Cost: 1, Source: quota.EstimateSourceFromRequest}},
		StartedAt:   f.clockNow,
	}); err != nil {
		t.Fatalf("seed in-flight run: %v", err)
	}

	mux := newTestProbeMux(f.handler)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, probeRequest(http.MethodPost, "/api/control/v1/offerings/"+f.opID+"/probe", nil))
	data := decodeProbeResponse(t, rec.Body.Bytes())
	row := waitForJobTerminal(t, f.jobs, data.JobID, 2*time.Second)

	if row.Status != storage.JobFailed {
		t.Fatalf("job Status = %q, want failed", row.Status)
	}
	if row.Error == nil || row.Error.Code != "probe_concurrency" {
		t.Fatalf("job error = %+v, want probe_concurrency", row.Error)
	}
	if f.transport.callCount() != 0 {
		t.Fatalf("transport called %d times, want 0 — a concurrency refusal must never reach the provider", f.transport.callCount())
	}
}

func TestProbe_Returns202WithJobAndStatusURL(t *testing.T) {
	f := newProbeFixture(t, probeFixtureOpts{TransportAvailable: true})
	f.transport.result = intelligence.ProbeResult{HTTPStatus: 200, Witness: intelligence.WitnessToolCall}
	mux := newTestProbeMux(f.handler)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, probeRequest(http.MethodPost, "/api/control/v1/offerings/"+f.opID+"/probe", nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %q", rec.Code, rec.Body.String())
	}
	data := decodeProbeResponse(t, rec.Body.Bytes())
	if data.JobID == "" || data.StatusURL != "/api/control/v1/jobs/"+data.JobID {
		t.Fatalf("data = %+v, want a non-empty JobID and a matching StatusURL", data)
	}

	row := waitForJobTerminal(t, f.jobs, data.JobID, 2*time.Second)
	if row.Status != storage.JobCompleted {
		t.Fatalf("job Status = %q, want completed (error = %+v)", row.Status, row.Error)
	}
	if row.Kind != "probe" {
		t.Fatalf("job Kind = %q, want probe", row.Kind)
	}
}

// --- TestProbe_ContextWindowWriteBack (Task 4) ---

// queryNativeContextTokens reads models.native_context_tokens for modelID,
// returning nil for SQL NULL.
func queryNativeContextTokens(t *testing.T, db *storage.DB, modelID string) *int {
	t.Helper()
	var v sql.NullInt64
	if err := db.Conn().QueryRow(`SELECT native_context_tokens FROM models WHERE id = ?`, modelID).Scan(&v); err != nil {
		t.Fatalf("query native_context_tokens for %q: %v", modelID, err)
	}
	if !v.Valid {
		return nil
	}
	n := int(v.Int64)
	return &n
}

// TestProbe_ContextWindowWriteBack_ExtractedLimitPersisted drives the
// PRODUCTION probe job path (POST /offerings/{id}/probe -> runProbe ->
// ContextProbe.Run -> RecordAttempt) end to end with a fake transport
// whose rejection carries a structured, extractable context limit. Task 4's
// whole point: that extracted number must now be durably written onto the
// offering's canonical model row, not thrown away.
// MUTATION: deleting the write-back call in runProbe (Step 5's proof)
// leaves this test's final assertion RED while every other assertion in
// this file stays green.
func TestProbe_ContextWindowWriteBack_ExtractedLimitPersisted(t *testing.T) {
	policy := intelligence.DefaultProbeSafetyPolicy()
	policy.ExpensiveProbesEnabled = true
	f := newProbeFixture(t, probeFixtureOpts{Operation: "context_window", CertState: "probing", TransportAvailable: true, Policy: &policy})
	mux := newTestProbeMux(f.handler)

	if got := queryNativeContextTokens(t, f.db, f.modelID); got != nil {
		t.Fatalf("native_context_tokens before the probe = %v, want NULL", *got)
	}

	limit := 128000
	f.transport.result = intelligence.ProbeResult{HTTPStatus: 400, StructuredContextLimit: &limit}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, probeRequest(http.MethodPost, "/api/control/v1/offerings/"+f.opID+"/probe", nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %q", rec.Code, rec.Body.String())
	}
	data := decodeProbeResponse(t, rec.Body.Bytes())
	row := waitForJobTerminal(t, f.jobs, data.JobID, 2*time.Second)
	if row.Status != storage.JobCompleted {
		t.Fatalf("job Status = %q, want completed (error = %+v)", row.Status, row.Error)
	}

	got := queryNativeContextTokens(t, f.db, f.modelID)
	if got == nil || *got != limit {
		t.Fatalf("native_context_tokens after the probe = %v, want %d", got, limit)
	}
}

// TestProbe_ContextWindowWriteBack_NoSignalNotWritten is the negative
// control: a rejection the extraction ladder cannot read anything out of
// (RungNoSignal) must leave native_context_tokens untouched — the ladder
// itself already guarantees it never hands back a positive value here, but
// this proves the handler honors that rather than writing a zero/garbage
// value or writing on every completed job regardless of extraction.
func TestProbe_ContextWindowWriteBack_NoSignalNotWritten(t *testing.T) {
	policy := intelligence.DefaultProbeSafetyPolicy()
	policy.ExpensiveProbesEnabled = true
	f := newProbeFixture(t, probeFixtureOpts{Operation: "context_window", CertState: "probing", TransportAvailable: true, Policy: &policy})
	mux := newTestProbeMux(f.handler)

	// No structured field, no context_length_exceeded provider code, and a
	// message containing none of the rung-4 generic keywords ("context
	// length", "context window", "maximum context", "token limit",
	// "context") — every rung misses, so classifyContextProbeResult reports
	// RungNoSignal.
	f.transport.result = intelligence.ProbeResult{HTTPStatus: 400, ProviderCode: "bad_request", Message: "the request body could not be parsed"}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, probeRequest(http.MethodPost, "/api/control/v1/offerings/"+f.opID+"/probe", nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %q", rec.Code, rec.Body.String())
	}
	data := decodeProbeResponse(t, rec.Body.Bytes())
	row := waitForJobTerminal(t, f.jobs, data.JobID, 2*time.Second)
	if row.Status != storage.JobCompleted {
		t.Fatalf("job Status = %q, want completed (error = %+v)", row.Status, row.Error)
	}

	if got := queryNativeContextTokens(t, f.db, f.modelID); got != nil {
		t.Fatalf("native_context_tokens after a no-signal rejection = %v, want NULL — nothing was extracted, so nothing must be written", *got)
	}
}

// --- TestProbe_ForceBypassesOnlyTheCooldown ---

func TestProbe_ForceBypassesOnlyTheCooldown(t *testing.T) {
	policy := intelligence.DefaultProbeSafetyPolicy()
	policy.ExpensiveProbesEnabled = true // context_window is ProbeExpensive; opt in so the cooldown gate is what's under test
	f := newProbeFixture(t, probeFixtureOpts{Operation: "context_window", CertState: "probing", TransportAvailable: true, Policy: &policy})
	mux := newTestProbeMux(f.handler)

	// Seed a PRIOR succeeded context-window probe run directly (not
	// through the handler) so a cooldown is already in effect, without
	// driving the certification to `certified` (which would make this
	// offering-operation no longer eligible for probing at all).
	if err := f.probeRuns.Start(context.Background(), storage.ProbeRunParams{
		ID: "run-prior", OfferingOperationID: f.opID, AccountID: f.accountID, ProviderID: f.providerID,
		Operation: "context_window", Class: intelligence.ProbeExpensive, StartedAt: f.clockNow.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("seed prior run: %v", err)
	}
	if err := f.probeRuns.Finish(context.Background(), "run-prior", intelligence.ProbeSucceeded, f.clockNow.Add(-time.Hour)); err != nil {
		t.Fatalf("finish prior run: %v", err)
	}

	limit := 100000
	f.transport.result = intelligence.ProbeResult{HTTPStatus: 400, StructuredContextLimit: &limit}

	// Without force: refused probe_cooling_down.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, probeRequest(http.MethodPost, "/api/control/v1/offerings/"+f.opID+"/probe", nil))
	data := decodeProbeResponse(t, rec.Body.Bytes())
	row := waitForJobTerminal(t, f.jobs, data.JobID, 2*time.Second)
	if row.Status != storage.JobFailed || row.Error == nil || row.Error.Code != "probe_cooling_down" {
		t.Fatalf("without force: status=%v error=%+v, want failed/probe_cooling_down", row.Status, row.Error)
	}

	// With force:true: the cooldown is bypassed, and the probe completes.
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, probeRequest(http.MethodPost, "/api/control/v1/offerings/"+f.opID+"/probe", []byte(`{"force":true}`)))
	data2 := decodeProbeResponse(t, rec2.Body.Bytes())
	row2 := waitForJobTerminal(t, f.jobs, data2.JobID, 2*time.Second)
	if row2.Status != storage.JobCompleted {
		t.Fatalf("with force: job = %+v, want completed (cooldown bypassed)", row2)
	}
}

// TestProbe_ForceDoesNotBypassCostCap proves force:true bypasses ONLY
// the cooldown — a cap-exceeding probe is still refused probe_capped.
func TestProbe_ForceDoesNotBypassCostCap(t *testing.T) {
	tinyPolicy := intelligence.DefaultProbeSafetyPolicy()
	tinyPolicy.ExpensiveProbesEnabled = true // opt in so the COST CAP gate is what's under test, not opt-in
	tinyPolicy.PerProbe = []intelligence.ProbeCostCap{
		{Unit: quota.UnitRequests, Max: 1},
		{Unit: quota.UnitConcurrency, Max: 1},
		// Far below intelligence.ContextProbeInputTokens (3,000,000): every
		// context-window probe attempt must be refused on this cap alone.
		{Unit: quota.UnitInputTokens, Max: 10},
		{Unit: quota.UnitOutputTokens, Max: 1024},
	}
	f := newProbeFixture(t, probeFixtureOpts{Operation: "context_window", CertState: "probing", TransportAvailable: true, Policy: &tinyPolicy})
	mux := newTestProbeMux(f.handler)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, probeRequest(http.MethodPost, "/api/control/v1/offerings/"+f.opID+"/probe", []byte(`{"force":true}`)))
	data := decodeProbeResponse(t, rec.Body.Bytes())
	row := waitForJobTerminal(t, f.jobs, data.JobID, 2*time.Second)
	if row.Status != storage.JobFailed || row.Error == nil || row.Error.Code != "probe_capped" {
		t.Fatalf("status=%v error=%+v, want failed/probe_capped (force must never bypass a cost cap)", row.Status, row.Error)
	}
	if f.transport.callCount() != 0 {
		t.Fatalf("transport was called %d times, want 0 (a capped probe must never reach the transport)", f.transport.callCount())
	}
}

// --- TestProbe_RefusalIsATypedTerminalJobError ---

func TestProbe_RefusalIsATypedTerminalJobError(t *testing.T) {
	t.Run("probe_concurrency", func(t *testing.T) {
		policy := intelligence.DefaultProbeSafetyPolicy()
		policy.MaxInFlightPerProvider = 1
		f := newProbeFixture(t, probeFixtureOpts{TransportAvailable: true, Policy: &policy})
		// Seed an already in-flight (running, unfinished) probe run for the
		// same provider so the concurrency gate refuses.
		if err := f.probeRuns.Start(context.Background(), storage.ProbeRunParams{
			ID: "run-inflight", OfferingOperationID: f.opID, AccountID: f.accountID, ProviderID: f.providerID,
			Operation: "tools", Class: intelligence.ProbeStandard, StartedAt: f.clockNow,
		}); err != nil {
			t.Fatalf("seed in-flight run: %v", err)
		}

		mux := newTestProbeMux(f.handler)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, probeRequest(http.MethodPost, "/api/control/v1/offerings/"+f.opID+"/probe", nil))
		data := decodeProbeResponse(t, rec.Body.Bytes())
		row := waitForJobTerminal(t, f.jobs, data.JobID, 2*time.Second)
		if row.Status != storage.JobFailed || row.Error == nil || row.Error.Code != "probe_concurrency" {
			t.Fatalf("job = %+v, want failed/probe_concurrency", row)
		}
	})

	t.Run("probe_opt_in_required", func(t *testing.T) {
		policy := intelligence.DefaultProbeSafetyPolicy()
		policy.ExpensiveProbesEnabled = false
		f := newProbeFixture(t, probeFixtureOpts{Operation: "context_window", CertState: "probing", TransportAvailable: true, Policy: &policy})
		mux := newTestProbeMux(f.handler)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, probeRequest(http.MethodPost, "/api/control/v1/offerings/"+f.opID+"/probe", nil))
		data := decodeProbeResponse(t, rec.Body.Bytes())
		row := waitForJobTerminal(t, f.jobs, data.JobID, 2*time.Second)
		if row.Status != storage.JobFailed || row.Error == nil || row.Error.Code != "probe_opt_in_required" {
			t.Fatalf("job = %+v, want failed/probe_opt_in_required", row)
		}
	})

	t.Run("probe_capped", func(t *testing.T) {
		policy := intelligence.DefaultProbeSafetyPolicy()
		policy.PerProbe = []intelligence.ProbeCostCap{
			{Unit: quota.UnitRequests, Max: 1}, {Unit: quota.UnitConcurrency, Max: 1},
			{Unit: quota.UnitInputTokens, Max: 1}, {Unit: quota.UnitOutputTokens, Max: 1},
		}
		f := newProbeFixture(t, probeFixtureOpts{TransportAvailable: true, Policy: &policy})
		mux := newTestProbeMux(f.handler)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, probeRequest(http.MethodPost, "/api/control/v1/offerings/"+f.opID+"/probe", nil))
		data := decodeProbeResponse(t, rec.Body.Bytes())
		row := waitForJobTerminal(t, f.jobs, data.JobID, 2*time.Second)
		if row.Status != storage.JobFailed || row.Error == nil || row.Error.Code != "probe_capped" {
			t.Fatalf("job = %+v, want failed/probe_capped", row)
		}
	})

	t.Run("probe_unsupported", func(t *testing.T) {
		f := newProbeFixture(t, probeFixtureOpts{TransportAvailable: false})
		mux := newTestProbeMux(f.handler)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, probeRequest(http.MethodPost, "/api/control/v1/offerings/"+f.opID+"/probe", nil))
		// probe_unsupported is a SYNCHRONOUS precondition (409), never a
		// job at all — confirmed here alongside the async codes above so
		// this table covers every documented code in one place.
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409", rec.Code)
		}
		if code := decodeErrorCode(t, rec.Body.Bytes()); code != "probe_unsupported" {
			t.Fatalf("error code = %q, want probe_unsupported", code)
		}
	})
}

// --- TestProbe_ResultRefIsAReferenceOnly ---

func TestProbe_ResultRefIsAReferenceOnly(t *testing.T) {
	f := newProbeFixture(t, probeFixtureOpts{TransportAvailable: true})
	f.transport.result = intelligence.ProbeResult{HTTPStatus: 200, Witness: intelligence.WitnessToolCall, Message: "should never leak"}
	mux := newTestProbeMux(f.handler)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, probeRequest(http.MethodPost, "/api/control/v1/offerings/"+f.opID+"/probe", nil))
	data := decodeProbeResponse(t, rec.Body.Bytes())
	row := waitForJobTerminal(t, f.jobs, data.JobID, 2*time.Second)
	if row.Status != storage.JobCompleted {
		t.Fatalf("job = %+v, want completed", row)
	}
	wantRef := "/api/control/v1/offerings/" + f.opID + "/certification"
	if row.ResultRef != wantRef {
		t.Fatalf("ResultRef = %q, want %q", row.ResultRef, wantRef)
	}
	for _, forbidden := range []string{"should never leak", "snippet", "evidence"} {
		if strings.Contains(row.ResultRef, forbidden) {
			t.Fatalf("ResultRef contains forbidden content %q: %s", forbidden, row.ResultRef)
		}
	}
}

// --- TestProbe_NoSecretReachesAnySink (the CANARY) ---

func TestProbe_NoSecretReachesAnySink(t *testing.T) {
	const canary = "sk-live-CANARY-should-never-appear"
	// plainMarker is deliberately NOT credential-shaped (no sk-/ya29./
	// vk_live_/ghp_ prefix, no key=value pair authHeaderRe/paramValueRe
	// would match) — sanitize.Text/redactProbeSnippet's own redaction
	// therefore does NOT strip it, so its presence in any sink can ONLY
	// be explained by this package's own code path propagating raw
	// Message content downstream (as opposed to the credential-shaped
	// canary above, which upstream redaction would mask regardless of
	// what this package does with it — a mutation that leaked the raw
	// message here would otherwise go undetected, vacuously).
	const plainMarker = "zzz-probe-message-marker-9f3c7a21e8-should-never-appear"
	f := newProbeFixture(t, probeFixtureOpts{TransportAvailable: true})
	f.transport.result = intelligence.ProbeResult{HTTPStatus: 400, ProviderCode: "unsupported_capability", Message: "rejected " + plainMarker + " token=" + canary}
	mux := newTestProbeMux(f.handler)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, probeRequest(http.MethodPost, "/api/control/v1/offerings/"+f.opID+"/probe", nil))
	if strings.Contains(rec.Body.String(), canary) || strings.Contains(rec.Body.String(), plainMarker) {
		t.Fatalf("202 response leaked the canary secret: %s", rec.Body.String())
	}
	data := decodeProbeResponse(t, rec.Body.Bytes())
	row := waitForJobTerminal(t, f.jobs, data.JobID, 2*time.Second)

	combined := row.Kind + "|" + row.ResultRef
	if row.Error != nil {
		combined += "|" + row.Error.Code + "|" + row.Error.Message
	}
	if strings.Contains(combined, canary) {
		t.Fatalf("job row (kind|result_ref|error) leaked the canary secret: %s", combined)
	}
	if strings.Contains(combined, plainMarker) {
		t.Fatalf("job row (kind|result_ref|error) leaked the raw provider message: %s", combined)
	}

	var auditCount int
	if err := f.db.Conn().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE reason_code LIKE '%' || ? || '%' OR entity_id LIKE '%' || ? || '%'`, canary, canary).Scan(&auditCount); err != nil {
		t.Fatalf("scan audit_events for canary: %v", err)
	}
	if auditCount != 0 {
		t.Fatalf("%d audit_events rows contain the canary secret, want 0", auditCount)
	}

	var probeRunCount int
	if err := f.db.Conn().QueryRow(`SELECT COUNT(*) FROM probe_runs WHERE operation LIKE '%' || ? || '%' OR reservation_id LIKE '%' || ? || '%'`, canary, canary).Scan(&probeRunCount); err != nil {
		t.Fatalf("scan probe_runs for canary: %v", err)
	}
	if probeRunCount != 0 {
		t.Fatalf("%d probe_runs rows contain the canary secret, want 0", probeRunCount)
	}
}
