package storage

import (
	"context"
	"testing"
	"time"
)

// benchmarkRunsFixture bundles a migrated DB plus one seeded model row,
// ready for benchmark-run tests. account_id/provider_id/provider_model_id
// carry no FK on benchmark_runs (00017_benchmark_runs.sql) — only model_id
// does — so those three are plain caller-chosen strings here.
type benchmarkRunsFixture struct {
	db      *DB
	repo    *BenchmarkRunRepo
	modelID string
}

func newBenchmarkRunsFixture(t *testing.T, now func() time.Time, seed string) *benchmarkRunsFixture {
	t.Helper()
	db := migratedCatalogDB(t)
	modelID := insertModel(t, db, "model-"+seed, seed+"-canonical")
	return &benchmarkRunsFixture{
		db:      db,
		repo:    NewBenchmarkRunRepo(db, now),
		modelID: modelID,
	}
}

// TestBenchmarkRunRepo_InsertAndLatestForModel_NilNullables proves a run
// inserted with every nullable field nil round-trips through
// LatestForModel with those fields still nil (never a zero-valued pointer
// standing in for "absent").
func TestBenchmarkRunRepo_InsertAndLatestForModel_NilNullables(t *testing.T) {
	now := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	f := newBenchmarkRunsFixture(t, fixedClock(now), "nilcase")
	ctx := context.Background()

	started := now.Add(-time.Minute)
	finished := now
	run := BenchmarkRun{
		ID:              "run-nil-1",
		ModelID:         f.modelID,
		AccountID:       "acct-1",
		ProviderID:      "prov-1",
		ProviderModelID: "pm-1",
		Requests:        5,
		Successes:       0,
		TTFTMillis:      nil,
		TokensPerSec:    nil,
		Rating:          nil,
		StartedAt:       started,
		FinishedAt:      finished,
	}
	if err := f.repo.Insert(ctx, run); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, ok, err := f.repo.LatestForModel(ctx, f.modelID)
	if err != nil {
		t.Fatalf("LatestForModel: %v", err)
	}
	if !ok {
		t.Fatalf("LatestForModel ok = false, want true")
	}
	if got.ID != run.ID {
		t.Errorf("ID = %q, want %q", got.ID, run.ID)
	}
	if got.ModelID != run.ModelID || got.AccountID != run.AccountID || got.ProviderID != run.ProviderID || got.ProviderModelID != run.ProviderModelID {
		t.Errorf("identity fields = %+v, want to match %+v", got, run)
	}
	if got.Requests != run.Requests || got.Successes != run.Successes {
		t.Errorf("Requests/Successes = %d/%d, want %d/%d", got.Requests, got.Successes, run.Requests, run.Successes)
	}
	if got.TTFTMillis != nil {
		t.Errorf("TTFTMillis = %v, want nil", got.TTFTMillis)
	}
	if got.TokensPerSec != nil {
		t.Errorf("TokensPerSec = %v, want nil", got.TokensPerSec)
	}
	if got.Rating != nil {
		t.Errorf("Rating = %v, want nil", got.Rating)
	}
	if !got.StartedAt.Equal(started) {
		t.Errorf("StartedAt = %v, want %v", got.StartedAt, started)
	}
	if !got.FinishedAt.Equal(finished) {
		t.Errorf("FinishedAt = %v, want %v", got.FinishedAt, finished)
	}
}

