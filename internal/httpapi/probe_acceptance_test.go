package httpapi

// probe_acceptance_test.go is P3c-TEST-001: the P3c phase gate, mechanized
// — the mirror of discovery_acceptance_test.go (P3a) and
// quota_acceptance_test.go (P3b). Every unit it certifies (the probe
// engine, the certification domain, the M6 migration, POST
// /offerings/{id}/probe, the workers, P3c-EXEC-001's real
// OpenAICompatibleTransport, P3c-CERT-008's edge 1) already has its own
// unit tests; this suite does not re-test any of their internals. It
// proves the phase header's own Acceptance-gate criteria end to end,
// through the REAL composed probe path, with a REAL execution.
// OpenAICompatibleTransport pointed at an httptest.Server — never a fake
// transport — so a regression in how the pieces are WIRED TOGETHER is
// caught here even if every narrower test stays green.
//
// TEST-ONLY: this file adds zero production files and changes zero
// production behavior. Every test drives production code exactly as
// ControlMux composes it (probeTransportAdapter + execution.
// OpenAICompatibleTransport + intelligence.CertificationDriver +
// intelligence.ProbeGuard), just against an httptest.Server instead of a
// live provider, and each fixture is its own ProbeHandler/mux — never the
// full ControlMux — since ControlMux's own registry only wires
// opencode-zen against a REAL base URL; substituting an injection point
// there purely for tests would itself be a production change, which this
// unit is expressly forbidden to make (the identical rationale
// discovery_acceptance_test.go's own doc comment gives for its Layer A/
// Layer B split).

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/execution"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/staticgate"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// --- shared fixture: a real ProbeHandler wired to a REAL
// execution.OpenAICompatibleTransport (never intelligence.ProbeTransport's
// fake test double from probe_test.go) ---

type p3cGateFixtureOpts struct {
	Operation string
	CertState string
	CertTruth string
	BaseURL   string
	Timeout   time.Duration
	Policy    *intelligence.ProbeSafetyPolicy
}

type p3cGateFixture struct {
	db         *storage.DB
	handler    *ProbeHandler
	jobs       *storage.JobRepo
	certs      *storage.CertificationRepo
	probeRuns  *storage.ProbeRunRepo
	accountID  string
	providerID string
	opID       string
}

// p3cGateRepoRoot mirrors internal/staticgate/layering_test.go's own
// repoRootFromThisFile: this file lives at
// <repoRoot>/internal/httpapi/probe_acceptance_test.go.
func p3cGateRepoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

func newP3cGateFixture(t *testing.T, opts p3cGateFixtureOpts) *p3cGateFixture {
	t.Helper()
	return newP3cGateFixtureWithDB(t, testControlDB(t), opts)
}

