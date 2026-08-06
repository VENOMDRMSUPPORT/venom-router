package httpapi

// qualification_test.go is task-2's TDD suite (automatic-model-qualification
// design): the scheduler tick that is now the ONLY writer of
// models.quality_rating, since the dashboard's benchmark trigger was
// deleted. Fixtures are built through the REAL repositories/write paths
// (storage.NewBenchmarkRunRepo.Insert, seedCertifiedOffering's
// DiscoveryRepo.Apply + intelligence.CertificationDriver — never a raw
// INSERT), so a seeded row can never encode a shape production does not
// itself produce.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/observability"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// qualificationTickOption configures a test-only qualificationTick beyond its
// base wiring. Task 3 added capability-probe dependencies (a fake transport;
// the real driver/probeRuns pair over the same db) that most of this file's
// existing tests never touch, so they are opt-in rather than growing
// newQualificationTickForTest's positional parameter list a second time.
type qualificationTickOption func(*qualificationTick)

// withStream overrides the fake benchmarkStreamFn the performance-scoring
// pass (dueModels/measureOne) calls — every pre-existing test in this file
// uses this exactly where it used to pass stream directly as
// newQualificationTickForTest's third positional argument.
func withStream(stream benchmarkStreamFn) qualificationTickOption {
	return func(tick *qualificationTick) { tick.stream = stream }
}

// withCapabilityProbeTransport wires transport directly — the seam a test
// needs when it must also inspect the fake afterward (transport.callCount(),
// fix round 1's Critical 2 tests: "was the transport actually re-invoked").
// withCapabilityProbeResult below is the common case built on top of this.
func withCapabilityProbeTransport(transport *fakeProbeTransport) qualificationTickOption {
	return func(tick *qualificationTick) { tick.probeTransport = transport }
}

// withCapabilityProbeResult wires a fake, controllable intelligence.
// ProbeTransport that returns res for every call — the ONE seam task 3's
// capability-probe tests fake (fakeProbeTransport is probe_test.go's own
// type, reused here rather than redefined — same package). Every other
// capability-probe dependency (probe_runs bookkeeping, the certification
// driver, the guard's spend/in-flight/cooldown reads) stays the REAL
// storage-backed one this file's default wiring already builds over the
// same test db — only the reservation itself is faked
// (fakeAlwaysAdmitReserver below), since a real reservation additionally
// requires quota-window fixtures no test in this file otherwise needs.
func withCapabilityProbeResult(res intelligence.ProbeResult) qualificationTickOption {
	return withCapabilityProbeTransport(&fakeProbeTransport{available: true, result: res})
}

// withProbeGuardPolicy overrides the ProbeSafetyPolicy every capability
// probe is admitted against — fix round 1's Critical 2(a) test uses this to
// force a deterministic ADMISSION refusal (no cap at all configured), so it
// can prove a refused attempt records no spend without depending on any
// particular real cost number.
func withProbeGuardPolicy(policy intelligence.ProbeSafetyPolicy) qualificationTickOption {
	return func(tick *qualificationTick) { tick.probeGuardPolicy = policy }
}

// withNow overrides the tick's clock (and therefore the ProbeGuard built
// fresh inside every probeOneCapability call, which is admitted against
// THIS clock) — fix round 1's recertification test uses this to simulate
// "enough real time has passed that the capability-probe cooldown
// (qualificationCapabilityProbeCooldown) has cleared" without an actual
// sleep.
func withNow(now func() time.Time) qualificationTickOption {
	return func(tick *qualificationTick) { tick.now = now }
}

// fakeAlwaysAdmitReserver is the one non-storage-backed capability-probe
// dependency this file's tests use: a real reservation
// (storage.QuotaReservationRepo.Reserve) fails closed with
// quota.ErrNoApplicableWindow unless quota-window rows already exist for the
// account, and this file's fixtures have no reason to carry that unrelated
// setup just to prove a capability-probe outcome. It always admits,
// deterministically, never touching the database.
type fakeAlwaysAdmitReserver struct{}

func (fakeAlwaysAdmitReserver) ReserveProbe(_ context.Context, _, requestID, attemptID string, _ []quota.Allocation) (string, error) {
	return "res-" + requestID + "-" + attemptID, nil
}

// buildQualificationTickForTest builds a qualificationTick directly
// (bypassing BuildQualificationTick's production credential/dispatcher
// composition) so a test can inject a fake benchmarkStreamFn (withStream)
// and/or a fake capability-probe transport (withCapabilityProbeResult) and
// drive real-clock freshness/certification logic without any network I/O —
// the same shape as benchmark_test.go injecting a fake stream into
// NewBenchmarkHandler. Every capability-probe dependency besides the
// transport and the reservation is wired over the REAL repos against db,
// exactly as BuildQualificationTick wires them in production.
//
// It returns the raw *qualificationTick (fix round 2, item 2): most tests
// only need the bound tick.Run value newQualificationTickForTest below
// hands back, but a test that must assert on a private SELECTION method's
// own output directly (dueCapabilityProbes, rather than inferring it
// indirectly through transport call counts) needs the struct itself.
func buildQualificationTickForTest(t *testing.T, db *storage.DB, opts ...qualificationTickOption) *qualificationTick {
	t.Helper()

	certRepo := storage.NewCertificationRepo(db, nil)
	certAuditor := newCertificationAuditorAdapter(newAuditEmitter(db, nil))
	driver, err := intelligence.NewCertificationDriver(certRepo, certAuditor, intelligence.DefaultProbeRetryBudget, nil)
	if err != nil {
		t.Fatalf("NewCertificationDriver: %v", err)
	}

	tick := &qualificationTick{
		catalog: storage.NewCatalogRepo(db),
		runs:    storage.NewBenchmarkRunRepo(db, nil),
		newID:   newOAuthTransactionID,
		now:     time.Now,
		log:     observability.Default(),

		db: db,
		probeRuns: storage.NewProbeRunRepo(db, nil, intelligence.DefaultProbeSafetyPolicy().ContextProbeCooldown).
			WithCapabilityProbeCooldown(qualificationCapabilityProbeCooldown),
		probeReserver:    fakeAlwaysAdmitReserver{},
		probeGuardPolicy: intelligence.DefaultProbeSafetyPolicy(),
		driver:           driver,
	}
	for _, opt := range opts {
		opt(tick)
	}
	return tick
}

// newQualificationTickForTest is buildQualificationTickForTest's bound
// tick.Run value — what every test that only cares about running a round
// (rather than inspecting a private method's own output) actually wants.
func newQualificationTickForTest(t *testing.T, db *storage.DB, opts ...qualificationTickOption) func(context.Context) error {
	t.Helper()
	tick := buildQualificationTickForTest(t, db, opts...)
	return tick.Run
}

