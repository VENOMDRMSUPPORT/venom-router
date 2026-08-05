package httpapi

// benchmark_readmodel_test.go walks ONE benchmark measurement all the way
// from the write path to what the owner's console actually reads:
//
//	runBenchmark -> models.quality_rating (SQLite) -> CatalogRepo.ListOfferings
//	  -> intelligence.Project -> GET /models' group + offering payload.
//
// It exists because the whole-branch review (2026-08-05) found the two ends
// of that walk disagreeing by a factor of 100: the write path stored
// localBenchmarkRating's raw 0..1 measurement in the 0-100
// models.quality_rating column (04 §3), so models.QualityScore's /100 turned
// a PERFECT benchmark into a 0.01 ranking score — worse than the 0.5 neutral
// score an unbenchmarked model gets — while the group header showed the raw
// 0.73. No test crossed that boundary, so nothing caught it. This one does,
// over a real migrated SQLite database and the real handlers, with every
// expected number hand-computed from localBenchmarkRating's documented
// weights.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// benchmarkReadModelEnvelope is GET /models' response, decoded to only the
// fields this walk is about.
type benchmarkReadModelEnvelope struct {
	Data []struct {
		ModelID         string   `json:"model_id"`
		QualityRating   *float64 `json:"quality_rating"`
		LatestBenchmark *struct {
			FinishedAt string `json:"finished_at"`
			Requests   int    `json:"requests"`
			Successes  int    `json:"successes"`
		} `json:"latest_benchmark"`
		Offerings []struct {
			ProviderModelID string  `json:"provider_model_id"`
			QualityScore    float64 `json:"quality_score"`
			QualityKnown    bool    `json:"quality_known"`
		} `json:"offerings"`
	} `json:"data"`
}

// serveBenchmarkReadModel drives the REAL ModelsHandler over the benchmark
// fixture's OWN database — the same rows runBenchmark just wrote, never a
// second seeded copy of them.
func serveBenchmarkReadModel(t *testing.T, f *benchmarkFixture) benchmarkReadModelEnvelope {
	t.Helper()
	h := NewModelsHandler(f.catalog, func() time.Time { return benchNow }).WithBenchmarkRuns(f.runs)
	rec := httptest.NewRecorder()
	h.ServeModels(rec, modelsRequest(http.MethodGet, "/api/control/v1/models"))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /models status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var env benchmarkReadModelEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode GET /models: %v", err)
	}
	return env
}

// TestBenchmark_RatingIsConsistentFromWritePathToReadModel is the
// cross-boundary consistency proof for finding 1. A single fully-successful
// suite is run through the real endpoint, and then BOTH surfaces the console
// renders are read back out of the real read model:
//
//	speed   = min(40/80, 1)        = 0.5
//	latency = max(0, 1 - 100/2000) = 0.95
//	rating  = 0.5*0.5 + 0.5*0.95   = 0.725
//
// so the group's quality_rating must be 72.5 (the 0-100 column) and every
// offering's quality_score must be 0.725 (models.QualityScore's rating/100)
// — the SAME measurement on its two documented scales, with quality_known
// true. Both numbers are written here by hand from the formula's weights;
// neither is read back from the implementation, and the two are additionally
// cross-checked against each other so the surfaces can never silently drift
// onto different scales again.
func TestBenchmark_RatingIsConsistentFromWritePathToReadModel(t *testing.T) {
	const wantRunRating = 0.725   // benchmark_runs.rating, 0..1
	const wantColumnRating = 72.5 // models.quality_rating, 0-100

	sample := benchmarkSample{OK: true, TTFT: 100 * time.Millisecond, TokensPerSec: 40}
	stream, _ := scriptedStream(t, []scriptedStep{{sample: sample}, {sample: sample}, {sample: sample}})

	f := newBenchmarkFixture(t, true, stream)
	seedLiveOffering(t, f.db, "acct-bench-readmodel")

	_, body := f.post(t, benchModelID)
	row := f.awaitTerminalJob(t, jobIDFrom(t, body))
	if row.Status != storage.JobCompleted {
		t.Fatalf("job status = %q (err %+v), want completed", row.Status, row.Error)
	}

	env := serveBenchmarkReadModel(t, f)
	if len(env.Data) != 1 || env.Data[0].ModelID != benchModelID {
		t.Fatalf("groups = %+v, want exactly the benchmarked model %q", env.Data, benchModelID)
	}
	group := env.Data[0]

	if group.QualityRating == nil || !floatsClose(*group.QualityRating, wantColumnRating, 1e-9) {
		t.Fatalf("group quality_rating = %v, want %v (the 0-100 column, 04 §3)", group.QualityRating, wantColumnRating)
	}
	if len(group.Offerings) != 1 {
		t.Fatalf("group offerings = %+v, want exactly one", group.Offerings)
	}
	off := group.Offerings[0]
	if !off.QualityKnown {
		t.Fatal("quality_known = false, want true — a benchmark just wrote a rating for this model")
	}
	if !floatsClose(off.QualityScore, wantRunRating, 1e-9) {
		t.Fatalf("quality_score = %v, want %v (the measured 0..1 rating)", off.QualityScore, wantRunRating)
	}
	// The two surfaces must express ONE fact. This catches a future change
	// that fixes one end and leaves the other behind, even if both happen to
	// still be inside their own valid ranges.
	if !floatsClose(off.QualityScore, *group.QualityRating/100, 1e-9) {
		t.Fatalf("quality_score %v and quality_rating %v are on inconsistent scales", off.QualityScore, *group.QualityRating)
	}
	// And it must be a GOOD score: this benchmark measured well, so the
	// routing quality factor has to beat the 0.5 neutral score an
	// unbenchmarked model receives. The pre-fix build scored 0.01 here.
	if off.QualityScore <= 0.5 {
		t.Fatalf("quality_score = %v, want > 0.5 — a well-measured benchmark must not rank below an unbenchmarked model", off.QualityScore)
	}
}