// newP3cGateFixtureWithDB builds the fixture over a caller-supplied db
// (rather than a fresh one) so a test can seed OTHER rows on the same
// database — TestP3cGate_FreeSafetyIndependentOfEnrichmentWithProbingActive
// needs a real probe run to coexist with a catalog it reads through the
// real ControlMux.
func newP3cGateFixtureWithDB(t *testing.T, db *storage.DB, opts p3cGateFixtureOpts) *p3cGateFixture {
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
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Second
	}

	ctx := context.Background()
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	const accountID = "acct-p3c-gate"
	const providerID = "prov-p3c-gate"
	p3aSeedAccount(t, db, accountID, providerID)

	credRepo := storage.NewAccountCredentialRepo(db)
	credSvc := application.NewCredentialService(credRepo, testKeyring(t), clock)
	if _, err := credSvc.Store(ctx, application.StoreCredentialParams{
		ID: "cred-p3c-gate", AccountID: accountID, ProviderID: providerID,
		Kind: domain.CredentialKindAPIKey, Active: true, PlaintextKey: "not-a-real-credential",
	}); err != nil {
		t.Fatalf("store credential: %v", err)
	}

	seedModelForCert(t, db, "model-p3c-gate")
	seedOfferingForCert(t, db, accountID, providerID, "pm-p3c-gate", "model-p3c-gate")
	opID := "op-p3c-gate"
	seedOfferingOperationForCert(t, db, opID, accountID, providerID, "pm-p3c-gate", opts.Operation, opts.CertState, opts.CertTruth, 1, "")

	windowRepo := storage.NewQuotaWindowRepo(db, nil, clock)
	if err := windowRepo.EnsureLocalSafetyWindows(ctx, accountID, generousLocalSafetySpecs(t)); err != nil {
		t.Fatalf("EnsureLocalSafetyWindows: %v", err)
	}

	accountRepo := storage.NewAccountRepo(db)
	catalogRepo := storage.NewCatalogRepo(db)
	jobRepo := storage.NewJobRepo(db)
	certRepo := storage.NewCertificationRepo(db, clock)
	probeRunRepo := storage.NewProbeRunRepo(db, clock, 7*24*time.Hour)
	reserver := newProbeReserverAdapter(storage.NewQuotaReservationRepo(db, clock))

	audit := newAuditEmitter(db, nil)
	certAuditor := newCertificationAuditorAdapter(audit)
	driver, err := intelligence.NewCertificationDriver(certRepo, certAuditor, intelligence.DefaultProbeRetryBudget, clock)
	if err != nil {
		t.Fatalf("NewCertificationDriver: %v", err)
	}

	// THE centerpiece seam: a REAL execution.OpenAICompatibleTransport,
	// wired through the SAME production probeTransportAdapter ControlMux
	// itself builds (probeadapters.go) — pointed at opts.BaseURL (an
	// httptest.Server) instead of a live provider's URL.
	realTransport := execution.NewOpenAICompatibleTransport(&http.Client{}, opts.Timeout)
	transports := map[string]execution.InferenceTransport{providerID: realTransport}
	baseURLs := map[string]string{providerID: opts.BaseURL}
	transportAdapter := newProbeTransportAdapter(transports, baseURLs, credRepo, credSvc)

	policy := intelligence.DefaultProbeSafetyPolicy()
	policy.ExpensiveProbesEnabled = true
	if opts.Policy != nil {
		policy = *opts.Policy
	}

	idem := newIdempotencyStore()
	handler := NewProbeHandler(
		accountRepo, credRepo, catalogRepo, jobRepo, certRepo, probeRunRepo,
		reserver, transportAdapter, driver, staticProbePolicy(policy), audit, idem, probeIDCounter(), clock,
	)

	return &p3cGateFixture{
		db: db, handler: handler, jobs: jobRepo, certs: certRepo, probeRuns: probeRunRepo,
		accountID: accountID, providerID: providerID, opID: opID,
	}
}

// =====================================================================
// (1) TestP3cGate_OfferingReachesCertifiedThroughARealTransport
// =====================================================================

// toolCallResponseServer returns an httptest.Server that always answers a
// chat-completion request with a genuine tool_calls response — the ONLY
// shape RequiredWitness(tools) accepts as a capability_response (04 §2/
// §5): a chat-shaped success never certifies tools.
func toolCallResponseServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"role": "assistant", "content": "",
						"tool_calls": []map[string]any{
							{"function": map[string]any{"name": "add", "arguments": `{"a":2,"b":2}`}},
						},
					},
					"finish_reason": "stop",
				},
			},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestP3cGate_OfferingReachesCertifiedThroughARealTransport(t *testing.T) {
	srv := toolCallResponseServer(t)
	f := newP3cGateFixture(t, p3cGateFixtureOpts{Operation: "tools", BaseURL: srv.URL})
	mux := newTestProbeMux(f.handler)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, probeRequest(http.MethodPost, "/api/control/v1/offerings/"+f.opID+"/probe", nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %q", rec.Code, rec.Body.String())
	}
	data := decodeProbeResponse(t, rec.Body.Bytes())
	row := waitForJobTerminal(t, f.jobs, data.JobID, 5*time.Second)
	if row.Status != storage.JobCompleted {
		t.Fatalf("job = %+v, want completed (error = %+v)", row, row.Error)
	}

	cert, err := f.certs.Load(context.Background(), f.opID)
	if err != nil {
		t.Fatalf("Load certification: %v", err)
	}
	if cert.State != models.CertCertified {
		t.Fatalf("certification state = %q, want certified", cert.State)
	}
	if cert.Truth != models.TruthSupported {
		t.Fatalf("capability truth = %q, want supported", cert.Truth)
	}
	if !models.Routable(cert.State, cert.Truth) {
		t.Fatalf("models.Routable(state, truth) = false, want true (certified_and_supported)")
	}
}

