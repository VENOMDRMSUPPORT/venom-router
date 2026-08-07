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
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
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

// withContextProbe replaces the tick's probeContext seam (task 4) with fn —
// the task-4 brief's own test shape: a plain (ctx, providerModelID) ->
// (limit, ok) function, deliberately simpler than injecting a fake
// intelligence.ProbeTransport, since dueContextProbes' SELECTION is what
// this file's own tests exercise — the extraction ladder itself is already
// unit-tested in intelligence/contextprobe_test.go. fn is called with
// c.ProviderModelID (the identifier every seed helper in this file, and the
// brief's own test, addresses a model by — e.g. "known"/"unknown" — not the
// internal canonical models.id).
func withContextProbe(fn func(context.Context, string) (int, bool)) qualificationTickOption {
	return func(tick *qualificationTick) {
		tick.probeContext = func(ctx context.Context, c contextProbeCandidate) (int, bool) {
			return fn(ctx, c.ProviderModelID)
		}
	}
}

// withContextProbeTransport is withCapabilityProbeTransport under another
// name: intelligence.ProbeTransport is shared by both probes in production
// (a single probeTransportAdapter instance) and by this file's tick struct
// (one probeTransport field) — task-4's own tests that exercise the REAL
// runContextProbe path (rather than faking probeContext wholesale) use this
// name so a reader is not left wondering why a "capability" helper appears
// in a context-probe test.
func withContextProbeTransport(transport *fakeProbeTransport) qualificationTickOption {
	return withCapabilityProbeTransport(transport)
}

// withContextProbeGuardPolicy overrides the ProbeSafetyPolicy the context
// probe (task 4) is admitted against — used to prove the negative case
// directly: WITHOUT this task's own ExpensiveProbesEnabled opt-in
// (buildQualificationTickForTest's default already flips it, mirroring
// BuildQualificationTick's production wiring), a context-probe attempt must
// be refused before ever reaching the transport.
func withContextProbeGuardPolicy(policy intelligence.ProbeSafetyPolicy) qualificationTickOption {
	return func(tick *qualificationTick) { tick.contextProbeGuardPolicy = policy }
}

// withContextProbeCap overrides the tick's per-round context-probe cap
// (fix round 1, MINOR 1) — used by the mutation-sensitive selection test to
// ensure a 2-candidate fixture is never itself truncated by
// qualificationContextProbeCap's own production value of 1, which would
// otherwise mask the very assertion the mutation is meant to trip.
func withContextProbeCap(n int) qualificationTickOption {
	return func(tick *qualificationTick) { tick.contextProbeCap = n }
}

// withTickBudget overrides the per-phase deadline Run() applies to each of
// its three phases (whole-branch review, FIX 6) — mirrors
// usabilityTick.budget's own test-injection field exactly (see
// qualificationTick.tickBudget's own doc comment).
func withTickBudget(d time.Duration) qualificationTickOption {
	return func(tick *qualificationTick) { tick.tickBudget = d }
}

// withSettings wires tick.settings to a REAL operationalSettings resolver
// over db — the same production seam BuildQualificationTick wires
// (whole-branch review, FIX 4) — so a test can prove the owner-settings
// gates (enrichment_enabled, probe_expensive_enabled, and the probe
// concurrency/window caps) actually reach this tick, rather than merely
// asserting on the probeGuardPolicy/contextProbeGuardPolicy FIELDS this
// file's other tests set directly. Every other test in this file leaves
// tick.settings nil (buildQualificationTickForTest's own default), so
// wiring it is the deliberate, explicit signal that a test means to
// exercise the real gate.
func withSettings(db *storage.DB) qualificationTickOption {
	return func(tick *qualificationTick) {
		tick.settings = newOperationalSettings(storage.NewSettingsRepo(db))
	}
}

