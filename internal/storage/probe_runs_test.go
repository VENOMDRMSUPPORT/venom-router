package storage

import (
	"context"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

// probeRunsVersion is the goose version of the probe-runs enabling-extra
// migration (00010_probe_runs.sql).
const probeRunsVersion = 10

// TestMigration00010_UpDown proves 00010 adds probe_runs/probe_run_costs,
// rolls back to exactly the pre-00010 shape (every earlier table
// survives), and re-applies. Count-agnostic: it rolls back every
// migration at or above probeRunsVersion, so a later migration lands
// without silently breaking this test (mirrors every other migrate_*_test
// in this package).
func TestMigration00010_UpDown(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	if _, err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() (up) error = %v", err)
	}
	assertTableExists(t, db, "probe_runs", true)
	assertTableExists(t, db, "probe_run_costs", true)

	for currentVersion(t, db) >= probeRunsVersion {
		if _, err := DownOne(ctx, db); err != nil {
			t.Fatalf("DownOne() error = %v", err)
		}
	}
	assertTableExists(t, db, "probe_runs", false)
	assertTableExists(t, db, "probe_run_costs", false)
	// Every earlier table must survive rolling back only 00010.
	assertTableExists(t, db, "certifications", true)
	assertTableExists(t, db, "offering_operations", true)
	assertTableExists(t, db, "quota_windows", true)
	assertTableExists(t, db, "jobs", true)
	assertBaselineTableExists(t, db, true)

	if _, err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() (re-up) error = %v", err)
	}
	assertTableExists(t, db, "probe_runs", true)
	assertTableExists(t, db, "probe_run_costs", true)
}

// probeRunsFixture bundles a migrated DB plus one seeded offering-operation
// chain, ready for probe-run tests.
type probeRunsFixture struct {
	db         *DB
	repo       *ProbeRunRepo
	accountID  string
	providerID string
	opID       string
	clock      func() time.Time
}

func newProbeRunsFixture(t *testing.T, clock func() time.Time, cooldown time.Duration, seed string) *probeRunsFixture {
	t.Helper()
	db := migratedCatalogDB(t)
	opID := seedOfferingOperationChain(t, db, "acct-"+seed, "prov-"+seed, "model-"+seed, "pm-"+seed)
	return &probeRunsFixture{
		db:         db,
		repo:       NewProbeRunRepo(db, clock, cooldown),
		accountID:  "acct-" + seed,
		providerID: "prov-" + seed,
		opID:       opID,
		clock:      clock,
	}
}