// =====================================================================
// (2) TestP3cGate_InfraFailureNeverFlipsCapability
// =====================================================================

func TestP3cGate_InfraFailureNeverFlipsCapability(t *testing.T) {
	// Positive control FIRST: a genuine semantic rejection DOES yield
	// unsupported — so the negative subtests below cannot pass "for the
	// wrong reason" (e.g. a classifier that vacuously leaves truth
	// unknown for every response it cannot parse, infra or not).
	t.Run("positive_control_semantic_rejection_yields_unsupported", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{"code": "unsupported_capability", "message": "this model has no tool support"},
			})
		}))
		t.Cleanup(srv.Close)
		f := newP3cGateFixture(t, p3cGateFixtureOpts{Operation: "tools", BaseURL: srv.URL})
		mux := newTestProbeMux(f.handler)

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, probeRequest(http.MethodPost, "/api/control/v1/offerings/"+f.opID+"/probe", nil))
		data := decodeProbeResponse(t, rec.Body.Bytes())
		row := waitForJobTerminal(t, f.jobs, data.JobID, 5*time.Second)
		if row.Status != storage.JobCompleted {
			t.Fatalf("POSITIVE CONTROL FAILED: job = %+v, want completed", row)
		}
		cert, err := f.certs.Load(context.Background(), f.opID)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cert.State != models.CertCertified || cert.Truth != models.TruthUnsupported {
			t.Fatalf("POSITIVE CONTROL FAILED: cert = %+v, want certified/unsupported — the rest of this test would be vacuous", cert)
		}
	})

	infraCases := []struct {
		name          string
		status        int
		wantExecution intelligence.ProbeExecution
	}{
		{"http_429_rate_limited", http.StatusTooManyRequests, intelligence.ProbeRetryableFailure},
		{"http_500_server_error", http.StatusInternalServerError, intelligence.ProbeRetryableFailure},
		{"http_401_unauthorized", http.StatusUnauthorized, intelligence.ProbeTerminalFailure},
		{"http_403_forbidden", http.StatusForbidden, intelligence.ProbeTerminalFailure},
	}
	for _, c := range infraCases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(c.status)
			}))
			t.Cleanup(srv.Close)
			f := newP3cGateFixture(t, p3cGateFixtureOpts{Operation: "tools", BaseURL: srv.URL})
			mux := newTestProbeMux(f.handler)

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, probeRequest(http.MethodPost, "/api/control/v1/offerings/"+f.opID+"/probe", nil))
			data := decodeProbeResponse(t, rec.Body.Bytes())
			row := waitForJobTerminal(t, f.jobs, data.JobID, 5*time.Second)
			if row.Status != storage.JobCompleted {
				t.Fatalf("job = %+v, want completed (an infra failure is a handled outcome, not a job failure)", row)
			}
			assertInfraFailureLeftCapabilityUnknown(t, f, c.wantExecution)
		})
	}

	t.Run("timeout", func(t *testing.T) {
		block := make(chan struct{})
		hung := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { <-block }))
		t.Cleanup(hung.Close)
		t.Cleanup(func() { close(block) })
		f := newP3cGateFixture(t, p3cGateFixtureOpts{Operation: "tools", BaseURL: hung.URL, Timeout: 150 * time.Millisecond})
		mux := newTestProbeMux(f.handler)

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, probeRequest(http.MethodPost, "/api/control/v1/offerings/"+f.opID+"/probe", nil))
		data := decodeProbeResponse(t, rec.Body.Bytes())
		row := waitForJobTerminal(t, f.jobs, data.JobID, 5*time.Second)
		if row.Status != storage.JobCompleted {
			t.Fatalf("job = %+v, want completed", row)
		}
		assertInfraFailureLeftCapabilityUnknown(t, f, intelligence.ProbeRetryableFailure)
	})
}