// seedLiveChatOffering builds one model with a LIVE chat offering — exactly
// what CatalogRepo.ListOfferings' LiveOnly gate requires (available offering,
// connected+healthy+not-reauthenticating account, certified+supported chat
// offering_operation) — through the real production write paths
// (seedCertifiedOffering, models_test.go: intelligence.DiscoveryRepo.Apply +
// intelligence.CertificationDriver.StartProbe/RecordAttempt), the same basis
// BenchmarkHandler.targetOffering already selects on (benchmark.go:327-344).
// providerModelID is the PROVIDER's own model id, matching seedArgs.ModelID's
// naming and callable straight back through qualityRatingOf/
// canonicalModelIDForProviderModel below.
func seedLiveChatOffering(t *testing.T, db *storage.DB, accountID, providerID, providerModelID string) {
	t.Helper()
	seedCertifiedOffering(t, db, seedArgs{
		AccountID:     accountID,
		ProviderID:    providerID,
		ModelID:       providerModelID,
		Capabilities:  []string{"chat"},
		Certified:     []string{"chat"},
		ContextTokens: 8192,
	})
}

// canonicalModelIDForProviderModel resolves a seeded provider model id to its
// canonical models.id — a read, never a competing write path — so tests can
// address the canonical id qualityRatingOf/SetQualityRating actually use.
func canonicalModelIDForProviderModel(t *testing.T, db *storage.DB, providerModelID string) string {
	t.Helper()
	var modelID string
	if err := db.Conn().QueryRow(
		`SELECT model_id FROM provider_model_aliases WHERE provider_model_id = ?`, providerModelID,
	).Scan(&modelID); err != nil {
		t.Fatalf("resolve canonical model id for %q: %v", providerModelID, err)
	}
	return modelID
}

// seedBenchmarkRun writes one benchmark_runs row for providerModelID's
// canonical model through the REAL BenchmarkRunRepo.Insert (never a raw
// INSERT), finished at finishedAt — the freshness clock dueModels reads.
func seedBenchmarkRun(t *testing.T, db *storage.DB, providerModelID string, finishedAt time.Time) {
	t.Helper()
	modelID := canonicalModelIDForProviderModel(t, db, providerModelID)
	runs := storage.NewBenchmarkRunRepo(db, func() time.Time { return finishedAt })
	ttft := int64(120)
	tps := 45.0
	rating := 0.6
	run := storage.BenchmarkRun{
		ID:              "seed-run-" + providerModelID,
		ModelID:         modelID,
		AccountID:       "acct-seed-" + providerModelID,
		ProviderID:      "prov-seed-" + providerModelID,
		ProviderModelID: providerModelID,
		Requests:        3,
		Successes:       3,
		TTFTMillis:      &ttft,
		TokensPerSec:    &tps,
		Rating:          &rating,
		StartedAt:       finishedAt.Add(-time.Second),
		FinishedAt:      finishedAt,
	}
	if err := runs.Insert(context.Background(), run); err != nil {
		t.Fatalf("seed benchmark run: %v", err)
	}
}

// qualityRatingOf reads models.quality_rating for the canonical model behind
// providerModelID, straight from the table — what is actually persisted,
// never a projection.
func qualityRatingOf(t *testing.T, db *storage.DB, providerModelID string) *float64 {
	t.Helper()
	modelID := canonicalModelIDForProviderModel(t, db, providerModelID)
	var v *float64
	if err := db.Conn().QueryRow(`SELECT quality_rating FROM models WHERE id = ?`, modelID).Scan(&v); err != nil {
		t.Fatalf("read quality_rating for %q: %v", modelID, err)
	}
	return v
}

// latestBenchmarkRunOf resolves providerModelID's canonical model and reads
// its most recent benchmark_runs row through the REAL
// BenchmarkRunRepo.LatestForModel read path (never a raw SELECT re-deriving
// the column layout, and never the aggregate the tick itself already
// computed) — so a test asserting on run.Rating is checking what production
// actually persisted, not re-deriving its own expectation from the code
// under test.
func latestBenchmarkRunOf(t *testing.T, db *storage.DB, providerModelID string) storage.BenchmarkRun {
	t.Helper()
	modelID := canonicalModelIDForProviderModel(t, db, providerModelID)
	runs := storage.NewBenchmarkRunRepo(db, nil)
	run, ok, err := runs.LatestForModel(context.Background(), modelID)
	if err != nil {
		t.Fatalf("LatestForModel(%q): %v", modelID, err)
	}
	if !ok {
		t.Fatalf("LatestForModel(%q): no row, want one", modelID)
	}
	return run
}

// TestQualificationTick_ScoresAModelThatHasNeverBeenMeasured is the task-2
// brief's Step 1 test, STRENGTHENED after fix-round 1 (see task-2-report.md):
// a model with a live chat offering and NO prior benchmark_runs row must be
// measured, and models.quality_rating must land on the EXACT 0-100 COLUMN
// value — not merely "some value in (0,100]", which a reviewer mutation
// proved cannot distinguish the correct 70.0 from the unscaled-raw-value
// defect (0.70 also satisfies "0 < x <= 100").
//
// The expected numbers are computed BY HAND from the fixture's own inputs
// (TTFT 200ms, 40 tok/s) and localBenchmarkRating's DOCUMENTED weights —
// deliberately NEVER by calling localBenchmarkRating/benchmarkRatingColumnScale
// from this test, which would let production supply its own expected value
// and make the assertion tautological again:
//
//	speed        = min(40/80, 1)        = 0.5
//	latency      = max(0, 1 - 200/2000) = 0.9
//	rawRating    = 0.5*0.5 + 0.5*0.9    = 0.70   (benchmark_runs.rating, 0..1)
//	columnRating = 0.70 * 100           = 70.0   (models.quality_rating, 0-100)
func TestQualificationTick_ScoresAModelThatHasNeverBeenMeasured(t *testing.T) {
	const wantRawRating = 0.70
	const wantColumnRating = 70.0

	db := testControlDB(t)
	seedLiveChatOffering(t, db, "acct-1", "prov-1", "m-1")

	var streamed int
	tick := newQualificationTickForTest(t, db, withStream(func(context.Context, string, string, string, string, int) (benchmarkSample, error) {
		streamed++
		return benchmarkSample{OK: true, TTFT: 200 * time.Millisecond, TokensPerSec: 40}, nil
	}))

	if err := tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if streamed == 0 {
		t.Fatal("the tick measured nothing; with the benchmark button deleted this tick is the only writer of a quality rating")
	}

	rating := qualityRatingOf(t, db, "m-1")
	if rating == nil {
		t.Fatal("quality_rating is still NULL — Not rated would stay unearnable")
	}
	if !floatsClose(*rating, wantColumnRating, 1e-9) {
		t.Fatalf("quality_rating = %v, want exactly %v (the 0-100 column scale) — %v would mean the raw 0..1 measurement leaked into this column unscaled (the exact prior incident benchmarkRatingColumnScale's doc comment documents)",
			*rating, wantColumnRating, wantRawRating)
	}

	// benchmark_runs.rating must independently hold the RAW 0..1 measurement
	// — the two scales living in two tables is the whole point of
	// benchmarkRatingColumnScale's doc comment, and pinning only the column
	// above would leave this one free to drift.
	run := latestBenchmarkRunOf(t, db, "m-1")
	if run.Rating == nil || !floatsClose(*run.Rating, wantRawRating, 1e-9) {
		t.Fatalf("benchmark_runs.rating = %v, want exactly %v (the raw 0..1 measurement)", run.Rating, wantRawRating)
	}
}