func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func countProbeRuns(t *testing.T, db *DB, id string) int {
	t.Helper()
	var n int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM probe_runs WHERE id = ?`, id).Scan(&n); err != nil {
		t.Fatalf("count probe_runs(%q): %v", id, err)
	}
	return n
}

// TestProbeRunRepo_StartIsAtomic proves a params set whose costs collide
// (a duplicate Unit, violating probe_run_costs' PRIMARY KEY) leaves NO
// probe_runs row behind either — the run insert and its cost inserts
// commit or roll back together.
func TestProbeRunRepo_StartIsAtomic(t *testing.T) {
	now := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	f := newProbeRunsFixture(t, fixedClock(now), 7*24*time.Hour, "atomic")
	ctx := context.Background()

	err := f.repo.Start(ctx, ProbeRunParams{
		ID:                  "run-atomic-1",
		OfferingOperationID: f.opID,
		AccountID:           f.accountID,
		ProviderID:          f.providerID,
		Operation:           "tools",
		Class:               intelligence.ProbeStandard,
		Allocations: []quota.Allocation{
			{Unit: quota.UnitRequests, Cost: 1},
			{Unit: quota.UnitRequests, Cost: 1}, // duplicate unit -> PK violation
		},
		StartedAt: now,
	})
	if err == nil {
		t.Fatalf("Start with duplicate-unit allocations succeeded, want an error")
	}
	if got := countProbeRuns(t, f.db, "run-atomic-1"); got != 0 {
		t.Fatalf("probe_runs rows for %q after failed Start = %d, want 0 (atomic rollback)", "run-atomic-1", got)
	}
}

// TestProbeRunRepo_SpendSumsPerUnitAndRespectsSince proves ProbeSpendSince
// sums per-unit costs only over runs with started_at >= since, in
// deterministic (unit-sorted) order.
func TestProbeRunRepo_SpendSumsPerUnitAndRespectsSince(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	f := newProbeRunsFixture(t, fixedClock(now), 7*24*time.Hour, "spend")
	ctx := context.Background()
	since := now.Add(-1 * time.Hour)

	// Inside the window.
	mustStart(t, f, "run-spend-in-1", now.Add(-30*time.Minute), []quota.Allocation{
		{Unit: quota.UnitRequests, Cost: 1},
		{Unit: quota.UnitInputTokens, Cost: 100},
	})
	mustStart(t, f, "run-spend-in-2", now.Add(-10*time.Minute), []quota.Allocation{
		{Unit: quota.UnitRequests, Cost: 1},
		{Unit: quota.UnitInputTokens, Cost: 50},
	})
	// Before the window — must be excluded.
	mustStart(t, f, "run-spend-out", now.Add(-2*time.Hour), []quota.Allocation{
		{Unit: quota.UnitRequests, Cost: 1000},
		{Unit: quota.UnitInputTokens, Cost: 1000},
	})

	got, err := f.repo.ProbeSpendSince(ctx, f.accountID, since)
	if err != nil {
		t.Fatalf("ProbeSpendSince: %v", err)
	}
	want := map[quota.Unit]float64{quota.UnitInputTokens: 150, quota.UnitRequests: 2}
	if len(got) != 2 {
		t.Fatalf("got %d allocations, want 2: %+v", len(got), got)
	}
	// SQL ORDER BY unit ASC: "input_tokens" < "requests" lexically.
	if got[0].Unit != quota.UnitInputTokens || got[0].Cost != 150 {
		t.Errorf("got[0] = %+v, want {input_tokens 150}", got[0])
	}
	if got[1].Unit != quota.UnitRequests || got[1].Cost != 2 {
		t.Errorf("got[1] = %+v, want {requests 2}", got[1])
	}
	for _, a := range got {
		if a.Cost != want[a.Unit] {
			t.Errorf("unit %q sum = %v, want %v", a.Unit, a.Cost, want[a.Unit])
		}
	}
}

// TestProbeRunRepo_InFlightCountsOnlyUnfinished proves InFlightProbes
// counts only running/pending rows for the given provider, excluding
// finished rows and another provider's running row.
func TestProbeRunRepo_InFlightCountsOnlyUnfinished(t *testing.T) {
	now := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	f := newProbeRunsFixture(t, fixedClock(now), 7*24*time.Hour, "inflight")
	ctx := context.Background()

	mustStart(t, f, "run-if-running", now, nil)
	mustStart(t, f, "run-if-finished", now, nil)
	if err := f.repo.Finish(ctx, "run-if-finished", intelligence.ProbeSucceeded, now); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	// A different provider's running run must not count toward this
	// provider's in-flight total.
	otherOpID := seedOfferingOperationChain(t, f.db, "acct-inflight-other", "prov-inflight-other", "model-inflight-other", "pm-inflight-other")
	if err := f.repo.Start(ctx, ProbeRunParams{
		ID: "run-if-other-provider", OfferingOperationID: otherOpID,
		AccountID: "acct-inflight-other", ProviderID: "prov-inflight-other",
		Operation: "tools", Class: intelligence.ProbeStandard, StartedAt: now,
	}); err != nil {
		t.Fatalf("start other-provider run: %v", err)
	}

	got, err := f.repo.InFlightProbes(ctx, f.providerID)
	if err != nil {
		t.Fatalf("InFlightProbes: %v", err)
	}
	if got != 1 {
		t.Fatalf("InFlightProbes = %d, want 1 (only run-if-running)", got)
	}
}

// TestProbeRunRepo_CooldownOnlySucceededContextProbe proves the cooldown
// is set ONLY by a succeeded context_window probe: a retryable/
// inconclusive/terminal context-window run yields nil, a succeeded
// `tools` run yields nil, and the most recent succeeded context-window
// run wins when several exist.
func TestProbeRunRepo_CooldownOnlySucceededContextProbe(t *testing.T) {
	now := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	window := 7 * 24 * time.Hour
	f := newProbeRunsFixture(t, fixedClock(now), window, "cooldown")
	ctx := context.Background()

	// No runs yet -> nil.
	until, err := f.repo.ProbeCooldownUntil(ctx, f.opID)
	if err != nil {
		t.Fatalf("ProbeCooldownUntil (no runs): %v", err)
	}
	if until != nil {
		t.Fatalf("cooldown with no runs = %v, want nil", until)
	}

	// A retryable failure on context_window must NOT set the cooldown.
	startCtxProbe(t, f, "run-cd-retryable", now.Add(-3*time.Hour), intelligence.ProbeRetryableFailure)
	until, err = f.repo.ProbeCooldownUntil(ctx, f.opID)
	if err != nil {
		t.Fatalf("ProbeCooldownUntil (retryable): %v", err)
	}
	if until != nil {
		t.Fatalf("cooldown after retryable_failure context probe = %v, want nil", until)
	}

	// A succeeded `tools` probe must NOT set the context-probe cooldown.
	if err := f.repo.Start(ctx, ProbeRunParams{
		ID: "run-cd-tools", OfferingOperationID: f.opID, AccountID: f.accountID, ProviderID: f.providerID,
		Operation: "tools", Class: intelligence.ProbeStandard, StartedAt: now.Add(-2 * time.Hour),
	}); err != nil {
		t.Fatalf("start tools run: %v", err)
	}
	if err := f.repo.Finish(ctx, "run-cd-tools", intelligence.ProbeSucceeded, now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("finish tools run: %v", err)
	}
	until, err = f.repo.ProbeCooldownUntil(ctx, f.opID)
	if err != nil {
		t.Fatalf("ProbeCooldownUntil (tools succeeded): %v", err)
	}
	if until != nil {
		t.Fatalf("cooldown after succeeded tools probe = %v, want nil", until)
	}

	// An older succeeded context probe, then a newer one — the newer must
	// win.
	olderStart := now.Add(-6 * time.Hour)
	startCtxProbe(t, f, "run-cd-older", olderStart, intelligence.ProbeSucceeded)
	newerStart := now.Add(-1 * time.Hour)
	startCtxProbe(t, f, "run-cd-newer", newerStart, intelligence.ProbeSucceeded)

	until, err = f.repo.ProbeCooldownUntil(ctx, f.opID)
	if err != nil {
		t.Fatalf("ProbeCooldownUntil (succeeded): %v", err)
	}
	if until == nil {
		t.Fatalf("cooldown after succeeded context probe = nil, want set")
	}
	want := newerStart.UTC().Add(window)
	if !until.Equal(want) {
		t.Fatalf("cooldown until = %v, want %v (the NEWER succeeded run)", until, want)
	}
}

// TestProbeRunRepo_CooldownUsesInjectedWindowNotAConstant proves the
// cooldown duration actually comes from NewProbeRunRepo's
// contextProbeCooldown parameter, not a value baked into the repo: two
// repos built with DIFFERENT windows over runs recorded at the SAME
// instant must report two DIFFERENT cooldown deadlines, each matching
// its own repo's configured window (not intelligence.ProbeSafetyPolicy's
// 7-day default, which neither repo here is configured with).
func TestProbeRunRepo_CooldownUsesInjectedWindowNotAConstant(t *testing.T) {
	now := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	shortWindow := 1 * time.Hour
	longWindow := 30 * 24 * time.Hour

	db := migratedCatalogDB(t)
	opID := seedOfferingOperationChain(t, db, "acct-window", "prov-window", "model-window", "pm-window")
	shortRepo := NewProbeRunRepo(db, fixedClock(now), shortWindow)
	longRepo := NewProbeRunRepo(db, fixedClock(now), longWindow)

	ctx := context.Background()
	if err := shortRepo.Start(ctx, ProbeRunParams{
		ID: "run-window", OfferingOperationID: opID, AccountID: "acct-window", ProviderID: "prov-window",
		Operation: "context_window", Class: intelligence.ProbeExpensive, StartedAt: now,
	}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := shortRepo.Finish(ctx, "run-window", intelligence.ProbeSucceeded, now); err != nil {
		t.Fatalf("finish: %v", err)
	}

	gotShort, err := shortRepo.ProbeCooldownUntil(ctx, opID)
	if err != nil {
		t.Fatalf("ProbeCooldownUntil (short): %v", err)
	}
	if gotShort == nil || !gotShort.Equal(now.Add(shortWindow)) {
		t.Fatalf("short-window cooldown = %v, want %v", gotShort, now.Add(shortWindow))
	}

	gotLong, err := longRepo.ProbeCooldownUntil(ctx, opID)
	if err != nil {
		t.Fatalf("ProbeCooldownUntil (long): %v", err)
	}
	if gotLong == nil || !gotLong.Equal(now.Add(longWindow)) {
		t.Fatalf("long-window cooldown = %v, want %v (must use ITS OWN injected window, not a shared constant)", gotLong, now.Add(longWindow))
	}
}

// TestProbeRunRepo_FinishIsIdempotent proves a second Finish call is a
// no-op and never overwrites the first terminal value.
func TestProbeRunRepo_FinishIsIdempotent(t *testing.T) {
	now := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	f := newProbeRunsFixture(t, fixedClock(now), 7*24*time.Hour, "idempotent")
	ctx := context.Background()

	mustStart(t, f, "run-idem", now, nil)
	if err := f.repo.Finish(ctx, "run-idem", intelligence.ProbeSucceeded, now); err != nil {
		t.Fatalf("first Finish: %v", err)
	}
	// A second Finish with a DIFFERENT execution must not overwrite the
	// first terminal value.
	if err := f.repo.Finish(ctx, "run-idem", intelligence.ProbeTerminalFailure, now.Add(time.Hour)); err != nil {
		t.Fatalf("second Finish: %v, want no error (idempotent no-op)", err)
	}

	var execution string
	var finishedAt int64
	if err := f.db.Conn().QueryRow(`SELECT execution, finished_at FROM probe_runs WHERE id = ?`, "run-idem").Scan(&execution, &finishedAt); err != nil {
		t.Fatalf("query after second Finish: %v", err)
	}
	if execution != string(intelligence.ProbeSucceeded) {
		t.Fatalf("execution after second Finish = %q, want %q (first terminal value preserved)", execution, intelligence.ProbeSucceeded)
	}
	if finishedAt != now.Unix() {
		t.Fatalf("finished_at after second Finish = %d, want %d (first stamp preserved)", finishedAt, now.Unix())
	}
}

// TestProbeRunRepo_ReclaimStale proves a stale running run is reclaimed
// (terminal_failure, stops counting as in-flight) while a fresh running
// run is untouched.
func TestProbeRunRepo_ReclaimStale(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	f := newProbeRunsFixture(t, fixedClock(now), 7*24*time.Hour, "reclaim")
	ctx := context.Background()

	mustStart(t, f, "run-reclaim-stale", now.Add(-2*time.Hour), nil)
	mustStart(t, f, "run-reclaim-fresh", now.Add(-1*time.Minute), nil)

	cutoff := now.Add(-30 * time.Minute)
	n, err := f.repo.ReclaimStale(ctx, cutoff)
	if err != nil {
		t.Fatalf("ReclaimStale: %v", err)
	}
	if n != 1 {
		t.Fatalf("ReclaimStale reclaimed = %d, want 1", n)
	}

	var staleExecution string
	if err := f.db.Conn().QueryRow(`SELECT execution FROM probe_runs WHERE id = ?`, "run-reclaim-stale").Scan(&staleExecution); err != nil {
		t.Fatalf("query stale run: %v", err)
	}
	if staleExecution != string(intelligence.ProbeTerminalFailure) {
		t.Fatalf("stale run execution = %q, want %q", staleExecution, intelligence.ProbeTerminalFailure)
	}

	var freshExecution string
	if err := f.db.Conn().QueryRow(`SELECT execution FROM probe_runs WHERE id = ?`, "run-reclaim-fresh").Scan(&freshExecution); err != nil {
		t.Fatalf("query fresh run: %v", err)
	}
	if freshExecution != string(intelligence.ProbeRunning) {
		t.Fatalf("fresh run execution = %q, want %q (untouched)", freshExecution, intelligence.ProbeRunning)
	}

	inFlight, err := f.repo.InFlightProbes(ctx, f.providerID)
	if err != nil {
		t.Fatalf("InFlightProbes: %v", err)
	}
	if inFlight != 1 {
		t.Fatalf("InFlightProbes after reclaim = %d, want 1 (only the fresh run)", inFlight)
	}
}

// TestProbeRunRepo_SucceededOfferingOperationIDs proves the batched
// task-5 provenance lookup: given a set of offering_operation_ids, it
// returns exactly the ones with at least one SUCCEEDED probe_runs row —
// one with a succeeded run, one with only a failed run, one with no run at
// all, and one that is not even in the requested set (must never appear).
// It also proves an empty input returns an empty, non-nil map.
func TestProbeRunRepo_SucceededOfferingOperationIDs(t *testing.T) {
	now := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	db := migratedCatalogDB(t)
	repo := NewProbeRunRepo(db, fixedClock(now), 7*24*time.Hour)
	ctx := context.Background()

	succeededOpID := seedOfferingOperationChain(t, db, "acct-succ", "prov-succ", "model-succ", "pm-succ")
	failedOpID := seedOfferingOperationChain(t, db, "acct-failed", "prov-failed", "model-failed", "pm-failed")
	noRunOpID := seedOfferingOperationChain(t, db, "acct-norun", "prov-norun", "model-norun", "pm-norun")
	notRequestedOpID := seedOfferingOperationChain(t, db, "acct-unreq", "prov-unreq", "model-unreq", "pm-unreq")

	if err := repo.Start(ctx, ProbeRunParams{
		ID: "run-succ", OfferingOperationID: succeededOpID, AccountID: "acct-succ", ProviderID: "prov-succ",
		Operation: "tools", Class: intelligence.ProbeStandard, StartedAt: now,
	}); err != nil {
		t.Fatalf("start succeeded run: %v", err)
	}
	if err := repo.Finish(ctx, "run-succ", intelligence.ProbeSucceeded, now); err != nil {
		t.Fatalf("finish succeeded run: %v", err)
	}

	if err := repo.Start(ctx, ProbeRunParams{
		ID: "run-failed", OfferingOperationID: failedOpID, AccountID: "acct-failed", ProviderID: "prov-failed",
		Operation: "tools", Class: intelligence.ProbeStandard, StartedAt: now,
	}); err != nil {
		t.Fatalf("start failed run: %v", err)
	}
	if err := repo.Finish(ctx, "run-failed", intelligence.ProbeTerminalFailure, now); err != nil {
		t.Fatalf("finish failed run: %v", err)
	}

	// A succeeded run exists for notRequestedOpID too, but it must never
	// surface because it is never passed in the requested id list below.
	if err := repo.Start(ctx, ProbeRunParams{
		ID: "run-unrequested", OfferingOperationID: notRequestedOpID, AccountID: "acct-unreq", ProviderID: "prov-unreq",
		Operation: "tools", Class: intelligence.ProbeStandard, StartedAt: now,
	}); err != nil {
		t.Fatalf("start unrequested run: %v", err)
	}
	if err := repo.Finish(ctx, "run-unrequested", intelligence.ProbeSucceeded, now); err != nil {
		t.Fatalf("finish unrequested run: %v", err)
	}

	got, err := repo.SucceededOfferingOperationIDs(ctx, []string{succeededOpID, failedOpID, noRunOpID})
	if err != nil {
		t.Fatalf("SucceededOfferingOperationIDs: %v", err)
	}
	if !got[succeededOpID] {
		t.Errorf("succeededOpID missing from result: %+v", got)
	}
	if got[failedOpID] {
		t.Errorf("failedOpID present in result, want absent (only a terminal_failure run exists): %+v", got)
	}
	if got[noRunOpID] {
		t.Errorf("noRunOpID present in result, want absent (no probe_runs row exists): %+v", got)
	}
	if got[notRequestedOpID] {
		t.Errorf("notRequestedOpID present in result despite never being requested: %+v", got)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want exactly 1 (%+v)", len(got), got)
	}

	empty, err := repo.SucceededOfferingOperationIDs(ctx, nil)
	if err != nil {
		t.Fatalf("SucceededOfferingOperationIDs(nil): %v", err)
	}
	if empty == nil {
		t.Fatalf("SucceededOfferingOperationIDs(nil) = nil map, want a non-nil empty map")
	}
	if len(empty) != 0 {
		t.Fatalf("SucceededOfferingOperationIDs(nil) = %+v, want empty", empty)
	}
}

// TestProbeRunRepo_SatisfiesIntelligencePorts is a compile-time-adjacent
// runtime check that ProbeRunRepo's methods are actually callable through
// the three intelligence port interfaces it claims to implement (the
// var _ assertions in probe_runs.go already prove this at compile time;
// this test additionally proves the assignment is reachable at runtime
// through a real *ProbeRunRepo value).
func TestProbeRunRepo_SatisfiesIntelligencePorts(t *testing.T) {
	f := newProbeRunsFixture(t, fixedClock(time.Now()), 7*24*time.Hour, "ports")
	var (
		_ intelligence.ProbeSpendReader    = f.repo
		_ intelligence.ProbeInFlightReader = f.repo
		_ intelligence.ProbeCooldownReader = f.repo
	)
}

// mustStart is a test-only helper wrapping Start with a fresh run id and
// clock-independent StartedAt, failing the test on error. allocations may
// be nil (no cost rows).
func mustStart(t *testing.T, f *probeRunsFixture, id string, startedAt time.Time, allocations []quota.Allocation) {
	t.Helper()
	if err := f.repo.Start(context.Background(), ProbeRunParams{
		ID:                  id,
		OfferingOperationID: f.opID,
		AccountID:           f.accountID,
		ProviderID:          f.providerID,
		Operation:           "tools",
		Class:               intelligence.ProbeStandard,
		Allocations:         allocations,
		StartedAt:           startedAt,
	}); err != nil {
		t.Fatalf("Start(%q): %v", id, err)
	}
}

// startCtxProbe starts a context_window-operation run and immediately
// finishes it with the given execution outcome.
func startCtxProbe(t *testing.T, f *probeRunsFixture, id string, startedAt time.Time, execution intelligence.ProbeExecution) {
	t.Helper()
	ctx := context.Background()
	if err := f.repo.Start(ctx, ProbeRunParams{
		ID:                  id,
		OfferingOperationID: f.opID,
		AccountID:           f.accountID,
		ProviderID:          f.providerID,
		Operation:           "context_window",
		Class:               intelligence.ProbeExpensive,
		StartedAt:           startedAt,
	}); err != nil {
		t.Fatalf("start context probe %q: %v", id, err)
	}
	if err := f.repo.Finish(ctx, id, execution, startedAt); err != nil {
		t.Fatalf("finish context probe %q: %v", id, err)
	}
}