// TestBenchmarkRunRepo_InsertAndLatestForModel_NonNilNullables proves the
// same round-trip with every nullable field SET, and that the values come
// back exactly (not just "non-nil").
func TestBenchmarkRunRepo_InsertAndLatestForModel_NonNilNullables(t *testing.T) {
	now := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	f := newBenchmarkRunsFixture(t, fixedClock(now), "nonnilcase")
	ctx := context.Background()

	ttft := int64(842)
	tps := 37.5
	rating := 0.91
	started := now.Add(-2 * time.Minute)
	finished := now.Add(-1 * time.Minute)
	run := BenchmarkRun{
		ID:              "run-nonnil-1",
		ModelID:         f.modelID,
		AccountID:       "acct-2",
		ProviderID:      "prov-2",
		ProviderModelID: "pm-2",
		Requests:        10,
		Successes:       9,
		TTFTMillis:      &ttft,
		TokensPerSec:    &tps,
		Rating:          &rating,
		StartedAt:       started,
		FinishedAt:      finished,
	}
	if err := f.repo.Insert(ctx, run); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, ok, err := f.repo.LatestForModel(ctx, f.modelID)
	if err != nil {
		t.Fatalf("LatestForModel: %v", err)
	}
	if !ok {
		t.Fatalf("LatestForModel ok = false, want true")
	}
	if got.TTFTMillis == nil || *got.TTFTMillis != ttft {
		t.Errorf("TTFTMillis = %v, want %d", got.TTFTMillis, ttft)
	}
	if got.TokensPerSec == nil || *got.TokensPerSec != tps {
		t.Errorf("TokensPerSec = %v, want %v", got.TokensPerSec, tps)
	}
	if got.Rating == nil || *got.Rating != rating {
		t.Errorf("Rating = %v, want %v", got.Rating, rating)
	}
}

// TestBenchmarkRunRepo_LatestForModel_NewestByFinishedAt proves
// LatestForModel picks the row with the greatest finished_at, regardless
// of insertion order.
func TestBenchmarkRunRepo_LatestForModel_NewestByFinishedAt(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	f := newBenchmarkRunsFixture(t, fixedClock(now), "ordering")
	ctx := context.Background()

	older := BenchmarkRun{
		ID:              "run-older",
		ModelID:         f.modelID,
		AccountID:       "acct-3",
		ProviderID:      "prov-3",
		ProviderModelID: "pm-3",
		Requests:        1,
		Successes:       1,
		StartedAt:       now.Add(-3 * time.Hour),
		FinishedAt:      now.Add(-2 * time.Hour),
	}
	newer := BenchmarkRun{
		ID:              "run-newer",
		ModelID:         f.modelID,
		AccountID:       "acct-3",
		ProviderID:      "prov-3",
		ProviderModelID: "pm-3",
		Requests:        2,
		Successes:       2,
		StartedAt:       now.Add(-time.Hour),
		FinishedAt:      now.Add(-30 * time.Minute),
	}

	// Insert the NEWER run first to prove ordering comes from finished_at,
	// not insertion order.
	if err := f.repo.Insert(ctx, newer); err != nil {
		t.Fatalf("Insert(newer): %v", err)
	}
	if err := f.repo.Insert(ctx, older); err != nil {
		t.Fatalf("Insert(older): %v", err)
	}

	got, ok, err := f.repo.LatestForModel(ctx, f.modelID)
	if err != nil {
		t.Fatalf("LatestForModel: %v", err)
	}
	if !ok {
		t.Fatalf("LatestForModel ok = false, want true")
	}
	if got.ID != newer.ID {
		t.Fatalf("LatestForModel ID = %q, want %q (the newer finished_at)", got.ID, newer.ID)
	}
}

// TestBenchmarkRunRepo_LatestForModel_UnknownModel proves LatestForModel
// returns the documented zero-value/false/nil triple for a model id with
// no benchmark_runs rows at all.
func TestBenchmarkRunRepo_LatestForModel_UnknownModel(t *testing.T) {
	f := newBenchmarkRunsFixture(t, fixedClock(time.Now()), "unknown")
	ctx := context.Background()

	got, ok, err := f.repo.LatestForModel(ctx, "model-does-not-exist")
	if err != nil {
		t.Fatalf("LatestForModel: %v", err)
	}
	if ok {
		t.Fatalf("LatestForModel ok = true, want false for an unknown model")
	}
	if got != (BenchmarkRun{}) {
		t.Fatalf("LatestForModel result = %+v, want zero value", got)
	}
}