// assertInfraFailureLeftCapabilityUnknown is TestP3cGate_InfraFailureNeverFlipsCapability's
// shared assertion: the certification never reached certified, capability
// truth is still unknown, and the probe-execution dimension reports the
// expected failure class.
func assertInfraFailureLeftCapabilityUnknown(t *testing.T, f *p3cGateFixture, wantExecution intelligence.ProbeExecution) {
	t.Helper()
	cert, err := f.certs.Load(context.Background(), f.opID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cert.State == models.CertCertified {
		t.Fatalf("cert.State = certified after an infra failure, want never certified")
	}
	if cert.Truth != models.TruthUnknown {
		t.Fatalf("cert.Truth = %q after an infra failure, want unknown (a quota/rate-limit/infra failure must never flip capability truth)", cert.Truth)
	}
	// The job row is marked completed BEFORE the deferred ProbeRunRepo.Finish
	// closes the run row (probe.go: the defer frees the in-flight slot on
	// every exit path), so "job terminal ⇒ run row closed" was never a
	// promised ordering — only that the run row's execution SETTLES to the
	// failure class. Wait out that window instead of asserting the ordering.
	deadline := time.Now().Add(5 * time.Second)
	var gotExecution intelligence.ProbeExecution
	for {
		var ok bool
		var err error
		gotExecution, ok, err = f.probeRuns.LatestExecution(context.Background(), f.opID)
		if err != nil || !ok {
			t.Fatalf("LatestExecution: ok=%v err=%v", ok, err)
		}
		if gotExecution != intelligence.ProbeRunning || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if gotExecution != wantExecution {
		t.Fatalf("probe execution = %q, want %q", gotExecution, wantExecution)
	}
}

// =====================================================================
// (3) TestP3cGate_CertifiedOnlyViaAVerdict
// =====================================================================

func TestP3cGate_CertifiedOnlyViaAVerdict(t *testing.T) {
	t.Run("discovery_alone_leaves_observed", func(t *testing.T) {
		db := testControlDB(t)
		clock := func() time.Time { return time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC) }
		p3aSeedAccount(t, db, "acct-verdict-disc", "prov-verdict-disc")
		seedModelForCert(t, db, "model-verdict-disc")
		seedOfferingForCert(t, db, "acct-verdict-disc", "prov-verdict-disc", "pm-verdict-disc", "model-verdict-disc")
		opID := "op-verdict-disc"
		seedOfferingOperationForCert(t, db, opID, "acct-verdict-disc", "prov-verdict-disc", "pm-verdict-disc", "tools", "discovered", "unknown", 1, "")

		certRepo := storage.NewCertificationRepo(db, clock)
		audit := newAuditEmitter(db, nil)
		driver, err := intelligence.NewCertificationDriver(certRepo, newCertificationAuditorAdapter(audit), intelligence.DefaultProbeRetryBudget, clock)
		if err != nil {
			t.Fatalf("NewCertificationDriver: %v", err)
		}

		cert, err := driver.Observe(context.Background(), opID)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if cert.State != models.CertObserved {
			t.Fatalf("state = %q, want observed (discovery evidence alone must never certify)", cert.State)
		}
	})

	t.Run("a_refused_probe_leaves_the_state_untouched", func(t *testing.T) {
		tinyPolicy := intelligence.DefaultProbeSafetyPolicy()
		tinyPolicy.PerProbe = []intelligence.ProbeCostCap{
			{Unit: quota.UnitRequests, Max: 1}, {Unit: quota.UnitConcurrency, Max: 1},
			{Unit: quota.UnitInputTokens, Max: 1}, {Unit: quota.UnitOutputTokens, Max: 1},
		}
		// CertState is already "probing" (not "observed") so this refusal is
		// the ONLY thing under test — no observed->probing edge muddies
		// what "untouched" means. BaseURL is an unreachable loopback port:
		// the guard must refuse before the transport is ever dialed.
		f := newP3cGateFixture(t, p3cGateFixtureOpts{Operation: "tools", CertState: "probing", Policy: &tinyPolicy, BaseURL: "http://127.0.0.1:1"})
		mux := newTestProbeMux(f.handler)

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, probeRequest(http.MethodPost, "/api/control/v1/offerings/"+f.opID+"/probe", nil))
		data := decodeProbeResponse(t, rec.Body.Bytes())
		row := waitForJobTerminal(t, f.jobs, data.JobID, 5*time.Second)
		if row.Status != storage.JobFailed || row.Error == nil || row.Error.Code != "probe_capped" {
			t.Fatalf("job = %+v, want failed/probe_capped", row)
		}

		cert, err := f.certs.Load(context.Background(), f.opID)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cert.State != models.CertProbing {
			t.Fatalf("state = %q after a refused probe, want unchanged (probing)", cert.State)
		}
	})

	t.Run("a_retryable_infra_failure_never_certifies", func(t *testing.T) {
		// The specific claim this row exists to pin: RecordAttempt must
		// reach models.CertCertified ONLY via outcome.Definitive — a
		// retryable_failure outcome (429 here) must never take that path,
		// no matter how RecordAttempt's internal plan is computed.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		t.Cleanup(srv.Close)
		f := newP3cGateFixture(t, p3cGateFixtureOpts{Operation: "tools", CertState: "probing", BaseURL: srv.URL})
		mux := newTestProbeMux(f.handler)

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, probeRequest(http.MethodPost, "/api/control/v1/offerings/"+f.opID+"/probe", nil))
		data := decodeProbeResponse(t, rec.Body.Bytes())
		row := waitForJobTerminal(t, f.jobs, data.JobID, 5*time.Second)
		// A retryable_failure is a routine, HANDLED outcome (edge 3: stay in
		// probing) — the job must complete normally. If RecordAttempt ever
		// tried to certify a non-definitive outcome, models.Certification.
		// Transition's own required-verdict guard rejects it and this job
		// fails instead — an equally certain, and easy to assert, signal
		// that "certify without a verdict" was attempted.
		if row.Status != storage.JobCompleted {
			t.Fatalf("job = %+v, want completed — a retryable failure must never even ATTEMPT to certify without a verdict", row)
		}

		cert, err := f.certs.Load(context.Background(), f.opID)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cert.State == models.CertCertified {
			t.Fatalf("state = certified after a retryable_failure (429) outcome, want never certified via a non-definitive verdict")
		}
	})

	t.Run("an_inconclusive_probe_leaves_it_in_probing", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{
					{"message": map[string]any{"role": "assistant", "content": "just a plain text reply, no tool call"}, "finish_reason": "stop"},
				},
			})
		}))
		t.Cleanup(srv.Close)
		f := newP3cGateFixture(t, p3cGateFixtureOpts{Operation: "tools", CertState: "probing", BaseURL: srv.URL})
		mux := newTestProbeMux(f.handler)

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, probeRequest(http.MethodPost, "/api/control/v1/offerings/"+f.opID+"/probe", nil))
		data := decodeProbeResponse(t, rec.Body.Bytes())
		row := waitForJobTerminal(t, f.jobs, data.JobID, 5*time.Second)
		if row.Status != storage.JobCompleted {
			t.Fatalf("job = %+v, want completed", row)
		}

		cert, err := f.certs.Load(context.Background(), f.opID)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cert.State != models.CertProbing {
			t.Fatalf("state = %q after an inconclusive (text_only) probe, want unchanged (probing)", cert.State)
		}
		if cert.Truth != models.TruthUnknown {
			t.Fatalf("truth = %q after an inconclusive probe, want unknown", cert.Truth)
		}
	})
}