// TestQualificationTick_SkipsAModelMeasuredRecently is the task-2 brief's
// Step 1 test verbatim: a model already measured within the freshness TTL
// must not be re-streamed — re-measuring every scheduler round would spend
// the owner's quota on a number that barely moves.
func TestQualificationTick_SkipsAModelMeasuredRecently(t *testing.T) {
	db := testControlDB(t)
	seedLiveChatOffering(t, db, "acct-1", "prov-1", "m-1")
	seedBenchmarkRun(t, db, "m-1", time.Now().Add(-time.Hour))

	var streamed int
	tick := newQualificationTickForTest(t, db, withStream(func(context.Context, string, string, string, string, int) (benchmarkSample, error) {
		streamed++
		return benchmarkSample{OK: true}, nil
	}))
	if err := tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if streamed != 0 {
		t.Fatalf("streamed %d times, want 0 — re-measuring every 30s would spend the owner's quota on a number that barely moves", streamed)
	}
}

// TestQualificationTick_ReMeasuresAfterTheTTLExpires is the freshness TTL's
// other edge: a benchmark_runs row OLDER than qualificationFreshnessTTL must
// not protect the model from being re-measured forever — otherwise a model
// that was slow a year ago would stay "not due" indefinitely.
func TestQualificationTick_ReMeasuresAfterTheTTLExpires(t *testing.T) {
	db := testControlDB(t)
	seedLiveChatOffering(t, db, "acct-1", "prov-1", "m-1")
	seedBenchmarkRun(t, db, "m-1", time.Now().Add(-qualificationFreshnessTTL-time.Hour))

	var streamed int
	tick := newQualificationTickForTest(t, db, withStream(func(context.Context, string, string, string, string, int) (benchmarkSample, error) {
		streamed++
		return benchmarkSample{OK: true, TTFT: 100 * time.Millisecond, TokensPerSec: 50}, nil
	}))
	if err := tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if streamed == 0 {
		t.Fatal("streamed 0 times, want the stale row to make this model due again")
	}
}

// TestQualificationTick_SkipsAModelWithNoLiveOffering proves the eligibility
// rule matches targetOffering's own "live chat offering" basis: a model with
// an alias but no live offering (unhealthy account, no certified chat op,
// etc.) must never be dispatched — there is nothing this tick could safely
// measure.
func TestQualificationTick_SkipsAModelWithNoLiveOffering(t *testing.T) {
	db := testControlDB(t)
	// A provider/account pair exists, but seedCertifiedOffering is never
	// called for it — no offering, no certification, nothing "live".
	p3aSeedAccount(t, db, "acct-dead", "prov-dead")

	var streamed int
	tick := newQualificationTickForTest(t, db, withStream(func(context.Context, string, string, string, string, int) (benchmarkSample, error) {
		streamed++
		return benchmarkSample{OK: true}, nil
	}))
	if err := tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if streamed != 0 {
		t.Fatalf("streamed %d times, want 0 — there is no live offering to measure", streamed)
	}
}