// TestBenchmark_ReadModelReportsLatestBenchmarkDate is finding 2's payload
// half: the spec (line ~205) asks a rating's provenance to read "local
// benchmark, <date>", and the shipped read model exposed no date at all, so
// the console could only ever say "Local benchmark". GET /models now reports
// the LATEST benchmark run's finished_at plus its successes/requests, which
// is also what lets a surface mark a rating that a later partial-failure run
// did not refresh.
//
// finished_at is asserted against the fixture clock (benchNow), which is what
// runBenchmark stamps the run with — not against whatever the payload
// happened to contain.
func TestBenchmark_ReadModelReportsLatestBenchmarkDate(t *testing.T) {
	sample := benchmarkSample{OK: true, TTFT: 100 * time.Millisecond, TokensPerSec: 40}
	stream, _ := scriptedStream(t, []scriptedStep{{sample: sample}, {sample: sample}, {sample: sample}})

	f := newBenchmarkFixture(t, true, stream)
	seedLiveOffering(t, f.db, "acct-bench-provenance-date")

	_, body := f.post(t, benchModelID)
	row := f.awaitTerminalJob(t, jobIDFrom(t, body))
	if row.Status != storage.JobCompleted {
		t.Fatalf("job status = %q (err %+v), want completed", row.Status, row.Error)
	}

	env := serveBenchmarkReadModel(t, f)
	if len(env.Data) != 1 {
		t.Fatalf("groups = %+v, want exactly one", env.Data)
	}
	latest := env.Data[0].LatestBenchmark
	if latest == nil {
		t.Fatal("latest_benchmark = null, want the run that just completed")
	}
	if want := benchNow.Format(time.RFC3339); latest.FinishedAt != want {
		t.Fatalf("latest_benchmark.finished_at = %q, want %q (the run's own finished_at)", latest.FinishedAt, want)
	}
	if latest.Requests != benchmarkDefaultRequests || latest.Successes != benchmarkDefaultRequests {
		t.Fatalf("latest_benchmark requests/successes = %d/%d, want %d/%d",
			latest.Requests, latest.Successes, benchmarkDefaultRequests, benchmarkDefaultRequests)
	}
}

// TestBenchmark_ReadModelReportsPartialLatestBenchmark is the STALE-RATING
// half of finding 2. A partial-failure run withholds the rating and leaves an
// older one in place; without the run's own successes/requests on the payload,
// the console would present that surviving rating as if the latest run had
// produced it. The payload must therefore report the latest run honestly —
// 2 of 3 — alongside the preserved rating.
func TestBenchmark_ReadModelReportsPartialLatestBenchmark(t *testing.T) {
	// A rating a PRIOR fully-successful run left behind, on the column's
	// documented 0-100 scale.
	const preexistingColumnRating = 64.0

	stream, _ := scriptedStream(t, []scriptedStep{
		{sample: benchmarkSample{OK: true, TTFT: 100 * time.Millisecond, TokensPerSec: 40}},
		{sample: benchmarkSample{OK: false}},
		{sample: benchmarkSample{OK: true, TTFT: 100 * time.Millisecond, TokensPerSec: 40}},
	})

	f := newBenchmarkFixture(t, true, stream)
	seedLiveOffering(t, f.db, "acct-bench-provenance-partial")
	if _, err := f.db.Conn().Exec(`UPDATE models SET quality_rating = ? WHERE id = ?`, preexistingColumnRating, benchModelID); err != nil {
		t.Fatalf("seed pre-existing rating: %v", err)
	}

	_, body := f.post(t, benchModelID)
	row := f.awaitTerminalJob(t, jobIDFrom(t, body))
	if row.Status != storage.JobCompleted {
		t.Fatalf("job status = %q (err %+v), want completed", row.Status, row.Error)
	}

	env := serveBenchmarkReadModel(t, f)
	if len(env.Data) != 1 {
		t.Fatalf("groups = %+v, want exactly one", env.Data)
	}
	group := env.Data[0]
	// The honest-withholding invariant, re-pinned through the read model.
	if group.QualityRating == nil || !floatsClose(*group.QualityRating, preexistingColumnRating, 1e-9) {
		t.Fatalf("group quality_rating = %v, want the untouched %v", group.QualityRating, preexistingColumnRating)
	}
	latest := group.LatestBenchmark
	if latest == nil {
		t.Fatal("latest_benchmark = null, want the partial run — the measurement is still evidence")
	}
	if latest.Requests != 3 || latest.Successes != 2 {
		t.Fatalf("latest_benchmark requests/successes = %d/%d, want 3/2 — the surviving rating did NOT come from this run",
			latest.Requests, latest.Successes)
	}
}