// =====================================================================
// (4) TestP3cGate_FreeSafetyIndependentOfEnrichmentWithProbingActive
// =====================================================================

// TestP3cGate_FreeSafetyIndependentOfEnrichmentWithProbingActive re-asserts
// 04 §2b with probing genuinely active: toggling enrichment on/off, WHILE a
// real probe runs to completion on the same database, must never change
// any offering's cost. Reuses TestP3aGate_FreeSafetyFailsClosed's own
// p3aOwnerMux/p3aMutate/assertP3aCostsIdentical helpers (discovery_acceptance_test.go,
// same package) rather than re-implementing the toggle/compare machinery.
func TestP3cGate_FreeSafetyIndependentOfEnrichmentWithProbingActive(t *testing.T) {
	db := testControlDB(t)
	const acct = "acct-p3c-fs"

	seed := func(providerModelID string, pricingJSON *string) {
		modelsSeedOffering(t, db, offeringSeed{
			AccountID: acct, ProviderID: "prov-p3c-fs", ProviderModelID: providerModelID, ModelID: providerModelID + "-model",
			ContextLength:    modelsIntPtr(4096),
			CapabilitiesJSON: modelsStrPtr(`["chat"]`),
			PricingJSON:      pricingJSON,
			Operations:       []offeringOpSeed{{Operation: "chat", Status: "certified", Truth: "supported"}},
		})
	}
	seed("model-free", modelsStrPtr(`{"cost":{"input":0,"output":0}}`))
	seed("model-paid", modelsStrPtr(`{"cost":{"input":5,"output":10}}`))

	mux, cookie, csrfToken := p3aOwnerMux(t, db)
	readOfferings := func() []effectiveOfferingJSON {
		rec := p3aGet(t, mux, cookie, "/api/control/v1/offerings?account_id="+acct)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /offerings status = %d, want 200; body = %q", rec.Code, rec.Body.String())
		}
		return p3aDecodeOfferings(t, rec.Body.Bytes())
	}
	before := readOfferings()

	toggle := func(enabled bool) {
		body := []byte(`{"enabled":false}`)
		if enabled {
			body = []byte(`{"enabled":true}`)
		}
		rec := p3aMutate(t, mux, cookie, csrfToken, http.MethodPut, "/api/control/v1/settings/enrichment", body)
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT /settings/enrichment(%v) status = %d, want 200; body = %q", enabled, rec.Code, rec.Body.String())
		}
	}

	// A REAL probe, on the SAME database, run to a genuine terminal state
	// WHILE enrichment is toggled on — proves the independence holds with
	// probing actually active, not merely with probing absent.
	srv := toolCallResponseServer(t)
	pf := newP3cGateFixtureWithDB(t, db, p3cGateFixtureOpts{Operation: "tools", BaseURL: srv.URL})
	probeMux := newTestProbeMux(pf.handler)

	toggle(true)
	probeRec := httptest.NewRecorder()
	probeMux.ServeHTTP(probeRec, probeRequest(http.MethodPost, "/api/control/v1/offerings/"+pf.opID+"/probe", nil))
	probeData := decodeProbeResponse(t, probeRec.Body.Bytes())
	probeRow := waitForJobTerminal(t, pf.jobs, probeData.JobID, 5*time.Second)
	if probeRow.Status != storage.JobCompleted {
		t.Fatalf("the interleaved probe = %+v, want completed — otherwise this test does not actually exercise probing", probeRow)
	}

	afterOnWithProbe := readOfferings()
	assertP3aCostsIdentical(t, before, afterOnWithProbe)

	toggle(false)
	afterOff := readOfferings()
	assertP3aCostsIdentical(t, before, afterOff)
}

