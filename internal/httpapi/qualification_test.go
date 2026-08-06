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
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/observability"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// newQualificationTickForTest builds a qualificationTick directly (bypassing
// BuildQualificationTick's production credential/dispatcher composition) so
// a test can inject a fake benchmarkStreamFn and drive real-clock freshness
// logic without any network I/O — the same shape as benchmark_test.go
// injecting a fake stream into NewBenchmarkHandler.
func newQualificationTickForTest(t *testing.T, db *storage.DB, stream benchmarkStreamFn) func(context.Context) error {
	t.Helper()
	tick := &qualificationTick{
		catalog: storage.NewCatalogRepo(db),
		runs:    storage.NewBenchmarkRunRepo(db, nil),
		stream:  stream,
		newID:   newOAuthTransactionID,
		now:     time.Now,
		log:     observability.Default(),
	}
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

// TestQualificationTick_ScoresAModelThatHasNeverBeenMeasured is the task-2
// brief's Step 1 test verbatim: a model with a live chat offering and NO
// prior benchmark_runs row must be measured, and models.quality_rating must
// land on the 0-100 COLUMN scale (never the raw 0..1 measurement) — with the
// dashboard trigger gone, this tick is the only writer left.
func TestQualificationTick_ScoresAModelThatHasNeverBeenMeasured(t *testing.T) {
	db := testControlDB(t)
	seedLiveChatOffering(t, db, "acct-1", "prov-1", "m-1")

	var streamed int
	tick := newQualificationTickForTest(t, db, func(context.Context, string, string, string, string, int) (benchmarkSample, error) {
		streamed++
		return benchmarkSample{OK: true, TTFT: 200 * time.Millisecond, TokensPerSec: 40}, nil
	})

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
	if *rating <= 0 || *rating > 100 {
		t.Fatalf("quality_rating = %v, want the 0-100 column scale", *rating)
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
	tick := newQualificationTickForTest(t, db, func(context.Context, string, string, string, string, int) (benchmarkSample, error) {
		streamed++
		return benchmarkSample{OK: true}, nil
	})
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
	tick := newQualificationTickForTest(t, db, func(context.Context, string, string, string, string, int) (benchmarkSample, error) {
		streamed++
		return benchmarkSample{OK: true, TTFT: 100 * time.Millisecond, TokensPerSec: 50}, nil
	})
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
	tick := newQualificationTickForTest(t, db, func(context.Context, string, string, string, string, int) (benchmarkSample, error) {
		streamed++
		return benchmarkSample{OK: true}, nil
	})
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
	tick := newQualificationTickForTest(t, db, func(context.Context, string, string, string, string, int) (benchmarkSample, error) {
		calls++
		if calls == 2 {
			return benchmarkSample{OK: false}, nil
		}
		return benchmarkSample{OK: true, TTFT: 100 * time.Millisecond, TokensPerSec: 40}, nil
	})
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
	tick := newQualificationTickForTest(t, db, func(context.Context, string, string, string, string, int) (benchmarkSample, error) {
		streamed++
		return benchmarkSample{OK: true, TTFT: 100 * time.Millisecond, TokensPerSec: 40}, nil
	})
	if err := tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	wantSamples := qualificationPerRoundCap * benchmarkDefaultRequests
	if streamed != wantSamples {
		t.Fatalf("streamed %d times, want %d (%d models * %d requests) — the per-round cap must bound the fan-out",
			streamed, wantSamples, qualificationPerRoundCap, benchmarkDefaultRequests)
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