// TestQualificationTick_NeverFabricatesARatingFromAPartialRun pins the
// never-relax invariant: when even one request in the suite fails,
// runBenchmarkSuite withholds Rating, and this tick must persist the
// benchmark_runs evidence WITHOUT ever writing models.quality_rating from a
// partial run.
func TestQualificationTick_NeverFabricatesARatingFromAPartialRun(t *testing.T) {
	db := testControlDB(t)
	seedLiveChatOffering(t, db, "acct-1", "prov-1", "m-1")

	calls := 0
	tick := newQualificationTickForTest(t, db, withStream(func(context.Context, string, string, string, string, int) (benchmarkSample, error) {
		calls++
		if calls == 2 {
			return benchmarkSample{OK: false}, nil
		}
		return benchmarkSample{OK: true, TTFT: 100 * time.Millisecond, TokensPerSec: 40}, nil
	}))
	if err := tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if rating := qualityRatingOf(t, db, "m-1"); rating != nil {
		t.Fatalf("quality_rating = %v, want NULL — one request failed, the success gate must withhold the rating", *rating)
	}

	modelID := canonicalModelIDForProviderModel(t, db, "m-1")
	var n int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM benchmark_runs WHERE model_id = ?`, modelID).Scan(&n); err != nil {
		t.Fatalf("count benchmark_runs: %v", err)
	}
	if n != 1 {
		t.Fatalf("benchmark_runs rows = %d, want 1 — the measurement is evidence even when the rating is withheld", n)
	}
}

// TestQualificationTick_CapsHowManyModelsOneRoundMeasures proves the
// per-round cap: with more due models than qualificationPerRoundCap, only
// qualificationPerRoundCap are actually streamed in one Run call — a fleet of
// many never-measured models must not stampede one provider in a single
// tick.
func TestQualificationTick_CapsHowManyModelsOneRoundMeasures(t *testing.T) {
	db := testControlDB(t)
	total := qualificationPerRoundCap + 3
	for i := 0; i < total; i++ {
		acct := "acct-cap-" + string(rune('a'+i))
		prov := "prov-cap-" + string(rune('a'+i))
		model := "m-cap-" + string(rune('a'+i))
		seedLiveChatOffering(t, db, acct, prov, model)
	}

	var streamed int
	tick := newQualificationTickForTest(t, db, withStream(func(context.Context, string, string, string, string, int) (benchmarkSample, error) {
		streamed++
		return benchmarkSample{OK: true, TTFT: 100 * time.Millisecond, TokensPerSec: 40}, nil
	}))
	if err := tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	wantSamples := qualificationPerRoundCap * benchmarkDefaultRequests
	if streamed != wantSamples {
		t.Fatalf("streamed %d times, want %d (%d models * %d requests) — the per-round cap must bound the fan-out",
			streamed, wantSamples, qualificationPerRoundCap, benchmarkDefaultRequests)
	}
}

// TestQualificationTick_HandlesAFleetLargerThanOnePage is fix-round 1's proof
// for the second finding: dueModels must walk ListOfferings' pagination and
// call BenchmarkRunRepo.LatestForModels bounded PER PAGE, never once over an
// unbounded id list accumulated across the whole fleet — LatestForModels'
// own doc comment (storage/benchmarkruns.go:94-99) is explicit that removing
// the caller-side page bound is a documented invariant every other caller
// (ServeModels) already respects. defaultCatalogListLimit (storage/
// catalog.go) is 50, so seeding more models than that forces ListOfferings
// across at least two pages; if dueModels regressed to one LatestForModels
// call over the whole accumulated list, this test would still pass at this
// fleet size (the real failure mode is a hard SQL error only far past
// SQLite's bind-parameter ceiling) — its job is to pin the PAGINATION
// walking + cap behaviour so a future change to page-at-a-time semantics is
// caught here rather than only in production at fleet-size scale.
func TestQualificationTick_HandlesAFleetLargerThanOnePage(t *testing.T) {
	db := testControlDB(t)
	total := 55 // > defaultCatalogListLimit (50): forces >= 2 ListOfferings pages.
	for i := 0; i < total; i++ {
		suffix := fmt.Sprintf("%03d", i)
		seedLiveChatOffering(t, db, "acct-page-"+suffix, "prov-page-"+suffix, "m-page-"+suffix)
	}

	var streamed int
	tick := newQualificationTickForTest(t, db, withStream(func(context.Context, string, string, string, string, int) (benchmarkSample, error) {
		streamed++
		return benchmarkSample{OK: true, TTFT: 100 * time.Millisecond, TokensPerSec: 40}, nil
	}))
	if err := tick(context.Background()); err != nil {
		t.Fatalf("tick across %d models spanning multiple ListOfferings pages: %v", total, err)
	}

	// The per-round cap still applies across a multi-page fleet — pagination
	// must not let the cap silently widen.
	wantSamples := qualificationPerRoundCap * benchmarkDefaultRequests
	if streamed != wantSamples {
		t.Fatalf("streamed %d times, want %d — the per-round cap must hold across a fleet spanning multiple ListOfferings pages", streamed, wantSamples)
	}
}

// TestBuildQualificationTick_ConstructsAWorkingTick proves
// BuildQualificationTick's own composition (production credential service +
// dispatcher via buildBenchmarkStreamFn) at least builds and runs without
// error against an empty fleet — the composition-root wiring itself, not the
// measurement logic (already covered above via the injected-stream tests).
func TestBuildQualificationTick_ConstructsAWorkingTick(t *testing.T) {
	db := testControlDB(t)
	kr := testKeyring(t)

	run, err := BuildQualificationTick(db, kr, nil)
	if err != nil {
		t.Fatalf("BuildQualificationTick: %v", err)
	}
	if err := run(context.Background()); err != nil {
		t.Fatalf("run() on an empty fleet: %v", err)
	}
}

// --- Task 3: upgrade declared capabilities to measured ones ---------------

// seedCertifiedDeclaredCapability builds one non-chat capability already
// certified/supported FROM ITS DECLARATION — exactly certifyDeclaredCapabilities'
// own product (usability_account.go) — through the real production write
// path (seedCertifiedOffering: intelligence.DiscoveryRepo.Apply +
// CertificationDriver.StartProbe/RecordAttempt), never a raw INSERT, so the
// seeded row cannot drift from what production actually certifies. It also
// grants the account generous local-safety quota windows so the REAL
// reservation engine wired into the capability-probe guard
// (newQualificationTickForTest's default probeRuns/driver wiring) can
// actually admit a probe attempt — the one piece of this fixture that is not
// itself under test (only the transport is faked; see
// withCapabilityProbeResult).
func seedCertifiedDeclaredCapability(t *testing.T, db *storage.DB, accountID, providerID, providerModelID, operation string) {
	t.Helper()
	seedCertifiedOffering(t, db, seedArgs{
		AccountID:     accountID,
		ProviderID:    providerID,
		ModelID:       providerModelID,
		Capabilities:  []string{operation},
		Certified:     []string{operation},
		ContextTokens: 8192,
	})

	windowRepo := storage.NewQuotaWindowRepo(db, nil, nil)
	if err := windowRepo.EnsureLocalSafetyWindows(context.Background(), accountID, generousLocalSafetySpecs(t)); err != nil {
		t.Fatalf("EnsureLocalSafetyWindows: %v", err)
	}
}

// offeringOperationIDFor resolves the offering_operations row id for one
// (account, provider-model, operation) triple — the join key
// hasSucceededProbeRun/certificationOf both need to address the exact row
// seedCertifiedDeclaredCapability built.
func offeringOperationIDFor(t *testing.T, db *storage.DB, accountID, providerModelID, operation string) string {
	t.Helper()
	var id string
	if err := db.Conn().QueryRow(
		`SELECT id FROM offering_operations WHERE account_id = ? AND provider_model_id = ? AND operation = ?`,
		accountID, providerModelID, operation,
	).Scan(&id); err != nil {
		t.Fatalf("resolve offering_operation id for (%q,%q,%q): %v", accountID, providerModelID, operation, err)
	}
	return id
}

// hasSucceededProbeRun reports whether a SUCCEEDED probe_runs row exists for
// the offering-operation identified by (accountID, providerModelID,
// operation) — read straight from probe_runs, the same table
// intelligence/readmodel.go's provenance derivation
// (ProbeRunRepo.SucceededOfferingOperationIDs) reads.
func hasSucceededProbeRun(t *testing.T, db *storage.DB, accountID, providerModelID, operation string) bool {
	t.Helper()
	opID := offeringOperationIDFor(t, db, accountID, providerModelID, operation)
	var n int
	if err := db.Conn().QueryRow(
		`SELECT COUNT(*) FROM probe_runs WHERE offering_operation_id = ? AND execution = 'succeeded'`,
		opID,
	).Scan(&n); err != nil {
		t.Fatalf("count succeeded probe runs for %q: %v", opID, err)
	}
	return n > 0
}

// certificationOf reads the certification row's (status, capability_truth)
// straight from the certifications table for the offering-operation
// identified by (accountID, providerModelID, operation) — never a
// projection, so a test asserting on it is checking what production actually
// persisted.
func certificationOf(t *testing.T, db *storage.DB, accountID, providerModelID, operation string) (state, truth string) {
	t.Helper()
	opID := offeringOperationIDFor(t, db, accountID, providerModelID, operation)
	if err := db.Conn().QueryRow(
		`SELECT status, capability_truth FROM certifications WHERE offering_operation_id = ?`,
		opID,
	).Scan(&state, &truth); err != nil {
		t.Fatalf("read certification for %q: %v", opID, err)
	}
	return state, truth
}

// TestQualificationTick_UpgradesADeclaredCapabilityToProbed is task-3's Step
// 1 test: a non-chat capability already certified/supported from its
// declaration, with no succeeded probe run yet, must be probed — and a
// genuine capability response (2xx, the required witness) must leave a
// SUCCEEDED probe_runs row behind. That row is exactly the fact
// intelligence/readmodel.go's provenance derivation (proved[op], built from
// ProbeRunRepo.SucceededOfferingOperationIDs) reads to move the chip from
// "declared" to "probed" — without it the chip would keep reading "declared"
// forever, even once a real probe exists to prove it.
func TestQualificationTick_UpgradesADeclaredCapabilityToProbed(t *testing.T) {
	db := testControlDB(t)
	seedCertifiedDeclaredCapability(t, db, "acct-1", "prov-1", "m-1", "tools")

	tick := newQualificationTickForTest(t, db,
		withCapabilityProbeResult(intelligence.ProbeResult{HTTPStatus: 200, Witness: intelligence.WitnessToolCall}))
	if err := tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if !hasSucceededProbeRun(t, db, "acct-1", "m-1", "tools") {
		t.Fatal("no succeeded probe run recorded; the read model derives provenance=probed from exactly this, so the chip would keep reading 'declared' forever")
	}
}

// TestQualificationTick_RateLimitNeverDowngradesADeclaredCapability is
// task-3's second Step 1 test, and this task's single most important
// assertion (04 §2's hard rule): a rate-limited probe attempt against an
// already certified/supported capability must leave the certification
// BYTE-IDENTICAL. intelligence.ClassifyProbeSignal maps a 429 to a
// non-definitive, retryable outcome, and models.Certification.Transition has
// no certified -> probing edge (models/certification.go's frozen legal-
// transition table), so CertificationDriver.RecordAttempt's own state
// machine rejects the write outright — never a second, bespoke "is this
// outcome definitive" check added by this tick.
//
// MINOR 7 (fix round 1): the transport's own call count is asserted too. A
// selection that silently selects nothing (a broken query, an accidentally
// tautological filter) would ALSO leave the certification unchanged and
// pass this test for the wrong reason — pinning "a probe was actually
// attempted" closes that gap.
func TestQualificationTick_RateLimitNeverDowngradesADeclaredCapability(t *testing.T) {
	db := testControlDB(t)
	seedCertifiedDeclaredCapability(t, db, "acct-1", "prov-1", "m-1", "tools")

	transport := &fakeProbeTransport{available: true, result: intelligence.ProbeResult{HTTPStatus: 429}}
	tick := newQualificationTickForTest(t, db, withCapabilityProbeTransport(transport))
	_ = tick(context.Background())

	if got := transport.callCount(); got != 1 {
		t.Fatalf("capability probe transport calls = %d, want 1 — a broken/empty selection must not be able to pass this invariant vacuously", got)
	}

	state, truth := certificationOf(t, db, "acct-1", "m-1", "tools")
	if state != "certified" || truth != "supported" {
		t.Fatalf("certification = %s/%s, want certified/supported unchanged — 04 §2's hard rule is that a rate limit must never flip a capability to false", state, truth)
	}
}

// provenanceOf reads GET /offerings' real "provenance" field for operation
// on providerModelID — the SAME read model (ModelsHandler.ServeOfferings,
// WithProbeRuns wired exactly as ControlMux wires it) models_test.go's own
// TestModels_CapabilityProvenanceFromProbeRuns exercises, run here against
// whatever this tick's own actions left in db. It is what "the read model
// does/does not report probed" actually means, rather than re-deriving that
// claim from probe_runs rows directly.
func provenanceOf(t *testing.T, db *storage.DB, providerModelID, operation string) string {
	t.Helper()
	h := NewModelsHandler(storage.NewCatalogRepo(db), nil).
		WithProbeRuns(storage.NewProbeRunRepo(db, nil, 7*24*time.Hour))

	rec := httptest.NewRecorder()
	h.ServeOfferings(rec, modelsRequest(http.MethodGet, "/api/control/v1/offerings"))
	if rec.Code != http.StatusOK {
		t.Fatalf("ServeOfferings status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	var env struct {
		Data []struct {
			ProviderModelID string `json:"provider_model_id"`
			Capabilities    []struct {
				Operation  string `json:"operation"`
				Provenance string `json:"provenance"`
			} `json:"capabilities"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode offerings: %v; body = %s", err, rec.Body.String())
	}
	for _, o := range env.Data {
		if o.ProviderModelID != providerModelID {
			continue
		}
		for _, c := range o.Capabilities {
			if c.Operation == operation {
				return c.Provenance
			}
		}
	}
	t.Fatalf("no capability %q found for provider model %q in offerings response", operation, providerModelID)
	return ""
}