// putOperationalSettings writes the owner_settings row's enrichment and
// probe-expensive toggles directly through the REAL storage.SettingsRepo
// write path (PutSettings) — never a raw INSERT — so a FIX 4 test's fixture
// can never encode a shape production itself does not produce.
func putOperationalSettings(t *testing.T, db *storage.DB, enrichmentEnabled, probeExpensiveEnabled bool) {
	t.Helper()
	repo := storage.NewSettingsRepo(db)
	if err := repo.PutSettings(context.Background(), storage.SettingsUpdate{
		Theme: storage.DefaultTheme, Density: storage.DefaultDensity,
		Accent: storage.DefaultAccent, RadiusPx: storage.DefaultRadiusPx, SpacingScale: storage.DefaultSpacingScale,
		EnrichmentEnabled:     &enrichmentEnabled,
		ProbeExpensiveEnabled: &probeExpensiveEnabled,
	}, time.Now()); err != nil {
		t.Fatalf("PutSettings: %v", err)
	}
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

// buildQualificationTickForTest builds a qualificationTick by calling
// buildQualificationTick — the REAL production composition root
// (qualification.go) — and overriding ONLY the three seams that would
// otherwise perform real network I/O or need quota-window fixtures no test
// in this file sets up: the benchmark stream, the capability/context probe
// transport, and the quota reserver. A test injects a fake benchmarkStreamFn
// (withStream) and/or a fake capability-probe transport
// (withCapabilityProbeResult) and drives real-clock freshness/certification
// logic without any network I/O — the same shape as benchmark_test.go
// injecting a fake stream into NewBenchmarkHandler.
//
// Whole-branch review, FIX 1 (Critical): this function used to hand-build
// the tick struct field by field, independently of buildQualificationTick.
// The two constructions had drifted — four production field assignments
// (contextProbeGuardPolicy.ExpensiveProbesEnabled — since replaced by FIX
// 4b's settings-based resolution, see below —, contextProbeCap,
// tick.probeContext, and the capability-probe cooldown) had no test
// coverage at all, because this function never went anywhere near the code
// that sets them. Routing through buildQualificationTick itself means every
// field this file's tests do not explicitly override (via the opts below)
// is EXACTLY what production sets it to — deleting contextProbeCap,
// tick.probeContext, or the capability-probe cooldown assignment in
// qualification.go now fails a test in this file instead of leaving the
// whole suite green (manually verified, each in isolation, when this fix
// landed — see the final fix report's FIX 1 section for the traces).
//
// It returns the raw *qualificationTick (fix round 2, item 2): most tests
// only need the bound tick.Run value newQualificationTickForTest below
// hands back, but a test that must assert on a private SELECTION method's
// own output directly (dueCapabilityProbes, rather than inferring it
// indirectly through transport call counts) needs the struct itself.
func buildQualificationTickForTest(t *testing.T, db *storage.DB, opts ...qualificationTickOption) *qualificationTick {
	t.Helper()

	kr := testKeyring(t)
	tick, err := buildQualificationTick(db, kr, time.Now)
	if err != nil {
		t.Fatalf("buildQualificationTick: %v", err)
	}

	// Override ONLY the network-touching seams — never any of the safety
	// wiring (cooldowns, caps, the probeContext assignment) this fix
	// exists to keep test-visible.
	tick.stream = nil
	tick.probeTransport = nil
	tick.probeReserver = fakeAlwaysAdmitReserver{}
	// FIX 4 (whole-branch review): t.settings is production's REAL
	// owner-settings resolver (buildQualificationTick wires it to the
	// actual storage.SettingsRepo over this same db). Nil it here for the
	// SAME reason stream/probeTransport/probeReserver are nil'd above —
	// every EXISTING test in this file drives probeGuardPolicy/
	// contextProbeGuardPolicy directly (via withProbeGuardPolicy/
	// withContextProbeGuardPolicy, or this function's own convenience
	// default just below) and none of them seed a settings row, so a
	// live resolver would silently override every one of those tests back
	// to storage.DefaultSettingsRow()'s off-by-default values. A test that
	// means to prove the settings gate itself wires this seam explicitly
	// (withSettings).
	tick.settings = nil
	// Test-only convenience default (does NOT mirror production — see
	// t.settings' own doc comment above for how production actually
	// decides this value now): ExpensiveProbesEnabled=true, so a test
	// exercising the REAL runContextProbe path (rather than replacing
	// probeContext wholesale via withContextProbe) gets this task's own
	// narrow opt-in without needing to seed a settings row just to keep
	// testing context-probe SELECTION, which is what most of those tests
	// are actually about. withContextProbeGuardPolicy overrides it to
	// prove the negative case; withSettings proves the real gate.
	tick.contextProbeGuardPolicy.ExpensiveProbesEnabled = true

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

// seedLiveChatOfferingNoContext is seedLiveChatOffering with NO catalog-
// declared context at all (task 4): ContextTokens is left at its zero
// value, so seedCertifiedOffering's DiscoverySnapshotModel.ContextLength
// still carries a non-nil *int (intPtrArg only maps a NIL pointer to SQL
// NULL — seedCertifiedOffering always takes the address of its local
// contextTokens variable, never a nil pointer), but pointing at 0.
// models.EffectiveContext's own positiveOrNil treats a non-positive value
// as UNKNOWN exactly like an absent one, so this offering has no
// catalog-declared context and — nothing having probed it yet — no native
// fact either: genuinely unknown, the fixture dueContextProbes must select.
func seedLiveChatOfferingNoContext(t *testing.T, db *storage.DB, accountID, providerID, providerModelID string) {
	t.Helper()
	seedCertifiedOffering(t, db, seedArgs{
		AccountID:    accountID,
		ProviderID:   providerID,
		ModelID:      providerModelID,
		Capabilities: []string{"chat"},
		Certified:    []string{"chat"},
	})
}

// nativeContextTokensOf reads models.native_context_tokens for the
// canonical model behind providerModelID, straight from the table — what
// is actually persisted by DiscoveryRepo.SetNativeContextTokens, never a
// projection.
func nativeContextTokensOf(t *testing.T, db *storage.DB, providerModelID string) *int {
	t.Helper()
	modelID := canonicalModelIDForProviderModel(t, db, providerModelID)
	var v *int
	if err := db.Conn().QueryRow(`SELECT native_context_tokens FROM models WHERE id = ?`, modelID).Scan(&v); err != nil {
		t.Fatalf("read native_context_tokens for %q: %v", modelID, err)
	}
	return v
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
//
// wantSamples is a LITERAL (whole-branch review, MINOR — the same
// tautology TestQualificationTick_CapsHowManyContextProbesOneRoundRuns's
// own doc comment already names and fixes for the CONTEXT-probe cap, never
// actually applied here): comparing production's own output against
// production's own constants (qualificationPerRoundCap *
// benchmarkDefaultRequests) can only ever catch the cap slice being
// removed entirely, never either constant being changed to a different
// (still enforced) wrong value. 15 = 5 (qualificationPerRoundCap's actual
// value as of this test) * 3 (benchmarkDefaultRequests's actual value as
// of this test), hand-pinned here independently.
func TestQualificationTick_CapsHowManyModelsOneRoundMeasures(t *testing.T) {
	const wantSamples = 15

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

	if streamed != wantSamples {
		t.Fatalf("streamed %d times, want %d — the per-round cap must bound the fan-out", streamed, wantSamples)
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
	// must not let the cap silently widen. wantSamples is a LITERAL
	// (whole-branch review, MINOR — see TestQualificationTick_
	// CapsHowManyModelsOneRoundMeasures' own identical fix's doc comment
	// for why comparing production's output against production's own
	// constants is a tautology): 15 = qualificationPerRoundCap (5) *
	// benchmarkDefaultRequests (3), hand-pinned independently.
	const wantSamples = 15
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

// TestBuildQualificationTick_HonoursTheRealEnrichmentGate strengthens the
// composition-root proof above (whole-branch review, FIX 4a): the test
// above runs against an EMPTY fleet and asserts only err == nil — deleting
// buildQualificationTick's settings wiring entirely would leave that test
// green, because an empty fleet never reaches the gate's own effect. This
// test seeds one live, due model through the REAL production composition
// (BuildQualificationTick itself, never buildQualificationTickForTest's
// test-only overrides) and proves that, on a fresh install
// (enrichment_enabled = false, the untouched default), NO benchmark_runs
// row is ever inserted for it — the gate must be reachable from
// BuildQualificationTick's own wiring, not merely provable through the
// test-only seam the other tests in this file drive.
func TestBuildQualificationTick_HonoursTheRealEnrichmentGate(t *testing.T) {
	db := testControlDB(t)
	kr := testKeyring(t)
	seedLiveChatOffering(t, db, "acct-1", "prov-1", "m-1")

	run, err := BuildQualificationTick(db, kr, nil)
	if err != nil {
		t.Fatalf("BuildQualificationTick: %v", err)
	}
	if err := run(context.Background()); err != nil {
		t.Fatalf("run(): %v", err)
	}

	// canonicalModelIDForProviderModel: "m-1" above is the PROVIDER's own
	// model id (seedLiveChatOffering's own naming), never the internal
	// canonical models.id benchmark_runs.model_id actually stores — using
	// the wrong id here would make this assertion pass unconditionally,
	// regardless of whether the gate fired.
	modelID := canonicalModelIDForProviderModel(t, db, "m-1")
	var n int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM benchmark_runs WHERE model_id = ?`, modelID).Scan(&n); err != nil {
		t.Fatalf("count benchmark_runs: %v", err)
	}
	if n != 0 {
		t.Fatalf("benchmark_runs rows for %q = %d, want 0 — enrichment_enabled is false on a fresh install, and BuildQualificationTick's own real composition must honour that, not just what a test can override", modelID, n)
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

// TestQualificationTick_SuspendedCapabilityNeverReselectedEvenPastTheCooldown
// isolates dueCapabilityProbes' own `c.status = 'certified'` filter from
// qualificationCapabilityProbeCooldown (whole-branch review, FIX 3's own
// pinning requirement). The treadmill test above runs its second round on
// the real clock, mere milliseconds after the first — well within the
// 1-hour capability-probe cooldown — so it cannot tell the two mechanisms
// apart: deleting the status filter from dueCapabilityProbes' query
// entirely still leaves that test green, because the cooldown alone
// happens to block the second attempt too. This test advances the tick's
// own clock past the cooldown before the second round, so ONLY the status
// filter can be what stops reselection.
func TestQualificationTick_SuspendedCapabilityNeverReselectedEvenPastTheCooldown(t *testing.T) {
	db := testControlDB(t)
	seedCertifiedDeclaredCapability(t, db, "acct-1", "prov-1", "m-1", "tools")

	now := time.Now()
	firstTransport := &fakeProbeTransport{available: true, result: intelligence.ProbeResult{HTTPStatus: 400, ProviderCode: "tool_use_not_supported"}}
	firstTick := newQualificationTickForTest(t, db, withCapabilityProbeTransport(firstTransport), withNow(func() time.Time { return now }))
	if err := firstTick(context.Background()); err != nil {
		t.Fatalf("round 1: %v", err)
	}
	if got := firstTransport.callCount(); got != 1 {
		t.Fatalf("transport calls after round 1 = %d, want 1", got)
	}
	if state, _ := certificationOf(t, db, "acct-1", "m-1", "tools"); state != "suspended" {
		t.Fatalf("setup: certification state = %q, want \"suspended\"", state)
	}

	later := now.Add(2 * qualificationCapabilityProbeCooldown) // past the cooldown too
	secondTransport := &fakeProbeTransport{available: true, result: intelligence.ProbeResult{HTTPStatus: 400, ProviderCode: "tool_use_not_supported"}}
	secondTick := newQualificationTickForTest(t, db, withCapabilityProbeTransport(secondTransport), withNow(func() time.Time { return later }))
	if err := secondTick(context.Background()); err != nil {
		t.Fatalf("round 2: %v", err)
	}
	if got := secondTransport.callCount(); got != 0 {
		t.Fatalf("transport calls after round 2 (run PAST the cooldown window) = %d, want 0 — dueCapabilityProbes' own `c.status = 'certified'` filter, not merely the cooldown, must be what keeps a suspended row unreachable", got)
	}
}

// TestQualificationTick_DefinitiveNegativeSurvivesReDrainAndDeclaredRecertification
// is the whole-branch review's FIX 3 (Critical): the certified -> suspended
// move above does NOT, by itself, stop the certification from being
// silently reversed within the very same 30-second scheduler round.
// Ticks run sequentially in registration order (boot.go): probe_drain runs
// BEFORE model_qualification's own next round, and its ReviewDrainer
// ReProbes ANY suspended/expired/observed row (edge 8: suspended ->
// probing, resetting Truth to unknown) with no memory of WHY it was
// suspended. model_usability's declared-capability step then runs
// ListNonChatOperationsToCertify, which selects any non-chat row merely
// sitting in `probing` whose operation is declared in the offering's
// capabilities_json — a purely static fact — and certifyDeclaredCapabilities
// blindly re-certifies it supported, with a fresh certified_at, from
// nothing but that stale declaration. Every candidate a definitive-negative
// probe can ever suspend is, by construction, already in capabilities_json
// (that is how it became certified/supported in the first place), so
// re-certification was GUARANTEED, not incidental — laundering the exact
// evidence the suspend above exists to act on.
//
// This test drives the REAL production paths for both other ticks — never
// a hand-rolled substitute — exactly as boot.go composes them:
// BuildSchedulerWorkers.DrainTick (the same call boot.go's "probe_drain"
// tick makes) for the drain, and the same
// ListNonChatOperationsToCertify + certifyDeclaredCapabilities pair
// usability_assembler.go's verifier calls for the declared-capability step.
func TestQualificationTick_DefinitiveNegativeSurvivesReDrainAndDeclaredRecertification(t *testing.T) {
	db := testControlDB(t)
	seedCertifiedDeclaredCapability(t, db, "acct-1", "prov-1", "m-1", "tools")

	// Round 1 (model_qualification): a definitive semantic rejection
	// suspends the capability — unchanged behaviour from fix round 2.
	transport := &fakeProbeTransport{available: true, result: intelligence.ProbeResult{HTTPStatus: 400, ProviderCode: "tool_use_not_supported"}}
	tick := newQualificationTickForTest(t, db, withCapabilityProbeTransport(transport))
	if err := tick(context.Background()); err != nil {
		t.Fatalf("qualification tick: %v", err)
	}
	if state, truth := certificationOf(t, db, "acct-1", "m-1", "tools"); state != "suspended" || truth != "supported" {
		t.Fatalf("setup: certification = (%q, %q), want (suspended, supported)", state, truth)
	}

	// probe_drain: the REAL ReviewDrainer, built exactly as boot.go builds
	// it. A suspended row's own ReProbe edge (8) is a LEGITIMATE thing for
	// the drain to keep doing — this must still happen.
	_, probeWorkers, err := BuildSchedulerWorkers(db, "test-owner", time.Now, newOAuthTransactionID)
	if err != nil {
		t.Fatalf("BuildSchedulerWorkers: %v", err)
	}
	if _, err := probeWorkers.DrainTick(context.Background()); err != nil {
		t.Fatalf("DrainTick: %v", err)
	}
	if state, truth := certificationOf(t, db, "acct-1", "m-1", "tools"); state != "probing" || truth != "unknown" {
		t.Fatalf("after probe_drain, certification = (%q, %q), want (probing, unknown) — the drain's own re-probe edge is legitimate and must still run", state, truth)
	}

	// model_usability's declared-capability step: the REAL functions, called
	// exactly as usability_assembler.go's verifier calls them.
	certRepo := storage.NewCertificationRepo(db, nil)
	certAuditor := newCertificationAuditorAdapter(newAuditEmitter(db, nil))
	driver, err := intelligence.NewCertificationDriver(certRepo, certAuditor, intelligence.DefaultProbeRetryBudget, nil)
	if err != nil {
		t.Fatalf("NewCertificationDriver: %v", err)
	}
	catalog := storage.NewCatalogRepo(db)
	declaredRows, err := catalog.ListNonChatOperationsToCertify(context.Background(), "acct-1")
	if err != nil {
		t.Fatalf("ListNonChatOperationsToCertify: %v", err)
	}
	caps := make([]declaredCapability, len(declaredRows))
	for i, r := range declaredRows {
		caps[i] = declaredCapability{OfferingOperationID: r.OfferingOperationID, Operation: r.Operation}
	}
	certified := certifyDeclaredCapabilities(context.Background(), driver, caps)

	if certified != 0 {
		t.Fatalf("certifyDeclaredCapabilities certified %d rows, want 0 — a capability a probe just DEFINITIVELY disproved must never be blindly re-certified from its declaration alone", certified)
	}
	if state, truth := certificationOf(t, db, "acct-1", "m-1", "tools"); state != "probing" || truth != "unknown" {
		t.Fatalf("certification after the declared-capability pass = (%q, %q), want STILL (probing, unknown) — the definitive negative must survive both probe_drain and declared re-certification within the same round", state, truth)
	}
}

// TestQualificationTick_NonContradictionSuspensionStillReCertifiesFromDeclaration
// is FIX 3's own required negative-control: the fix above must not break
// the drain's LEGITIMATE job of resurrecting a row suspended for a reason
// that says nothing about whether the capability is actually supported
// (here, a credential rejection) — once the drain moves it back to
// probing, the declared-capability step must still be able to re-certify
// it from declaration exactly as before this fix.
func TestQualificationTick_NonContradictionSuspensionStillReCertifiesFromDeclaration(t *testing.T) {
	db := testControlDB(t)
	seedCertifiedDeclaredCapability(t, db, "acct-1", "prov-1", "m-1", "tools")

	certRepo := storage.NewCertificationRepo(db, nil)
	certAuditor := newCertificationAuditorAdapter(newAuditEmitter(db, nil))
	driver, err := intelligence.NewCertificationDriver(certRepo, certAuditor, intelligence.DefaultProbeRetryBudget, nil)
	if err != nil {
		t.Fatalf("NewCertificationDriver: %v", err)
	}
	opID := offeringOperationIDFor(t, db, "acct-1", "m-1", "tools")
	if _, err := driver.Suspend(context.Background(), opID, intelligence.SuspensionCredentialBlocked); err != nil {
		t.Fatalf("Suspend: %v", err)
	}

	_, probeWorkers, err := BuildSchedulerWorkers(db, "test-owner", time.Now, newOAuthTransactionID)
	if err != nil {
		t.Fatalf("BuildSchedulerWorkers: %v", err)
	}
	if _, err := probeWorkers.DrainTick(context.Background()); err != nil {
		t.Fatalf("DrainTick: %v", err)
	}

	catalog := storage.NewCatalogRepo(db)
	declaredRows, err := catalog.ListNonChatOperationsToCertify(context.Background(), "acct-1")
	if err != nil {
		t.Fatalf("ListNonChatOperationsToCertify: %v", err)
	}
	caps := make([]declaredCapability, len(declaredRows))
	for i, r := range declaredRows {
		caps[i] = declaredCapability{OfferingOperationID: r.OfferingOperationID, Operation: r.Operation}
	}
	certified := certifyDeclaredCapabilities(context.Background(), driver, caps)
	if certified != 1 {
		t.Fatalf("certifyDeclaredCapabilities certified %d rows, want 1 — a credential-related suspension (which says nothing about whether the capability is actually supported) must still be re-certifiable from declaration once the drain moves it back to probing", certified)
	}
	if state, truth := certificationOf(t, db, "acct-1", "m-1", "tools"); state != "certified" || truth != "supported" {
		t.Fatalf("certification = (%q, %q), want (certified, supported)", state, truth)
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

// --- Task 4, fix round 2: the identical loop closed on the capability path ---

// TestQualificationTick_CapabilityProbeTransportErrorRecordsAnAttemptAndIsNotRetriedNextRound
// is fix round 2's CRITICAL test, shape 1: a TRANSPORT failure (not a guard
// refusal — intelligence.RefusalOf(err) is false) happens AFTER
// guard.Admit already succeeded and ReserveProbe already reserved this
// attempt's cost. Before this fix, probeOneCapability recorded nothing for
// this path (byte-identical to before fix round 1's comment-only edit), so
// qualificationCapabilityProbeCooldown never had a probe_runs row to find
// (CapabilityProbeCooldownUntil hits sql.ErrNoRows and returns (nil, nil)),
// Admit's own "until != nil" cooldown gate was a silent no-op, and the SAME
// candidate was due again in 30 seconds, indefinitely, with a fresh
// reservation every round — a live loop, not a dormant one, per the
// reviewer's own trace through this file, CapabilityProbeCooldownUntil, and
// Admit's capability-cooldown gate.
func TestQualificationTick_CapabilityProbeTransportErrorRecordsAnAttemptAndIsNotRetriedNextRound(t *testing.T) {
	db := testControlDB(t)
	seedCertifiedDeclaredCapability(t, db, "acct-1", "prov-1", "m-1", "tools")

	transport := &fakeProbeTransport{available: true, err: fmt.Errorf("boom: capability transport unavailable")}
	tick := newQualificationTickForTest(t, db, withCapabilityProbeTransport(transport))

	if err := tick(context.Background()); err != nil {
		t.Fatalf("round 1: %v", err)
	}
	if got := transport.callCount(); got != 1 {
		t.Fatalf("transport calls after round 1 = %d, want 1", got)
	}

	opID := offeringOperationIDFor(t, db, "acct-1", "m-1", "tools")
	if n := probeRunCount(t, db, opID); n != 1 {
		t.Fatalf("probe_runs rows = %d, want 1 — Admit already reserved this attempt's cost before the transport failed; the attempt must be recorded so the cooldown engages", n)
	}
	if got := probeRunExecutionOf(t, db, opID, "tools"); got != "terminal_failure" {
		t.Fatalf("probe_runs.execution = %q, want terminal_failure", got)
	}
	if n := probeRunCostRowCount(t, db); n == 0 {
		t.Fatalf("probe_run_costs rows = %d, want > 0 — the reservation Admit already made must be visible to ProbeSpendSince", n)
	}
	state, truth := certificationOf(t, db, "acct-1", "m-1", "tools")
	if state != "certified" || truth != "supported" {
		t.Fatalf("certification = %s/%s, want certified/supported unchanged — a transport error must never flip a capability's truth", state, truth)
	}

	if err := tick(context.Background()); err != nil {
		t.Fatalf("round 2: %v", err)
	}
	if got := transport.callCount(); got != 1 {
		t.Fatalf("transport calls after round 2 = %d, want STILL 1 — a transport-error attempt must not be retried on the very next round", got)
	}
}

// TestQualificationTick_RefusedCapabilityProbeIsNotRetriedNextRoundEither is
// fix round 2's shape 2 test: the OTHER half of the three-way split — a
// guard REFUSAL — must, across MULTIPLE rounds, keep recording nothing at
// all (no accumulating probe_runs/probe_run_costs rows), since nothing is
// ever reserved for a refused attempt in the first place. This is not the
// same claim as "the transport is never retried because a cooldown
// engaged" (there is no cooldown to engage here, and none is needed): a
// refusal costs nothing to repeat, unlike a transport failure, which is
// exactly why the three-way split treats them differently.
func TestQualificationTick_RefusedCapabilityProbeIsNotRetriedNextRoundEither(t *testing.T) {
	db := testControlDB(t)
	seedCertifiedDeclaredCapability(t, db, "acct-1", "prov-1", "m-1", "tools")

	policy := intelligence.DefaultProbeSafetyPolicy()
	policy.PerProbe = nil // every allocation now has "no cap configured" -> refused

	transport := &fakeProbeTransport{available: true, result: intelligence.ProbeResult{HTTPStatus: 200, Witness: intelligence.WitnessToolCall}}
	tick := newQualificationTickForTest(t, db, withCapabilityProbeTransport(transport), withProbeGuardPolicy(policy))

	for round := 1; round <= 2; round++ {
		_ = tick(context.Background())
		if got := transport.callCount(); got != 0 {
			t.Fatalf("round %d: capability probe transport calls = %d, want 0 — the guard must refuse before the transport is ever reached", round, got)
		}
	}

	opID := offeringOperationIDFor(t, db, "acct-1", "m-1", "tools")
	if n := probeRunCount(t, db, opID); n != 0 {
		t.Fatalf("probe_runs rows = %d, want 0 across both rounds — a refused attempt must record no evidence at all, ever", n)
	}
	if n := probeRunCostRowCount(t, db); n != 0 {
		t.Fatalf("probe_run_costs rows = %d, want 0 across both rounds — a refused attempt must record no spend, ever", n)
	}
}

// TestQualificationTick_PreAdmitCapabilityErrorRecordsNoSpendAndIsUnreachableByReselection
// is fix round 2's shape 3 test — THE trap the reviewer named: a pre-Admit
// error (CapabilityFixture/RequiredWitness fail for an operation outside
// tools/structured_output/vision) must record NOTHING, exactly like a
// refusal, and NOT like a transport failure. A verbatim copy of
// runContextProbe's two-way intelligence.RefusalOf split cannot tell this
// case apart from a genuine transport failure (neither pre-Admit function
// returns a *ProbeRefusedError), so it would wrongly record a
// terminal_failure row and cost allocations for an attempt where
// guard.Admit — and therefore ReserveProbe — was never even called.
//
// probeOneCapability is called DIRECTLY with Operation: models.OperationChat
// (never a fixture-supported operation) rather than through a full tick()
// round: dueCapabilityProbes' own selection (capabilityProbeOperations,
// exactly {tools, structured_output, vision} — pinned against silent
// widening by TestQualificationTick_SelectionNeverIncludesChatOrContextWindow)
// can never select chat in the first place, so this pre-Admit path has no
// reachable path to a 30-second re-selection loop to bound: nothing ever
// selects it, so nothing ever re-selects it either. This test proves the
// METHOD's own discrimination is correct in isolation; the selection-level
// unreachability is proved by the pre-existing test named above.
func TestQualificationTick_PreAdmitCapabilityErrorRecordsNoSpendAndIsUnreachableByReselection(t *testing.T) {
	db := testControlDB(t)
	seedCertifiedDeclaredCapability(t, db, "acct-1", "prov-1", "m-1", "tools")
	opID := offeringOperationIDFor(t, db, "acct-1", "m-1", "tools")

	transport := &fakeProbeTransport{available: true, result: intelligence.ProbeResult{HTTPStatus: 200, Witness: intelligence.WitnessToolCall}}
	tick := buildQualificationTickForTest(t, db, withCapabilityProbeTransport(transport))

	err := tick.probeOneCapability(context.Background(), capabilityProbeCandidate{
		OfferingOperationID: opID, Operation: models.OperationChat,
		AccountID: "acct-1", ProviderID: "prov-1", ProviderModelID: "m-1",
	})
	if err == nil {
		t.Fatal("probeOneCapability(chat) err = nil, want ErrNoCapabilityFixture — chat has no capability fixture")
	}
	if !errors.Is(err, intelligence.ErrNoCapabilityFixture) {
		t.Fatalf("probeOneCapability(chat) err = %v, want it to wrap intelligence.ErrNoCapabilityFixture", err)
	}

	if got := transport.callCount(); got != 0 {
		t.Fatalf("capability probe transport calls = %d, want 0 — a pre-Admit error must never reach the transport", got)
	}
	if n := probeRunCount(t, db, opID); n != 0 {
		t.Fatalf("probe_runs rows = %d, want 0 — guard.Admit was never called, so ReserveProbe never reserved anything to record", n)
	}
	if n := probeRunCostRowCount(t, db); n != 0 {
		t.Fatalf("probe_run_costs rows = %d, want 0 — a pre-Admit error must never be recorded as spend", n)
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
//
// wantCalls is a LITERAL (whole-branch review, MINOR — the SAME tautology
// TestQualificationTick_CapsHowManyContextProbesOneRoundRuns's own doc
// comment already names for the context-probe cap, never actually applied
// here): comparing production's own output against production's own
// constant can only ever catch the cap slice being removed entirely, never
// the constant being changed to a different (still enforced) wrong value.
// 5 is qualificationCapabilityProbeCap's actual value as of this test,
// hand-pinned here independently.
func TestQualificationTick_CapsHowManyCapabilityProbesOneRoundRuns(t *testing.T) {
	const wantCalls = 5

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

	if got := transport.callCount(); got != wantCalls {
		t.Fatalf("capability probe transport calls = %d, want %d — the per-round cap must bound the fan-out", got, wantCalls)
	}
}

// TestQualificationTick_CoolingDownCapabilityNeverConsumesTheRoundCap is
// FIX 5 (Important, whole-branch review): dueCapabilityProbes' selection
// (oo.id ASC, shrinking only on SUCCESS) used to let a candidate that is
// currently COOLING DOWN — an attempt was already recorded for it within
// qualificationCapabilityProbeCooldown — still occupy one of
// qualificationCapabilityProbeCap's slots, even though probeOneCapability
// was guaranteed to refuse it a moment later (ProbeGuard.
// WithCapabilityCooldown). Sorting first among the due candidates, it
// would consume the SAME slot every round, forever, crowding out
// candidates further down the list that could actually be attempted. This
// test seeds cap+1 candidates, marks the one that sorts FIRST as already
// cooling down, and proves the round spends its cap on the OTHER
// candidates instead of wasting a slot re-selecting the blocked one.
func TestQualificationTick_CoolingDownCapabilityNeverConsumesTheRoundCap(t *testing.T) {
	// wantCalls is a LITERAL, never qualificationCapabilityProbeCap
	// re-read (whole-branch review, MINOR — the same tautology named
	// elsewhere in this file): comparing production's own output against
	// production's own constant can only ever catch the cap slice being
	// removed entirely. 5 is the constant's actual value as of this test,
	// hand-pinned independently.
	const wantCalls = 5

	db := testControlDB(t)
	total := qualificationCapabilityProbeCap + 1
	for i := 0; i < total; i++ {
		suffix := fmt.Sprintf("%03d", i)
		seedCertifiedDeclaredCapability(t, db, "acct-cd-"+suffix, "prov-cd-"+suffix, "m-cd-"+suffix, "tools")
	}

	rows, err := db.Conn().Query(`SELECT id, account_id, provider_model_id FROM offering_operations WHERE operation = 'tools' ORDER BY id ASC`)
	if err != nil {
		t.Fatalf("query offering_operation ids: %v", err)
	}
	type opRow struct{ id, accountID, providerModelID string }
	var opRows []opRow
	for rows.Next() {
		var r opRow
		if err := rows.Scan(&r.id, &r.accountID, &r.providerModelID); err != nil {
			t.Fatalf("scan: %v", err)
		}
		opRows = append(opRows, r)
	}
	_ = rows.Close()
	if len(opRows) != total {
		t.Fatalf("setup: seeded %d offering-operations, found %d", total, len(opRows))
	}
	coolingDown := opRows[0] // dueCapabilityProbes' own ORDER BY oo.id ASC

	// Seed a genuine (non-succeeded) attempt via the REAL ProbeRunRepo.Start/
	// Finish write path — never a raw INSERT — moments ago, well inside
	// qualificationCapabilityProbeCooldown.
	probeRuns := storage.NewProbeRunRepo(db, nil, intelligence.DefaultProbeSafetyPolicy().ContextProbeCooldown)
	if err := probeRuns.Start(context.Background(), storage.ProbeRunParams{
		ID: "run-cooling-down", OfferingOperationID: coolingDown.id, AccountID: coolingDown.accountID,
		ProviderID: "prov-cd-000", Operation: "tools", Class: intelligence.ProbeStandard, StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed cooling-down probe run: %v", err)
	}
	if err := probeRuns.Finish(context.Background(), "run-cooling-down", intelligence.ProbeInconclusive, time.Now()); err != nil {
		t.Fatalf("finish cooling-down probe run: %v", err)
	}

	transport := &fakeProbeTransport{available: true, result: intelligence.ProbeResult{HTTPStatus: 200, Witness: intelligence.WitnessToolCall}}
	tick := newQualificationTickForTest(t, db, withCapabilityProbeTransport(transport))
	if err := tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if hasSucceededProbeRun(t, db, coolingDown.accountID, coolingDown.providerModelID, "tools") {
		t.Fatal("the cooling-down candidate was probed this round, want it excluded from selection entirely")
	}
	if got := transport.callCount(); got != wantCalls {
		t.Fatalf("transport calls = %d, want %d — the cap must be spent on candidates that can actually be attempted this round, not wasted re-selecting the cooling-down one", got, wantCalls)
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

// --- Task 4: measure the context window for offerings no catalog covers ---

// harmlessStream is withStream wired to a fixed, always-OK sample — every
// context-probe fixture below (seedLiveChatOfferingNoContext) is, by
// construction, ALSO a live chat offering the sibling performance-scoring
// pass (dueModels/measureOne) selects (a live chat op is exactly what BOTH
// passes require), so every test that does not itself override the stream
// needs this to keep that unrelated pass from panicking on a nil stream
// function — mirrors TestQualificationTick_NeverProbesChatOrContextWindow's
// own identical need.
func harmlessStream() qualificationTickOption {
	return withStream(func(context.Context, string, string, string, string, int) (benchmarkSample, error) {
		return benchmarkSample{OK: true, TTFT: 100 * time.Millisecond, TokensPerSec: 40}, nil
	})
}

// probeRunExecutionOf reads the most recent probe_runs.execution for
// (offeringOperationID, operation), straight from the table — what
// production actually persisted.
func probeRunExecutionOf(t *testing.T, db *storage.DB, offeringOperationID, operation string) string {
	t.Helper()
	var execution string
	if err := db.Conn().QueryRow(
		`SELECT execution FROM probe_runs WHERE offering_operation_id = ? AND operation = ? ORDER BY started_at DESC LIMIT 1`,
		offeringOperationID, operation,
	).Scan(&execution); err != nil {
		t.Fatalf("read probe run execution for (%q,%q): %v", offeringOperationID, operation, err)
	}
	return execution
}

// TestQualificationTick_MeasuresContextOnlyWhenTheCatalogDidNot is task-4's
// Step 1 test verbatim (the automatic-model-qualification plan's own task-4
// brief): a model with NO catalog-declared context (and no prior native
// fact either) must be measured; a model the catalog already described
// must NOT be re-measured — the context probe declares 3,000,000 input
// tokens and is the most expensive probe in the system, so re-measuring a
// catalogued model would spend real quota to learn a number the dataset
// already gave for free. This is the exact cline-pass/qwen3.8-max scenario
// the task brief names: absent from models.dev, reading "ctx unknown"
// forever without this pass.
//
// withContextProbeCap(10) (fix round 1, MINOR 1) keeps
// qualificationContextProbeCap's own production value of 1 from truncating
// this fixture's 2 candidates down to 1 before the "known" candidate's
// filter is even applied. Without this, removing the "context is nil"
// filter (the brief's own prescribed mutation) makes "known" (acct-1) sort
// before "unknown" (acct-2) and get admitted by the cap FIRST, so the test
// fails on the FIRST assertion ("unknown" never probed) rather than the
// second one (the mutation's own point: "known" must never be re-probed) —
// the reviewer had to raise the cap to reach the intended assertion at all.
func TestQualificationTick_MeasuresContextOnlyWhenTheCatalogDidNot(t *testing.T) {
	db := testControlDB(t)
	seedLiveChatOffering(t, db, "acct-1", "prov-1", "known")            // context_length set (8192)
	seedLiveChatOfferingNoContext(t, db, "acct-2", "prov-2", "unknown") // no catalog fact at all

	var probed []string
	tick := newQualificationTickForTest(t, db,
		withContextProbe(func(_ context.Context, modelID string) (int, bool) {
			probed = append(probed, modelID)
			return 200000, true
		}),
		harmlessStream(),
		withContextProbeCap(10),
	)
	if err := tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if !slices.Contains(probed, "unknown") {
		t.Fatal("an uncatalogued model was never measured; it would read ctx unknown forever")
	}
	if slices.Contains(probed, "known") {
		t.Fatalf("a catalogued model was re-measured — the context probe declares 3,000,000 input tokens and is the most expensive probe in the system")
	}

	if got := nativeContextTokensOf(t, db, "unknown"); got == nil || *got != 200000 {
		t.Fatalf("native_context_tokens for the uncatalogued model = %v, want 200000 written back", got)
	}
	if got := nativeContextTokensOf(t, db, "known"); got != nil {
		t.Fatalf("native_context_tokens for the catalogued model = %v, want nil — it was never probed", *got)
	}
}

// TestQualificationTick_ContextProbeRunsThroughTheRealGuardAndWritesBackNativeContextTokens
// proves the REAL production path (runContextProbe), not the withContextProbe
// fake above: a genuine 4xx rejection carrying the OpenAI rung-2 phrase is
// admitted through intelligence.ProbeGuard (this task's own
// ExpensiveProbesEnabled opt-in, wired by buildQualificationTickForTest's
// default contextProbeGuardPolicy), extracted by ExtractContextLimit, and
// persisted through the SAME DiscoveryRepo.SetNativeContextTokens write-back
// probe.go's manual endpoint already uses — proof that "enabled through the
// policy, per probe" and "the existing write-back" are not merely asserted
// in a doc comment but actually wired end to end.
func TestQualificationTick_ContextProbeRunsThroughTheRealGuardAndWritesBackNativeContextTokens(t *testing.T) {
	db := testControlDB(t)
	seedLiveChatOfferingNoContext(t, db, "acct-1", "prov-1", "unknown")

	transport := &fakeProbeTransport{available: true, result: intelligence.ProbeResult{
		HTTPStatus: 400, ProviderCode: "context_length_exceeded",
		Message: "This model's maximum context length is 128000 tokens.",
	}}
	tick := newQualificationTickForTest(t, db, withContextProbeTransport(transport), harmlessStream())
	if err := tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if got := transport.callCount(); got != 1 {
		t.Fatalf("context probe transport calls = %d, want 1", got)
	}
	if got := nativeContextTokensOf(t, db, "unknown"); got == nil || *got != 128000 {
		t.Fatalf("native_context_tokens = %v, want 128000 extracted from the rejection", got)
	}

	chatOpID := offeringOperationIDFor(t, db, "acct-1", "unknown", "chat")
	if n := probeRunCount(t, db, chatOpID); n != 1 {
		t.Fatalf("probe_runs rows for the chat offering-operation = %d, want 1 — the context probe's own bookkeeping anchor is the offering's chat row (see contextProbeCandidate's own doc comment)", n)
	}
	if got := probeRunExecutionOf(t, db, chatOpID, "context_window"); got != "succeeded" {
		t.Fatalf("probe_runs.execution = %q, want \"succeeded\"", got)
	}
}

// TestQualificationTick_ContextProbeRefusedWithoutTheExpensiveOptInRecordsNoSpend
// proves the OTHER half of "enabled through the policy, per probe": WITHOUT
// this task's own ExpensiveProbesEnabled opt-in (the plain
// intelligence.DefaultProbeSafetyPolicy(), which leaves it false — every
// context probe declares Class=ProbeExpensive by construction), the SAME
// candidate is refused before the transport is ever reached, and — mirroring
// task-3's CRITICAL 2(a) fix — probeRuns.Start never runs before a
// successful Admit, so a refused attempt records neither a probe_runs row
// nor a probe_run_costs row (no spend at all).
func TestQualificationTick_ContextProbeRefusedWithoutTheExpensiveOptInRecordsNoSpend(t *testing.T) {
	db := testControlDB(t)
	seedLiveChatOfferingNoContext(t, db, "acct-1", "prov-1", "unknown")

	transport := &fakeProbeTransport{available: true, result: intelligence.ProbeResult{
		HTTPStatus: 400, ProviderCode: "context_length_exceeded",
		Message: "This model's maximum context length is 128000 tokens.",
	}}
	tick := newQualificationTickForTest(t, db,
		withContextProbeTransport(transport),
		withContextProbeGuardPolicy(intelligence.DefaultProbeSafetyPolicy()), // ExpensiveProbesEnabled stays false
		harmlessStream())
	if err := tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if got := transport.callCount(); got != 0 {
		t.Fatalf("context probe transport calls = %d, want 0 — the guard must refuse an expensive-class probe before the transport is ever reached without this task's own opt-in", got)
	}
	chatOpID := offeringOperationIDFor(t, db, "acct-1", "unknown", "chat")
	if n := probeRunCount(t, db, chatOpID); n != 0 {
		t.Fatalf("probe_runs rows = %d, want 0 — a refused attempt must record no evidence at all", n)
	}
	if n := probeRunCostRowCount(t, db); n != 0 {
		t.Fatalf("probe_run_costs rows = %d, want 0 — a refused attempt must record no spend", n)
	}
	if got := nativeContextTokensOf(t, db, "unknown"); got != nil {
		t.Fatalf("native_context_tokens = %v, want nil — a refused attempt learned nothing", *got)
	}
}

// TestQualificationTick_NonResolvingContextProbeIsNotRetriedNextRound proves
// qualificationContextProbeCooldown's own reason for existing: a 2xx
// response is classified inconclusive (contextprobe.go's own doc comment —
// "the provider ACCEPTED the oversized request... we must not invent [a
// limit]"), so intelligence.ProbeGuard's OWN context-probe cooldown
// (succeeded-only) never fires for it, and dueContextProbes' "effective
// context is nil" selection alone would re-pick this candidate on every
// subsequent round forever. This tick's own selection-level backoff must
// stop that on the very next round.
func TestQualificationTick_NonResolvingContextProbeIsNotRetriedNextRound(t *testing.T) {
	db := testControlDB(t)
	seedLiveChatOfferingNoContext(t, db, "acct-1", "prov-1", "unknown")

	transport := &fakeProbeTransport{available: true, result: intelligence.ProbeResult{HTTPStatus: 200}}
	tick := newQualificationTickForTest(t, db, withContextProbeTransport(transport), harmlessStream())

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
		t.Fatalf("transport calls after round 2 = %d, want STILL 1 — a non-resolving context probe must not be retried on the very next round", got)
	}
	if got := nativeContextTokensOf(t, db, "unknown"); got != nil {
		t.Fatalf("native_context_tokens = %v, want nil — nothing was ever extracted", *got)
	}
}

// TestQualificationTick_CapsHowManyContextProbesOneRoundRuns proves
// qualificationContextProbeCap: with more due context probes than the cap,
// only that many are actually attempted in one Run call.
//
// wantCalls is a LITERAL (fix round 1, MINOR 2), not
// qualificationContextProbeCap re-read: comparing production's own output
// against production's own constant can only ever catch the cap SLICE being
// removed entirely, never the constant being changed to a different (still
// enforced) wrong value. CORRECTION (whole-branch review): this comment
// used to claim task-2's fix round 1 "found and fixed" the identical
// tautology for qualificationPerRoundCap's own per-round-cap test — that
// claim was FALSE. Task-2's fix round 1 only fixed a DIFFERENT tautology
// (the 0..1-vs-0-100 rating SCALE guard); TestQualificationTick_
// CapsHowManyModelsOneRoundMeasures and TestQualificationTick_
// HandlesAFleetLargerThanOnePage both still compared production's output
// against qualificationPerRoundCap * benchmarkDefaultRequests directly
// until this fix hand-pinned them too. 1 is qualificationContextProbeCap's
// actual value as of this test, hand-pinned here independently.
func TestQualificationTick_CapsHowManyContextProbesOneRoundRuns(t *testing.T) {
	const wantCalls = 1

	db := testControlDB(t)
	total := wantCalls + 2
	for i := 0; i < total; i++ {
		suffix := fmt.Sprintf("%03d", i)
		seedLiveChatOfferingNoContext(t, db, "acct-ctxcap-"+suffix, "prov-ctxcap-"+suffix, "m-ctxcap-"+suffix)
	}

	transport := &fakeProbeTransport{available: true, result: intelligence.ProbeResult{
		HTTPStatus: 400, ProviderCode: "context_length_exceeded",
		Message: "This model's maximum context length is 128000 tokens.",
	}}
	tick := newQualificationTickForTest(t, db, withContextProbeTransport(transport), harmlessStream())
	if err := tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if got := transport.callCount(); got != wantCalls {
		t.Fatalf("context probe transport calls = %d, want %d — the per-round cap must bound the fan-out", got, wantCalls)
	}
}

// --- Task 4, fix round 1 ---

// certificationProbeExecutionOf reads GET /offerings/{id}/certification's
// real "probe_execution" field for offeringOperationID — the SAME read
// model (DiscoveryHandler.ServeCertification, WithProbeRuns wired exactly
// as ControlMux wires it) discovery.go's own doc comment describes. nil
// means "no probe of THIS row's own operation has ever run" (the field is
// omitempty) — fix round 1's IMPORTANT 2 fix is precisely what makes that
// statement true once a DIFFERENT operation (context_window, anchored on
// this same offering-operation id) has a probe_runs row of its own.
func certificationProbeExecutionOf(t *testing.T, db *storage.DB, offeringOperationID string) *string {
	t.Helper()
	h := NewDiscoveryHandler(nil, nil, storage.NewCatalogRepo(db), nil, nil, nil, nil, nil, nil, nil, nil).
		WithProbeRuns(storage.NewProbeRunRepo(db, nil, 7*24*time.Hour))
	mux := newTestDiscoveryMux(h)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, discoveryRequest(http.MethodGet, "/api/control/v1/offerings/"+offeringOperationID+"/certification"))
	if rec.Code != http.StatusOK {
		t.Fatalf("ServeCertification status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	var env struct {
		Data certificationJSON `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode certification: %v; body = %s", err, rec.Body.String())
	}
	return env.Data.ProbeExecution
}

// TestQualificationTick_ContextProbeTransportErrorRecordsAnAttemptAndIsNotRetriedNextRound
// is fix round 1's IMPORTANT 1 test: when cp.Run fails on the TRANSPORT
// path (not a guard refusal — intelligence.RefusalOf(err) is false), Admit
// has already reserved this attempt's cost, so the tick must record a
// terminal_failure probe_runs row (with its own probe_run_costs rows) so
// (a) hasRecentContextProbeAttempt's own selection-level cooldown engages
// on the very next round, and (b) ProbeSpendSince can see the recorded
// spend. Before this fix, a transport error recorded nothing at all, so the
// SAME candidate returned every 30 seconds forever with no probe_runs
// evidence and no visible spend to ever self-limit it.
func TestQualificationTick_ContextProbeTransportErrorRecordsAnAttemptAndIsNotRetriedNextRound(t *testing.T) {
	db := testControlDB(t)
	seedLiveChatOfferingNoContext(t, db, "acct-1", "prov-1", "unknown")

	transport := &fakeProbeTransport{available: true, err: fmt.Errorf("boom: transport unavailable")}
	tick := newQualificationTickForTest(t, db, withContextProbeTransport(transport), harmlessStream())

	if err := tick(context.Background()); err != nil {
		t.Fatalf("round 1: %v", err)
	}
	if got := transport.callCount(); got != 1 {
		t.Fatalf("transport calls after round 1 = %d, want 1", got)
	}

	chatOpID := offeringOperationIDFor(t, db, "acct-1", "unknown", "chat")
	if n := probeRunCount(t, db, chatOpID); n != 1 {
		t.Fatalf("probe_runs rows = %d, want 1 — Admit already reserved this attempt's cost before the transport failed; the attempt must be recorded so the cooldown engages", n)
	}
	if got := probeRunExecutionOf(t, db, chatOpID, "context_window"); got != "terminal_failure" {
		t.Fatalf("probe_runs.execution = %q, want terminal_failure", got)
	}
	// probeEstimateAllocations(OperationContextWindow) emits one row per
	// quota.Unit (requests/concurrency/input_tokens/output_tokens) for a
	// SINGLE probe_runs row — the same multi-row shape the success path
	// already writes — so this asserts "something was recorded", not a
	// specific count that would just re-derive quota.Estimate's own unit
	// count.
	if n := probeRunCostRowCount(t, db); n == 0 {
		t.Fatalf("probe_run_costs rows = %d, want > 0 — the reservation Admit already made must be visible to ProbeSpendSince, or the PerAccount cap can never self-limit a persistently failing provider", n)
	}
	if got := nativeContextTokensOf(t, db, "unknown"); got != nil {
		t.Fatalf("native_context_tokens = %v, want nil — nothing was ever extracted", *got)
	}

	if err := tick(context.Background()); err != nil {
		t.Fatalf("round 2: %v", err)
	}
	if got := transport.callCount(); got != 1 {
		t.Fatalf("transport calls after round 2 = %d, want STILL 1 — a transport-error attempt must not be retried on the very next round", got)
	}
}

// TestQualificationTick_ContextProbeExecutionNeverSurfacesAsTheChatCertifications
// is fix round 1's IMPORTANT 2 test: a context probe is anchored on the
// offering's CHAT offering_operation row id (contextProbeCandidate's own
// doc comment), so GET /offerings/{chatOpID}/certification — the CHAT
// capability's own certification read — must NEVER report the context
// probe's own execution as if it belonged to chat. Before fix round 1,
// storage.ProbeRunRepo.LatestExecution carried no operation filter, so a
// rate-limited context probe would render a certified, supported, working
// chat capability as if a probe had just failed against it.
func TestQualificationTick_ContextProbeExecutionNeverSurfacesAsTheChatCertifications(t *testing.T) {
	db := testControlDB(t)
	seedLiveChatOfferingNoContext(t, db, "acct-1", "prov-1", "unknown")

	transport := &fakeProbeTransport{available: true, result: intelligence.ProbeResult{HTTPStatus: 429}}
	tick := newQualificationTickForTest(t, db, withContextProbeTransport(transport), harmlessStream())
	if err := tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	chatOpID := offeringOperationIDFor(t, db, "acct-1", "unknown", "chat")
	if got := probeRunExecutionOf(t, db, chatOpID, "context_window"); got != "retryable_failure" {
		t.Fatalf("setup: probe_runs.execution for context_window = %q, want retryable_failure — the context probe really did run and record something on this row id", got)
	}

	if got := certificationProbeExecutionOf(t, db, chatOpID); got != nil {
		t.Fatalf("GET /offerings/%s/certification probe_execution = %q, want nil — a context probe's own execution must never surface as the CHAT certification's probe result", chatOpID, *got)
	}
}

// --- Whole-branch review, FIX 4: the two owner-consent gates ---------------

// TestQualificationTick_EnrichmentDisabledSkipsThePerformanceScoringPass is
// FIX 4a's own test: enrichment_enabled is FALSE on a fresh install
// (storage.DefaultSettingsRow), and ServeBenchmark (benchmark.go) already
// refuses to run real inference unless the owner has explicitly flipped it
// on. Before this fix, this tick never consulted settings at all — a fresh
// install would start spending the owner's real inference quota through the
// identical dispatch path 30 seconds after boot, with no button and no
// setting able to stop it. withSettings wires the REAL storage.SettingsRepo
// (never a fake); the row is left untouched (the fresh-install default), so
// stream must NEVER be called.
func TestQualificationTick_EnrichmentDisabledSkipsThePerformanceScoringPass(t *testing.T) {
	db := testControlDB(t)
	seedLiveChatOffering(t, db, "acct-1", "prov-1", "m-1")

	calls := 0
	stream := func(context.Context, string, string, string, string, int) (benchmarkSample, error) {
		calls++
		return benchmarkSample{OK: true, TTFT: 100 * time.Millisecond, TokensPerSec: 40}, nil
	}
	tick := newQualificationTickForTest(t, db, withStream(stream), withSettings(db))
	if err := tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if calls != 0 {
		t.Fatalf("stream was called %d times, want 0 — enrichment_enabled is false on a fresh install and the performance-scoring pass must not spend real inference quota without it", calls)
	}
	if got := qualityRatingOf(t, db, "m-1"); got != nil {
		t.Fatalf("models.quality_rating = %v, want nil — nothing was measured", got)
	}
}

// TestQualificationTick_EnrichmentEnabledStillRunsThePerformanceScoringPass
// is the positive control for the test above: once the owner has
// explicitly flipped enrichment_enabled on (through the REAL
// storage.SettingsRepo write path, never a raw INSERT), the
// performance-scoring pass must run exactly as it always has.
func TestQualificationTick_EnrichmentEnabledStillRunsThePerformanceScoringPass(t *testing.T) {
	db := testControlDB(t)
	seedLiveChatOffering(t, db, "acct-1", "prov-1", "m-1")
	putOperationalSettings(t, db, true, false)

	calls := 0
	stream := func(context.Context, string, string, string, string, int) (benchmarkSample, error) {
		calls++
		return benchmarkSample{OK: true, TTFT: 100 * time.Millisecond, TokensPerSec: 40}, nil
	}
	tick := newQualificationTickForTest(t, db, withStream(stream), withSettings(db))
	if err := tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if calls == 0 {
		t.Fatal("stream was never called, want at least one call — enrichment_enabled is true, so the performance-scoring pass must run")
	}
	if got := qualityRatingOf(t, db, "m-1"); got == nil {
		t.Fatal("models.quality_rating = nil, want a measured rating")
	}
}

// TestQualificationTick_ProbeExpensiveDisabledRefusesTheRealContextProbeGuard
// is FIX 4b's own test for the context-probe half: probe_expensive_enabled
// is FALSE on a fresh install (04 §2: opt-in), and before this fix
// BuildQualificationTick force-flipped it true on a copy of the default
// policy regardless of what the owner actually configured. withSettings
// wires the REAL resolver over an untouched (fresh-install-default) row, so
// the context probe's OWN guard — not a hand-set contextProbeGuardPolicy
// field — must refuse it before the transport is ever reached.
func TestQualificationTick_ProbeExpensiveDisabledRefusesTheRealContextProbeGuard(t *testing.T) {
	db := testControlDB(t)
	seedLiveChatOfferingNoContext(t, db, "acct-1", "prov-1", "unknown")

	transport := &fakeProbeTransport{available: true, result: intelligence.ProbeResult{HTTPStatus: 400, ProviderCode: "context_length_exceeded", Message: "maximum context length is 128000 tokens"}}
	tick := newQualificationTickForTest(t, db, withContextProbeTransport(transport), withSettings(db), harmlessStream())
	if err := tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if got := transport.callCount(); got != 0 {
		t.Fatalf("context probe transport calls = %d, want 0 — probe_expensive_enabled is false on a fresh install; the REAL settings-backed guard must refuse before the transport is ever reached", got)
	}
	if got := nativeContextTokensOf(t, db, "unknown"); got != nil {
		t.Fatalf("native_context_tokens = %v, want nil — nothing was learned", got)
	}
}

// TestQualificationTick_ProbeExpensiveEnabledStillRunsTheRealContextProbe is
// the positive control: once the owner has explicitly flipped
// probe_expensive_enabled on, the context probe must still run through the
// REAL settings-backed guard exactly as it always has.
func TestQualificationTick_ProbeExpensiveEnabledStillRunsTheRealContextProbe(t *testing.T) {
	db := testControlDB(t)
	seedLiveChatOfferingNoContext(t, db, "acct-1", "prov-1", "unknown")
	putOperationalSettings(t, db, false, true)

	transport := &fakeProbeTransport{available: true, result: intelligence.ProbeResult{HTTPStatus: 400, ProviderCode: "context_length_exceeded", Message: "maximum context length is 128000 tokens"}}
	tick := newQualificationTickForTest(t, db, withContextProbeTransport(transport), withSettings(db), harmlessStream())
	if err := tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if got := transport.callCount(); got != 1 {
		t.Fatalf("context probe transport calls = %d, want 1 — probe_expensive_enabled is explicitly true, so the REAL settings-backed guard must admit the attempt", got)
	}
	if got := nativeContextTokensOf(t, db, "unknown"); got == nil || *got != 128000 {
		t.Fatalf("native_context_tokens = %v, want 128000", got)
	}
}

// --- Whole-branch review, FIX 6: a per-phase wall-clock budget -------------

// TestQualificationTick_CapabilityProbePhaseGetsItsOwnDeadline is FIX 6's own
// test for the capability-probe phase: the ctx that reaches the transport
// must carry a deadline roughly tickBudget from when the phase started —
// proof the phase itself is bounded, mirroring
// TestUsabilityTick_ListPhaseIsDeadlineBounded's identical
// deadline-inspection technique (usability_tick_test.go) rather than an
// actual timed-out sleep, which would be flaky.
func TestQualificationTick_CapabilityProbePhaseGetsItsOwnDeadline(t *testing.T) {
	db := testControlDB(t)
	seedCertifiedDeclaredCapability(t, db, "acct-1", "prov-1", "m-1", "tools")

	const budget = 3 * time.Second
	transport := &fakeProbeTransport{available: true, result: intelligence.ProbeResult{HTTPStatus: 200, Witness: intelligence.WitnessToolCall}}
	tick := newQualificationTickForTest(t, db, withCapabilityProbeTransport(transport), withTickBudget(budget))

	start := time.Now()
	if err := tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	end := time.Now()

	dl, ok := transport.deadline()
	if !ok {
		t.Fatal("capability-probe transport ran with no deadline — the phase must be bounded (FIX 6)")
	}
	if dl.Before(start.Add(budget)) || dl.After(end.Add(budget)) {
		t.Fatalf("capability-probe deadline = %v, want one %v budget from an instant in [%v, %v]", dl, budget, start, end)
	}
}

// TestQualificationTick_ContextProbePhaseGetsItsOwnDeadline is the context-
// probe phase's own counterpart.
func TestQualificationTick_ContextProbePhaseGetsItsOwnDeadline(t *testing.T) {
	db := testControlDB(t)
	seedLiveChatOfferingNoContext(t, db, "acct-1", "prov-1", "unknown")

	const budget = 3 * time.Second
	transport := &fakeProbeTransport{available: true, result: intelligence.ProbeResult{HTTPStatus: 400, ProviderCode: "context_length_exceeded", Message: "maximum context length is 128000 tokens"}}
	tick := newQualificationTickForTest(t, db, withContextProbeTransport(transport), withTickBudget(budget), harmlessStream())

	start := time.Now()
	if err := tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	end := time.Now()

	dl, ok := transport.deadline()
	if !ok {
		t.Fatal("context-probe transport ran with no deadline — the phase must be bounded (FIX 6)")
	}
	if dl.Before(start.Add(budget)) || dl.After(end.Add(budget)) {
		t.Fatalf("context-probe deadline = %v, want one %v budget from an instant in [%v, %v]", dl, budget, start, end)
	}
}

// TestQualificationTick_PerformanceScoringPhaseGetsItsOwnDeadline is the
// performance-scoring (benchmark) phase's own counterpart.
func TestQualificationTick_PerformanceScoringPhaseGetsItsOwnDeadline(t *testing.T) {
	db := testControlDB(t)
	seedLiveChatOffering(t, db, "acct-1", "prov-1", "m-1")
	putOperationalSettings(t, db, true, false)

	const budget = 3 * time.Second
	var (
		mu   sync.Mutex
		dl   time.Time
		hasD bool
	)
	stream := func(ctx context.Context, _, _, _, _ string, _ int) (benchmarkSample, error) {
		d, ok := ctx.Deadline()
		mu.Lock()
		dl, hasD = d, ok
		mu.Unlock()
		return benchmarkSample{OK: true, TTFT: 10 * time.Millisecond, TokensPerSec: 40}, nil
	}
	tick := newQualificationTickForTest(t, db, withStream(stream), withSettings(db), withTickBudget(budget))

	start := time.Now()
	if err := tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	end := time.Now()

	mu.Lock()
	defer mu.Unlock()
	if !hasD {
		t.Fatal("performance-scoring pass ran with no deadline — the phase must be bounded (FIX 6)")
	}
	if dl.Before(start.Add(budget)) || dl.After(end.Add(budget)) {
		t.Fatalf("performance-scoring deadline = %v, want one %v budget from an instant in [%v, %v]", dl, budget, start, end)
	}
}

// TestQualificationTick_ContextProbePhaseGetsAFreshBudgetAfterCapabilityPhase
// pins that the capability-probe phase's deadline is RELEASED before the
// context-probe phase starts — mirroring
// TestUsabilityTick_LanesGetAFullBudgetAfterTheListPhase's identical
// "a later phase must get a full, independent budget rather than an
// earlier phase's leftover" proof. The SAME fake transport instance backs
// BOTH phases here (as it does in production — one probeTransportAdapter),
// so ctxAt(0)/ctxAt(1) isolate each phase's own call.
func TestQualificationTick_ContextProbePhaseGetsAFreshBudgetAfterCapabilityPhase(t *testing.T) {
	db := testControlDB(t)
	seedCertifiedDeclaredCapability(t, db, "acct-cap", "prov-cap", "m-cap", "tools")
	seedLiveChatOfferingNoContext(t, db, "acct-ctx", "prov-ctx", "unknown")

	const (
		budget   = 1 * time.Second
		capBurns = budget / 2
	)
	var sawFirstCall int32
	transport := &fakeProbeTransport{
		available: true,
		result:    intelligence.ProbeResult{HTTPStatus: 200, Witness: intelligence.WitnessToolCall},
		// Sleep ONLY on the capability phase's own call, never the context
		// phase's — a second sleep there would eat into the very deadline
		// this test measures, confounding the result with wall-clock
		// elapsed AFTER the ctx (a fixed instant) was already captured.
		onProbe: func() {
			if atomic.CompareAndSwapInt32(&sawFirstCall, 0, 1) {
				time.Sleep(capBurns)
			}
		},
	}
	tick := newQualificationTickForTest(t, db, withCapabilityProbeTransport(transport), withTickBudget(budget), harmlessStream())
	if err := tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if got := transport.callCount(); got != 2 {
		t.Fatalf("transport calls = %d, want 2 (one capability probe, one context probe)", got)
	}
	ctxProbeCtx, ok := transport.ctxAt(1)
	if !ok {
		t.Fatal("context-probe call was never recorded")
	}
	dl, hasD := ctxProbeCtx.Deadline()
	if !hasD {
		t.Fatal("context-probe phase ran with no deadline")
	}
	// The context-probe phase's own budget starts when THAT phase starts,
	// after the capability phase already burned capBurns (half a budget)
	// sleeping inside its own onProbe hook. A phase that inherited the
	// capability phase's leftover deadline would have at most
	// budget-capBurns left; a phase with its OWN fresh budget has close to
	// the whole budget still ahead of it.
	if remaining := time.Until(dl); remaining <= budget-capBurns/2 {
		t.Fatalf("context-probe phase had %v left of a %v budget: it inherited the capability phase's deadline instead of getting its own", remaining, budget)
	}
}