// =====================================================================
// (5) TestP3cGate_NoHardcodedModelData
// =====================================================================

func TestP3cGate_NoHardcodedModelData(t *testing.T) {
	root := p3cGateRepoRoot(t)

	modelViolations, err := staticgate.CheckNoStaticModelList(root)
	if err != nil {
		t.Fatalf("CheckNoStaticModelList: %v", err)
	}
	if len(modelViolations) != 0 {
		t.Fatalf("%d static-model-list violation(s), want 0: %+v", len(modelViolations), modelViolations)
	}

	rejectedViolations, err := staticgate.CheckNoRejectedState(root)
	if err != nil {
		t.Fatalf("CheckNoRejectedState: %v", err)
	}
	if len(rejectedViolations) != 0 {
		t.Fatalf("%d rejected-state violation(s), want 0: %+v", len(rejectedViolations), rejectedViolations)
	}
}

// =====================================================================
// (6) TestP3cGate_ProbeEvidenceIsRedacted (the CANARY)
// =====================================================================

// TestP3cGate_ProbeEvidenceIsRedacted plants BOTH a credential-shaped
// canary AND a plain non-credential marker in a REAL HTTP error body and
// proves neither reaches any sink. The plain marker is the load-bearing
// half (probe_test.go's TestProbe_NoSecretReachesAnySink's own doc comment
// gives the reason): a credential-shaped canary alone is redacted by
// redactProbeSnippet's own credential-shape patterns regardless of what
// this code does with it, so it proves nothing about THIS path; the plain
// marker is never touched by that redaction, so its absence from every
// sink can only be explained by the raw provider message never being
// propagated downstream in the first place.
func TestP3cGate_ProbeEvidenceIsRedacted(t *testing.T) {
	const canary = "sk-live-CANARY-p3c-gate-should-never-appear"
	const plainMarker = "zzz-p3c-gate-marker-8f2e1c9a-should-never-appear"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code":    "unsupported_capability",
				"message": "rejected " + plainMarker + " token=" + canary,
			},
		})
	}))
	t.Cleanup(srv.Close)

	f := newP3cGateFixture(t, p3cGateFixtureOpts{Operation: "tools", BaseURL: srv.URL})
	mux := newTestProbeMux(f.handler)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, probeRequest(http.MethodPost, "/api/control/v1/offerings/"+f.opID+"/probe", nil))
	if strings.Contains(rec.Body.String(), canary) || strings.Contains(rec.Body.String(), plainMarker) {
		t.Fatalf("202 response leaked the canary: %s", rec.Body.String())
	}
	data := decodeProbeResponse(t, rec.Body.Bytes())
	row := waitForJobTerminal(t, f.jobs, data.JobID, 5*time.Second)

	combined := row.Kind + "|" + row.ResultRef
	if row.Error != nil {
		combined += "|" + row.Error.Code + "|" + row.Error.Message
	}
	for _, marker := range []string{canary, plainMarker} {
		if strings.Contains(combined, marker) {
			t.Fatalf("job row (kind|result_ref|error) leaked %q: %s", marker, combined)
		}
	}

	for _, marker := range []string{canary, plainMarker} {
		var auditCount int
		if err := f.db.Conn().QueryRow(
			`SELECT COUNT(*) FROM audit_events WHERE reason_code LIKE '%' || ? || '%' OR entity_id LIKE '%' || ? || '%'`,
			marker, marker,
		).Scan(&auditCount); err != nil {
			t.Fatalf("scan audit_events for %q: %v", marker, err)
		}
		if auditCount != 0 {
			t.Fatalf("%d audit_events rows contain %q, want 0", auditCount, marker)
		}

		var probeRunCount int
		if err := f.db.Conn().QueryRow(
			`SELECT COUNT(*) FROM probe_runs WHERE operation LIKE '%' || ? || '%' OR reservation_id LIKE '%' || ? || '%'`,
			marker, marker,
		).Scan(&probeRunCount); err != nil {
			t.Fatalf("scan probe_runs for %q: %v", marker, err)
		}
		if probeRunCount != 0 {
			t.Fatalf("%d probe_runs rows contain %q, want 0", probeRunCount, marker)
		}
	}

	cert, err := f.certs.Load(context.Background(), f.opID)
	if err != nil {
		t.Fatalf("Load certification: %v", err)
	}
	if strings.Contains(cert.EvidenceRef, canary) || strings.Contains(cert.EvidenceRef, plainMarker) {
		t.Fatalf("certification evidence_ref leaked the canary: %q", cert.EvidenceRef)
	}
}