// probeRunCount is the total probe_runs row count for offeringOperationID,
// of any execution — used to prove EITHER "an attempt happened" (rows > 0)
// or "a refused attempt recorded nothing at all" (rows == 0).
func probeRunCount(t *testing.T, db *storage.DB, offeringOperationID string) int {
	t.Helper()
	var n int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM probe_runs WHERE offering_operation_id = ?`, offeringOperationID).Scan(&n); err != nil {
		t.Fatalf("count probe runs for %q: %v", offeringOperationID, err)
	}
	return n
}

// probeRunCostRowCount is the total probe_run_costs row count across the
// whole database — the exact rows intelligence.ProbeSpendReader's
// ProbeSpendSince sums to enforce the PerAccount rolling cap (CRITICAL 2a:
// "a refused attempt records no spend").
func probeRunCostRowCount(t *testing.T, db *storage.DB) int {
	t.Helper()
	var n int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM probe_run_costs`).Scan(&n); err != nil {
		t.Fatalf("count probe_run_costs: %v", err)
	}
	return n
}

// TestQualificationTick_SemanticRejectionNeverRendersAsProbed is fix round
// 1's CRITICAL 1 test: a provider that explicitly disowns a capability via
// one of the three reliableUnsupportedCodes must never leave a SUCCEEDED
// probe_runs row behind, and the read model must never render it "probed".
// Before the fix, intelligence.ClassifyProbeSignal's own (correct)
// Execution=ProbeSucceeded for a semantic rejection was written straight
// into probe_runs.execution, so a proven-ABSENT capability rendered as
// "measured supported" — strictly worse than the pre-task-3 "declared"
// hedge, since the owner would then route real traffic into a capability
// the provider just rejected.
func TestQualificationTick_SemanticRejectionNeverRendersAsProbed(t *testing.T) {
	db := testControlDB(t)
	seedCertifiedDeclaredCapability(t, db, "acct-1", "prov-1", "m-1", "tools")

	transport := &fakeProbeTransport{available: true, result: intelligence.ProbeResult{HTTPStatus: 400, ProviderCode: "tool_use_not_supported"}}
	tick := newQualificationTickForTest(t, db, withCapabilityProbeTransport(transport))
	if err := tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if got := transport.callCount(); got != 1 {
		t.Fatalf("capability probe transport calls = %d, want 1", got)
	}

	if hasSucceededProbeRun(t, db, "acct-1", "m-1", "tools") {
		t.Fatal("a proven-absent capability must never be recorded as a succeeded probe run — that is exactly what would render the chip 'probed' while truth stays stuck at 'supported'")
	}
	if got := provenanceOf(t, db, "m-1", "tools"); got == "probed" {
		t.Fatalf("read-model provenance = %q, want anything but \"probed\" — a provider that explicitly disowned this capability must never be surfaced as measured-supported", got)
	}
}

