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
		ModelID       string   `json:"model_id"`
		QualityRating *float64 `json:"quality_rating"`
		Offerings     []struct {
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
	h := NewModelsHandler(f.catalog, func() time.Time { return benchNow })
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