// =====================================================================
// (7) TestP3cGate_ProbeRunRecordedWithProvenance
// =====================================================================

func TestP3cGate_ProbeRunRecordedWithProvenance(t *testing.T) {
	srv := toolCallResponseServer(t)
	f := newP3cGateFixture(t, p3cGateFixtureOpts{Operation: "tools", BaseURL: srv.URL})
	mux := newTestProbeMux(f.handler)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, probeRequest(http.MethodPost, "/api/control/v1/offerings/"+f.opID+"/probe", nil))
	data := decodeProbeResponse(t, rec.Body.Bytes())
	row := waitForJobTerminal(t, f.jobs, data.JobID, 5*time.Second)
	if row.Status != storage.JobCompleted {
		t.Fatalf("job = %+v, want completed", row)
	}

	var operation, probeClass, execState string
	var reservationID sql.NullString
	if err := f.db.Conn().QueryRow(
		`SELECT operation, probe_class, execution, reservation_id FROM probe_runs WHERE offering_operation_id = ? ORDER BY started_at DESC LIMIT 1`,
		f.opID,
	).Scan(&operation, &probeClass, &execState, &reservationID); err != nil {
		t.Fatalf("query probe_runs: %v", err)
	}
	if operation != "tools" {
		t.Fatalf("probe_runs.operation = %q, want tools", operation)
	}
	if probeClass != string(intelligence.ProbeStandard) {
		t.Fatalf("probe_runs.probe_class = %q, want %q", probeClass, intelligence.ProbeStandard)
	}
	if execState != string(intelligence.ProbeSucceeded) {
		t.Fatalf("probe_runs.execution = %q, want succeeded (a terminal execution)", execState)
	}
	if !reservationID.Valid || reservationID.String == "" {
		t.Fatalf("probe_runs.reservation_id = %+v, want a non-empty reservation id (the provenance this test proves)", reservationID)
	}
}