// TestQualificationTick_DefinitiveNegativeSuspendsAndStopsTheTreadmill is
// fix round 2's ITEM 1 test — the important one. Fix round 1 stopped a
// semantic rejection from rendering as "probed" (the test above), but left
// the certification itself certified/supported and the candidate endlessly
// re-selected: certified -> certified is illegal for ANY outcome, so no
// verdict from RecordAttempt could ever land, and the read model's honest
// "declared" hedge was still wrong about what was actually measured and
// paid for — the provider explicitly said "no". This mirrors Important
// 4's own terminal-failure suspend (certified -> suspended, edge 6, legal)
// applied to the OTHER outcome that can never legally reach
// certified -> certified: a definitive negative.
func TestQualificationTick_DefinitiveNegativeSuspendsAndStopsTheTreadmill(t *testing.T) {
	db := testControlDB(t)
	seedCertifiedDeclaredCapability(t, db, "acct-1", "prov-1", "m-1", "tools")

	transport := &fakeProbeTransport{available: true, result: intelligence.ProbeResult{HTTPStatus: 400, ProviderCode: "tool_use_not_supported"}}
	tick := newQualificationTickForTest(t, db, withCapabilityProbeTransport(transport))
	if err := tick(context.Background()); err != nil {
		t.Fatalf("round 1: %v", err)
	}
	if got := transport.callCount(); got != 1 {
		t.Fatalf("transport calls after round 1 = %d, want 1", got)
	}

	state, truth := certificationOf(t, db, "acct-1", "m-1", "tools")
	if state != "suspended" {
		t.Fatalf("certification state = %q, want \"suspended\" — a definitive negative must stop this capability from staying routable behind evidence that just contradicted it", state)
	}
	if truth != "supported" {
		t.Fatalf("certification truth = %q, want \"supported\" unchanged — Transition's own default branch never touches Truth on the certified -> suspended edge, exactly like Important 4's terminal-failure suspend", truth)
	}

	// The treadmill: with the row now suspended (not certified/supported),
	// dueCapabilityProbes' own `c.status = 'certified'` filter must stop
	// selecting it — a second round must not re-attempt it at all.
	if err := tick(context.Background()); err != nil {
		t.Fatalf("round 2: %v", err)
	}
	if got := transport.callCount(); got != 1 {
		t.Fatalf("transport calls after round 2 = %d, want STILL 1 — a suspended row must never be re-selected; the treadmill must be terminated, not merely slowed by the cooldown", got)
	}
}

// TestQualificationTick_RefusedCapabilityProbeRecordsNoSpend is fix round
// 1's CRITICAL 2(a) test: when ProbeGuard.Admit refuses an attempt (here,
// deliberately, by configuring a policy with NO per-probe caps at all —
// checkCaps refuses any allocation whose unit has no configured cap), the
// transport must never be called, AND neither a probe_runs row nor a
// probe_run_costs row may exist afterward. Before the fix, probeRuns.Start
// ran before the guard was even built, so a refused attempt still
// permanently consumed the account's rolling probe-spend budget.
func TestQualificationTick_RefusedCapabilityProbeRecordsNoSpend(t *testing.T) {
	db := testControlDB(t)
	seedCertifiedDeclaredCapability(t, db, "acct-1", "prov-1", "m-1", "tools")

	policy := intelligence.DefaultProbeSafetyPolicy()
	policy.PerProbe = nil // every allocation now has "no cap configured" -> refused

	transport := &fakeProbeTransport{available: true, result: intelligence.ProbeResult{HTTPStatus: 200, Witness: intelligence.WitnessToolCall}}
	tick := newQualificationTickForTest(t, db, withCapabilityProbeTransport(transport), withProbeGuardPolicy(policy))
	_ = tick(context.Background())

	if got := transport.callCount(); got != 0 {
		t.Fatalf("capability probe transport calls = %d, want 0 — the guard must refuse before the transport is ever reached", got)
	}

	opID := offeringOperationIDFor(t, db, "acct-1", "m-1", "tools")
	if n := probeRunCount(t, db, opID); n != 0 {
		t.Fatalf("probe_runs rows = %d, want 0 — a refused attempt must record no evidence at all", n)
	}
	if n := probeRunCostRowCount(t, db); n != 0 {
		t.Fatalf("probe_run_costs rows = %d, want 0 — a refused attempt must record no spend", n)
	}
}

// TestQualificationTick_NonSucceedingCapabilityProbeIsNotRetriedNextRound is
// fix round 1's CRITICAL 2(b) test: a candidate whose attempt did not
// succeed (here, rate-limited) must not have its transport re-invoked on
// the very next round. Before the fix, dueCapabilityProbes' own "no
// succeeded run yet" selection re-picked the identical candidate every
// round with no backoff at all — measured at 10 attempts over 10 rounds.
func TestQualificationTick_NonSucceedingCapabilityProbeIsNotRetriedNextRound(t *testing.T) {
	db := testControlDB(t)
	seedCertifiedDeclaredCapability(t, db, "acct-1", "prov-1", "m-1", "tools")

	transport := &fakeProbeTransport{available: true, result: intelligence.ProbeResult{HTTPStatus: 429}}
	tick := newQualificationTickForTest(t, db, withCapabilityProbeTransport(transport))

	if err := tick(context.Background()); err != nil {
		t.Fatalf("round 1: %v", err)
	}
	if got := transport.callCount(); got != 1 {
		t.Fatalf("transport calls after round 1 = %d, want 1", got)
	}

	if err := tick(context.Background()); err != nil {
		t.Fatalf("round 2: %v", err)
	}
	if got := transport.callCount(); got != 1 {
		t.Fatalf("transport calls after round 2 = %d, want STILL 1 — a non-succeeding attempt must not be retried on the very next round; the capability-probe cooldown must have refused it before the transport was ever reached", got)
	}
}