// TestBenchmark_ReadModelOmitsLatestBenchmarkWhenNeverBenchmarked proves the
// nullable half: a live model that has never been benchmarked reports
// "latest_benchmark": null, so no surface can render a fabricated date.
func TestBenchmark_ReadModelOmitsLatestBenchmarkWhenNeverBenchmarked(t *testing.T) {
	stream, calls := scriptedStream(t, nil)
	f := newBenchmarkFixture(t, true, stream)
	seedLiveOffering(t, f.db, "acct-bench-never")

	env := serveBenchmarkReadModel(t, f)
	if len(env.Data) != 1 {
		t.Fatalf("groups = %+v, want exactly one", env.Data)
	}
	if env.Data[0].LatestBenchmark != nil {
		t.Fatalf("latest_benchmark = %+v, want null — this model has never been benchmarked", env.Data[0].LatestBenchmark)
	}
	if len(*calls) != 0 {
		t.Fatalf("stream invoked %d times, want 0 — this test never triggers a benchmark", len(*calls))
	}
}

// TestControlMux_ModelsExposesLatestBenchmarkProvenance mutation-proofs the
// COMPOSITION ROOT for finding 2. The three tests above drive a ModelsHandler
// this test file wires itself, so they would all stay green if ControlMux
// forgot to call WithBenchmarkRuns and every real request served
// latest_benchmark: null. This one goes through the REAL composed mux —
// owner session, CSRF gating, ControlMux's own repos — and asserts the dated
// provenance actually reaches the wire.
func TestControlMux_ModelsExposesLatestBenchmarkProvenance(t *testing.T) {
	db := testControlDB(t)

	// Seed the model + a live offering exactly as the benchmark fixture does,
	// then write ONE benchmark_runs row through the REAL repository (never a
	// hand-built INSERT that could drift from what runBenchmark writes).
	if _, err := db.Conn().Exec(
		`INSERT INTO providers (id, display_name, auth_mode, funding_mode, funding_locked, created_at, updated_at)
		 VALUES (?, ?, 'api_key', 'owner_policy', 0, 0, 0)`, benchProvider, benchProvider,
	); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if _, err := db.Conn().Exec(
		`INSERT INTO models (id, canonical_key_sha256, display_name, quality_rating, created_at, updated_at)
		 VALUES (?, ?, 'Great Model', 72.5, 0, 0)`, benchModelID, "sha-"+benchModelID,
	); err != nil {
		t.Fatalf("seed model: %v", err)
	}
	if _, err := db.Conn().Exec(
		`INSERT INTO provider_model_aliases (provider_id, provider_model_id, model_id) VALUES (?, ?, ?)`,
		benchProvider, benchProvModel, benchModelID,
	); err != nil {
		t.Fatalf("seed alias: %v", err)
	}
	seedLiveOffering(t, db, "acct-bench-mux")

	finished := benchNow
	if err := storage.NewBenchmarkRunRepo(db, func() time.Time { return benchNow }).Insert(context.Background(), storage.BenchmarkRun{
		ID: "run-mux-1", ModelID: benchModelID, AccountID: "acct-bench-mux",
		ProviderID: benchProvider, ProviderModelID: benchProvModel,
		Requests: 3, Successes: 3,
		StartedAt: finished.Add(-time.Minute), FinishedAt: finished,
	}); err != nil {
		t.Fatalf("insert benchmark run: %v", err)
	}

	mux, cookie, _ := p3aOwnerMux(t, db)
	rec := p3aGet(t, mux, cookie, "/api/control/v1/models")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /models status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	var env benchmarkReadModelEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode GET /models: %v", err)
	}
	if len(env.Data) != 1 {
		t.Fatalf("groups = %+v, want exactly one", env.Data)
	}
	latest := env.Data[0].LatestBenchmark
	if latest == nil {
		t.Fatal("latest_benchmark = null through the real ControlMux — the composition root is not wiring the benchmark-runs repo into the read model")
	}
	if want := finished.Format(time.RFC3339); latest.FinishedAt != want {
		t.Fatalf("latest_benchmark.finished_at = %q, want %q", latest.FinishedAt, want)
	}
	if latest.Requests != 3 || latest.Successes != 3 {
		t.Fatalf("latest_benchmark requests/successes = %d/%d, want 3/3", latest.Requests, latest.Successes)
	}
}