// TestQualificationTick_ReProbesAfterRecertificationInvalidatesThePriorRun is
// fix round 1's IMPORTANT 3 test: after a certification EXPIRES and is
// re-certified from a bare declaration (simulated here by bumping
// certified_at past the existing succeeded run's finished_at, exactly what
// probe_recertify's real edge does), the tick must select this candidate
// again — the old succeeded run predates the new certification and must not
// count. Before the fix, dueCapabilityProbes only asked "does ANY succeeded
// run exist", so it would never select this row again, and the read model
// (which DOES apply the certified_at >= threshold) had already silently
// fallen back to "declared" with no path back to "probed".
func TestQualificationTick_ReProbesAfterRecertificationInvalidatesThePriorRun(t *testing.T) {
	db := testControlDB(t)
	seedCertifiedDeclaredCapability(t, db, "acct-1", "prov-1", "m-1", "tools")

	firstTransport := &fakeProbeTransport{available: true, result: intelligence.ProbeResult{HTTPStatus: 200, Witness: intelligence.WitnessToolCall}}
	tick := newQualificationTickForTest(t, db, withCapabilityProbeTransport(firstTransport))
	if err := tick(context.Background()); err != nil {
		t.Fatalf("first tick: %v", err)
	}
	if !hasSucceededProbeRun(t, db, "acct-1", "m-1", "tools") {
		t.Fatal("setup: the first probe must have succeeded")
	}
	if got := provenanceOf(t, db, "m-1", "tools"); got != "probed" {
		t.Fatalf("setup: provenance = %q, want \"probed\" before simulating recertification", got)
	}

	// Simulate recertification: bump certified_at to AFTER the existing
	// succeeded run's finished_at.
	opID := offeringOperationIDFor(t, db, "acct-1", "m-1", "tools")
	future := time.Now().Add(24 * time.Hour).Unix()
	if _, err := db.Conn().Exec(`UPDATE certifications SET certified_at = ? WHERE offering_operation_id = ?`, future, opID); err != nil {
		t.Fatalf("simulate recertification: %v", err)
	}

	if got := provenanceOf(t, db, "m-1", "tools"); got != "declared" {
		t.Fatalf("provenance after recertification = %q, want \"declared\" — the stale pre-recertification run must no longer count", got)
	}

	// The capability-probe cooldown (qualificationCapabilityProbeCooldown,
	// keyed off the LAST ATTEMPT regardless of outcome — see its own doc
	// comment) is unconditional and would otherwise still be blocking this
	// second attempt purely from real wall-clock proximity to the first one,
	// which is not what this test is about: it exists to prove
	// RE-SELECTION after recertification, not to race the cooldown. Advance
	// the tick's own clock past BOTH the cooldown window AND the bumped
	// certified_at above (derived FROM future, so it is unconditionally
	// later, never independently computed and hoped to line up).
	later := time.Unix(future, 0).Add(2 * qualificationCapabilityProbeCooldown)
	secondTransport := &fakeProbeTransport{available: true, result: intelligence.ProbeResult{HTTPStatus: 200, Witness: intelligence.WitnessToolCall}}
	secondTick := newQualificationTickForTest(t, db, withCapabilityProbeTransport(secondTransport), withNow(func() time.Time { return later }))
	if err := secondTick(context.Background()); err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if got := secondTransport.callCount(); got != 1 {
		t.Fatalf("second tick's transport calls = %d, want 1 — the row must be selected again after recertification invalidated the prior run", got)
	}
	if got := provenanceOf(t, db, "m-1", "tools"); got != "probed" {
		t.Fatalf("provenance after the fresh probe = %q, want \"probed\" again", got)
	}
}

// TestQualificationTick_TerminalFailureSuspendsAnAlreadyCertifiedCapability
// is fix round 1's IMPORTANT 4 test: a 401/403 is the ONE outcome that
// legally moves an already certified/supported row (certified -> suspended,
// edge 6) — pinned here as the deliberate, intended behaviour it is (not
// incidental): recovery from a credential rejection is the review drainer's
// job, and leaving a rejected credential's capability routable would be
// worse than suspending it.
func TestQualificationTick_TerminalFailureSuspendsAnAlreadyCertifiedCapability(t *testing.T) {
	db := testControlDB(t)
	seedCertifiedDeclaredCapability(t, db, "acct-1", "prov-1", "m-1", "tools")

	tick := newQualificationTickForTest(t, db,
		withCapabilityProbeResult(intelligence.ProbeResult{HTTPStatus: 401}))
	if err := tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	state, truth := certificationOf(t, db, "acct-1", "m-1", "tools")
	if state != "suspended" {
		t.Fatalf("certification state = %q, want \"suspended\" — a genuine credential-blocked outcome is the one legal write path from an infrastructure signal onto an already-certified row", state)
	}
	if truth != "supported" {
		t.Fatalf("certification truth = %q, want \"supported\" unchanged — Transition's own default branch never touches Truth on the certified -> suspended edge", truth)
	}
}

// TestQualificationTick_NeverProbesADisconnectedAccountsCapability is fix
// round 1's IMPORTANT 5 test: dueCapabilityProbes must apply the SAME
// liveness gate dueModels applies (CatalogRepo.ListOfferings' LiveOnly) —
// a disconnected account's otherwise-eligible capability must never be
// probed. Before the fix, dueCapabilityProbes filtered on nothing but the
// certification row, so a disconnected/unhealthy account's capabilities
// were probed anyway, wasting quota against an account that cannot even
// serve the request.
func TestQualificationTick_NeverProbesADisconnectedAccountsCapability(t *testing.T) {
	db := testControlDB(t)
	seedCertifiedDeclaredCapability(t, db, "acct-1", "prov-1", "m-1", "tools")

	if _, err := db.Conn().Exec(`UPDATE accounts SET connection_state = 'disconnected' WHERE id = ?`, "acct-1"); err != nil {
		t.Fatalf("simulate disconnected account: %v", err)
	}

	transport := &fakeProbeTransport{available: true, result: intelligence.ProbeResult{HTTPStatus: 200, Witness: intelligence.WitnessToolCall}}
	tick := newQualificationTickForTest(t, db, withCapabilityProbeTransport(transport))
	if err := tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if got := transport.callCount(); got != 0 {
		t.Fatalf("capability probe transport calls = %d, want 0 — a disconnected account's capability must never be probed", got)
	}
	if hasSucceededProbeRun(t, db, "acct-1", "m-1", "tools") {
		t.Fatal("no probe run of any kind should exist for a disconnected account's capability")
	}
}

// TestQualificationTick_NeverProbesAWithdrawnOfferingsCapability is fix
// round 2's ITEM 3 test: the OTHER half of Important 5's liveness gate —
// account_model_offerings.availability = 'available' — had no test of its
// own. A healthy, connected account whose offering has since been
// withdrawn (the SAME state DiscoveryRepo.withdrawMissing sets when a
// provider stops listing a model) must not be probed either. Before this
// test, deleting the `amo.availability = 'available'` condition from
// dueCapabilityProbes left the entire qualification suite green.
func TestQualificationTick_NeverProbesAWithdrawnOfferingsCapability(t *testing.T) {
	db := testControlDB(t)
	seedCertifiedDeclaredCapability(t, db, "acct-1", "prov-1", "m-1", "tools")

	if _, err := db.Conn().Exec(`UPDATE account_model_offerings SET availability = 'withdrawn' WHERE account_id = ? AND provider_model_id = ?`, "acct-1", "m-1"); err != nil {
		t.Fatalf("simulate withdrawn offering: %v", err)
	}

	transport := &fakeProbeTransport{available: true, result: intelligence.ProbeResult{HTTPStatus: 200, Witness: intelligence.WitnessToolCall}}
	tick := newQualificationTickForTest(t, db, withCapabilityProbeTransport(transport))
	if err := tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if got := transport.callCount(); got != 0 {
		t.Fatalf("capability probe transport calls = %d, want 0 — a withdrawn offering must never be probed even on a healthy, connected account", got)
	}
	if hasSucceededProbeRun(t, db, "acct-1", "m-1", "tools") {
		t.Fatal("no probe run of any kind should exist for a withdrawn offering")
	}
}

// TestQualificationTick_CapsHowManyCapabilityProbesOneRoundRuns is fix round
// 1's MINOR 6(a) test: with more due capability probes than
// qualificationCapabilityProbeCap, only that many are actually attempted in
// one Run call. Mutation this pins: replacing the capped slice with the
// full due list.
func TestQualificationTick_CapsHowManyCapabilityProbesOneRoundRuns(t *testing.T) {
	db := testControlDB(t)
	total := qualificationCapabilityProbeCap + 3
	for i := 0; i < total; i++ {
		suffix := fmt.Sprintf("%03d", i)
		seedCertifiedDeclaredCapability(t, db, "acct-cp-"+suffix, "prov-cp-"+suffix, "m-cp-"+suffix, "tools")
	}

	transport := &fakeProbeTransport{available: true, result: intelligence.ProbeResult{HTTPStatus: 200, Witness: intelligence.WitnessToolCall}}
	tick := newQualificationTickForTest(t, db, withCapabilityProbeTransport(transport))
	if err := tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if got := transport.callCount(); got != qualificationCapabilityProbeCap {
		t.Fatalf("capability probe transport calls = %d, want %d — the per-round cap must bound the fan-out", got, qualificationCapabilityProbeCap)
	}
}

// TestQualificationTick_SelectionNeverIncludesChatOrContextWindow is fix
// round 2's ITEM 2 test, replacing the transport-call-count assertion below
// as the thing that actually pins capabilityProbeOperations: it asserts on
// dueCapabilityProbes' own OUTPUT directly. The reviewer's mutation — adding
// models.OperationChat and models.OperationContextWindow to
// capabilityProbeOperations — survived TestQualificationTick_
// NeverProbesChatOrContextWindow below unnoticed: CapabilityProbe.Run looks
// up CapabilityFixture/RequiredWitness BEFORE Admit (capabilityprobe.go),
// so a chat/context_window row bails out with ErrNoCapabilityFixture and
// never reaches the transport — the zero-call assertion was enforced by the
// FIXTURE layer, not by the operation list this test's own doc comment
// claimed it pinned. Asserting on the selection's own output instead means
// this test fails exactly when the list changes, never when a downstream
// layer happens to also catch it.
func TestQualificationTick_SelectionNeverIncludesChatOrContextWindow(t *testing.T) {
	db := testControlDB(t)
	seedCertifiedDeclaredCapability(t, db, "acct-chat", "prov-chat", "m-chat", "chat")
	seedCertifiedDeclaredCapability(t, db, "acct-ctx", "prov-ctx", "m-ctx", "context_window")

	tick := buildQualificationTickForTest(t, db)
	due, err := tick.dueCapabilityProbes(context.Background())
	if err != nil {
		t.Fatalf("dueCapabilityProbes: %v", err)
	}
	for _, c := range due {
		if c.Operation == models.OperationChat || c.Operation == models.OperationContextWindow {
			t.Fatalf("dueCapabilityProbes returned operation %q for offering-operation %q — chat and context_window must never be in capabilityProbeOperations", c.Operation, c.OfferingOperationID)
		}
	}
}

// TestQualificationTick_NeverProbesChatOrContextWindow is fix round 1's
// MINOR 6(b) test, kept as defense-in-depth: even IF the operation list
// were ever mistakenly widened, CapabilityProbe.Run's own fixture lookup
// (CapabilityFixture/RequiredWitness) would still refuse chat/context_window
// before the transport is ever reached. It is NOT, by itself, proof that
// capabilityProbeOperations excludes them — see
// TestQualificationTick_SelectionNeverIncludesChatOrContextWindow above for
// the test that actually pins the operation list itself (fix round 2, item
// 2 — the reviewer's mutation on capabilityProbeOperations survived THIS
// test unnoticed for exactly that reason).
func TestQualificationTick_NeverProbesChatOrContextWindow(t *testing.T) {
	db := testControlDB(t)
	// acct-chat's certified "chat" op incidentally also satisfies dueModels'
	// OWN live-chat-offering gate (they share the same certified+supported
	// chat basis) — this test seeds it purely to prove the CAPABILITY-probe
	// pass never touches it, so a harmless withStream keeps the SIBLING
	// benchmark-rating pass from panicking on a nil stream; it is not what
	// this test is about.
	seedCertifiedDeclaredCapability(t, db, "acct-chat", "prov-chat", "m-chat", "chat")
	seedCertifiedDeclaredCapability(t, db, "acct-ctx", "prov-ctx", "m-ctx", "context_window")

	transport := &fakeProbeTransport{available: true, result: intelligence.ProbeResult{HTTPStatus: 200, Witness: intelligence.WitnessToolCall}}
	tick := newQualificationTickForTest(t, db, withCapabilityProbeTransport(transport),
		withStream(func(context.Context, string, string, string, string, int) (benchmarkSample, error) {
			return benchmarkSample{OK: true, TTFT: 100 * time.Millisecond, TokensPerSec: 40}, nil
		}))
	if err := tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if got := transport.callCount(); got != 0 {
		t.Fatalf("capability probe transport calls = %d, want 0 — chat and context_window must never be probed by this pass", got)
	}
}
