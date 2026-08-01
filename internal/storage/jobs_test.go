package storage

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestJobRepo_Create_InsertsPendingRow(t *testing.T) {
	db := migratedAuditJobsDB(t)
	repo := NewJobRepo(db)
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	if err := repo.Create(ctx, "job-1", "discovery", now); err != nil {
		t.Fatalf("Create: %v", err)
	}

	row, ok, err := repo.GetByID(ctx, "job-1")
	if err != nil || !ok {
		t.Fatalf("GetByID: ok=%v err=%v", ok, err)
	}
	if row.Status != JobPending {
		t.Fatalf("Status = %q, want pending", row.Status)
	}
	if row.Kind != "discovery" {
		t.Fatalf("Kind = %q, want discovery", row.Kind)
	}
	if !row.CreatedAt.Equal(now) {
		t.Fatalf("CreatedAt = %v, want %v", row.CreatedAt, now)
	}
	if row.StartedAt != nil || row.FinishedAt != nil || row.RetentionUntil != nil {
		t.Fatalf("a freshly created job has non-nil optional timestamps: %+v", row)
	}
}

func TestJobRepo_GetByID_UnknownNotOK(t *testing.T) {
	db := migratedAuditJobsDB(t)
	_, ok, err := NewJobRepo(db).GetByID(context.Background(), "does-not-exist")
	if err != nil {
		t.Fatalf("GetByID: unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("GetByID(unknown) ok = true, want false")
	}
}

func TestJobRepo_FullLifecycle_PendingRunningCompleted(t *testing.T) {
	db := migratedAuditJobsDB(t)
	repo := NewJobRepo(db)
	ctx := context.Background()
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	if err := repo.Create(ctx, "job-2", "discovery", created); err != nil {
		t.Fatalf("Create: %v", err)
	}

	startedAt := created.Add(time.Second)
	if err := repo.MarkRunning(ctx, "job-2", startedAt); err != nil {
		t.Fatalf("MarkRunning: %v", err)
	}
	row, ok, err := repo.GetByID(ctx, "job-2")
	if err != nil || !ok {
		t.Fatalf("GetByID after MarkRunning: ok=%v err=%v", ok, err)
	}
	if row.Status != JobRunning || row.StartedAt == nil || !row.StartedAt.Equal(startedAt) {
		t.Fatalf("row after MarkRunning = %+v, want running with StartedAt=%v", row, startedAt)
	}

	finishedAt := startedAt.Add(2 * time.Second)
	if err := repo.MarkTerminal(ctx, "job-2", JobCompleted, finishedAt, "account:acct-1", nil, DefaultJobRetention); err != nil {
		t.Fatalf("MarkTerminal: %v", err)
	}

	row, ok, err = repo.GetByID(ctx, "job-2")
	if err != nil || !ok {
		t.Fatalf("GetByID after MarkTerminal: ok=%v err=%v", ok, err)
	}
	if row.Status != JobCompleted {
		t.Fatalf("Status = %q, want completed", row.Status)
	}
	if row.FinishedAt == nil || !row.FinishedAt.Equal(finishedAt) {
		t.Fatalf("FinishedAt = %v, want %v", row.FinishedAt, finishedAt)
	}
	if row.ResultRef != "account:acct-1" {
		t.Fatalf("ResultRef = %q, want account:acct-1", row.ResultRef)
	}
	wantRetention := finishedAt.Add(DefaultJobRetention)
	if row.RetentionUntil == nil || !row.RetentionUntil.Equal(wantRetention) {
		t.Fatalf("RetentionUntil = %v, want %v", row.RetentionUntil, wantRetention)
	}
	if row.Error != nil {
		t.Fatalf("Error = %+v, want nil for a successful completion", row.Error)
	}
}

func TestJobRepo_MarkTerminal_Failed_StoresJobError(t *testing.T) {
	db := migratedAuditJobsDB(t)
	repo := NewJobRepo(db)
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	if err := repo.Create(ctx, "job-3", "probe", now); err != nil {
		t.Fatalf("Create: %v", err)
	}
	jobErr := &JobError{Code: "provider_unreachable", Message: "the provider did not respond"}
	if err := repo.MarkTerminal(ctx, "job-3", JobFailed, now.Add(time.Second), "", jobErr, DefaultJobRetention); err != nil {
		t.Fatalf("MarkTerminal: %v", err)
	}

	row, ok, err := repo.GetByID(ctx, "job-3")
	if err != nil || !ok {
		t.Fatalf("GetByID: ok=%v err=%v", ok, err)
	}
	if row.Status != JobFailed {
		t.Fatalf("Status = %q, want failed", row.Status)
	}
	if row.Error == nil || row.Error.Code != "provider_unreachable" || row.Error.Message != "the provider did not respond" {
		t.Fatalf("Error = %+v, want round-tripped {provider_unreachable, ...}", row.Error)
	}
	if row.ResultRef != "" {
		t.Fatalf("ResultRef = %q, want empty for a failed job with none given", row.ResultRef)
	}
}

func TestJobRepo_MarkTerminal_RejectsNonTerminalStatus(t *testing.T) {
	db := migratedAuditJobsDB(t)
	repo := NewJobRepo(db)
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	if err := repo.Create(ctx, "job-4", "probe", now); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.MarkTerminal(ctx, "job-4", JobPending, now, "", nil, DefaultJobRetention); err == nil {
		t.Fatalf("MarkTerminal(pending) succeeded, want rejection (pending is not a terminal status)")
	}
	if err := repo.MarkTerminal(ctx, "job-4", JobRunning, now, "", nil, DefaultJobRetention); err == nil {
		t.Fatalf("MarkTerminal(running) succeeded, want rejection")
	}
}

// --- P3a-JOBS-001: the discovery job kind ---

// TestJobKind_ParsesDiscoveryFailsClosed proves ParseJobKind accepts
// exactly "discovery" and fails closed on everything else — including a
// near-miss case variant and a trailing-space variant.
func TestJobKind_ParsesDiscoveryFailsClosed(t *testing.T) {
	if got, err := ParseJobKind("discovery"); err != nil || got != JobKindDiscovery {
		t.Fatalf("ParseJobKind(discovery) = (%q, %v), want (discovery, nil)", got, err)
	}
	for _, bad := range []string{"", "Discovery", "probe ", "anything"} {
		if _, err := ParseJobKind(bad); err == nil {
			t.Fatalf("ParseJobKind(%q) succeeded, want ErrUnknownJobKind", bad)
		}
	}
}

// TestJobKind_ParsesReconciliationAndQuotaSync proves P3b-QUOTA-007/008
// registered exactly "reconciliation" and "quota_sync" alongside the
// existing "discovery" kind.
func TestJobKind_ParsesReconciliationAndQuotaSync(t *testing.T) {
	if got, err := ParseJobKind("reconciliation"); err != nil || got != JobKindReconciliation {
		t.Fatalf("ParseJobKind(reconciliation) = (%q, %v), want (reconciliation, nil)", got, err)
	}
	if got, err := ParseJobKind("quota_sync"); err != nil || got != JobKindQuotaSync {
		t.Fatalf("ParseJobKind(quota_sync) = (%q, %v), want (quota_sync, nil)", got, err)
	}
	for _, bad := range []string{"Reconciliation", "quota-sync", "quotasync"} {
		if _, err := ParseJobKind(bad); err == nil {
			t.Fatalf("ParseJobKind(%q) succeeded, want ErrUnknownJobKind", bad)
		}
	}
}

// TestParseJobKind_ProbeRoundTripsAndUnknownStillFails proves
// P3c-CAPI-001/JOBS-001 registered exactly "probe" alongside the existing
// three kinds, and that an unregistered kind still fails closed.
//
// "benchmark" WAS in the unregistered list below and has been removed by
// P6-CAPI-001, which registers it (09 §3.12 names benchmark in the documented
// kind vocabulary, and POST /models/{id}/benchmark now mints jobs of that
// kind). The entry was a snapshot of what was unregistered at P3c time, not an
// invariant — the invariant is "an unregistered kind fails closed", which the
// remaining entries still prove, and TestParseJobKind_AcceptsBenchmark
// (benchmark_test.go) covers the newly-registered value in both directions.
// "backup" and "restore" stay unregistered until P8.
func TestParseJobKind_ProbeRoundTripsAndUnknownStillFails(t *testing.T) {
	if got, err := ParseJobKind("probe"); err != nil || got != JobKindProbe {
		t.Fatalf("ParseJobKind(probe) = (%q, %v), want (probe, nil)", got, err)
	}
	for _, bad := range []string{"Probe", "probes", "Benchmark", "benchmarks", "backup", "restore", ""} {
		if _, err := ParseJobKind(bad); err == nil {
			t.Fatalf("ParseJobKind(%q) succeeded, want ErrUnknownJobKind", bad)
		}
	}
}

// TestJobs_DiscoveryLifecycle proves a discovery-kind job progresses
// pending -> running (started_at stamped) -> completed (retention_until
// stamped), with kind == "discovery" preserved throughout.
func TestJobs_DiscoveryLifecycle(t *testing.T) {
	db := migratedAuditJobsDB(t)
	repo := NewJobRepo(db)
	ctx := context.Background()
	created := time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)

	if err := repo.Create(ctx, "job-disc-1", string(JobKindDiscovery), created); err != nil {
		t.Fatalf("Create: %v", err)
	}
	row, ok, err := repo.GetByID(ctx, "job-disc-1")
	if err != nil || !ok {
		t.Fatalf("GetByID after Create: ok=%v err=%v", ok, err)
	}
	if row.Status != JobPending || row.Kind != string(JobKindDiscovery) {
		t.Fatalf("row after Create = %+v, want pending/discovery", row)
	}

	startedAt := created.Add(time.Second)
	if err := repo.MarkRunning(ctx, "job-disc-1", startedAt); err != nil {
		t.Fatalf("MarkRunning: %v", err)
	}
	row, ok, err = repo.GetByID(ctx, "job-disc-1")
	if err != nil || !ok {
		t.Fatalf("GetByID after MarkRunning: ok=%v err=%v", ok, err)
	}
	if row.Status != JobRunning || row.StartedAt == nil || !row.StartedAt.Equal(startedAt) {
		t.Fatalf("row after MarkRunning = %+v, want running with StartedAt=%v", row, startedAt)
	}

	finishedAt := startedAt.Add(2 * time.Second)
	resultRef := "/api/control/v1/models?account_id=acct-disc-1"
	if err := repo.MarkTerminal(ctx, "job-disc-1", JobCompleted, finishedAt, resultRef, nil, DefaultJobRetention); err != nil {
		t.Fatalf("MarkTerminal: %v", err)
	}
	row, ok, err = repo.GetByID(ctx, "job-disc-1")
	if err != nil || !ok {
		t.Fatalf("GetByID after MarkTerminal: ok=%v err=%v", ok, err)
	}
	if row.Status != JobCompleted || row.Kind != string(JobKindDiscovery) {
		t.Fatalf("row after MarkTerminal = %+v, want completed/discovery", row)
	}
	wantRetention := finishedAt.Add(DefaultJobRetention)
	if row.RetentionUntil == nil || !row.RetentionUntil.Equal(wantRetention) {
		t.Fatalf("RetentionUntil = %v, want %v", row.RetentionUntil, wantRetention)
	}
}

// TestJobs_DiscoveryResultRefIsAReference proves a discovery job's
// result_ref is stored and returned VERBATIM as a reference string: it
// round-trips exactly, contains the affected account id, and — since a
// reference never carries run content — contains none of a model id, the
// string "sk-" (a planted canary secret shape), or a provider error
// string. This proves the storage layer never mutates or enriches
// result_ref; the dynamic guarantee that ServeDiscover only ever PASSES
// such a reference (never inline model/secret content) is proven in
// internal/httpapi/discovery_test.go against the real discoveryResultRef
// helper and a live DiscoveryService.Run.
func TestJobs_DiscoveryResultRefIsAReference(t *testing.T) {
	db := migratedAuditJobsDB(t)
	repo := NewJobRepo(db)
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)

	if err := repo.Create(ctx, "job-disc-2", string(JobKindDiscovery), now); err != nil {
		t.Fatalf("Create: %v", err)
	}

	const accountID = "acct-canary-1"
	resultRef := "/api/control/v1/models?account_id=" + accountID
	if err := repo.MarkTerminal(ctx, "job-disc-2", JobCompleted, now, resultRef, nil, DefaultJobRetention); err != nil {
		t.Fatalf("MarkTerminal: %v", err)
	}

	row, ok, err := repo.GetByID(ctx, "job-disc-2")
	if err != nil || !ok {
		t.Fatalf("GetByID: ok=%v err=%v", ok, err)
	}
	if row.ResultRef != resultRef {
		t.Fatalf("ResultRef = %q, want the exact reference %q (stored verbatim)", row.ResultRef, resultRef)
	}
	if !strings.Contains(row.ResultRef, accountID) {
		t.Fatalf("ResultRef = %q, want it to contain the affected account id %q", row.ResultRef, accountID)
	}
	for _, canary := range []string{"sk-", "model-", "provider error", "gpt-4", "claude-"} {
		if strings.Contains(row.ResultRef, canary) {
			t.Fatalf("ResultRef = %q contains canary %q — a reference must never carry run content", row.ResultRef, canary)
		}
	}
}

// TestJobs_ReadingAJobNeverMutatesIt proves 09 §3.12's "idempotent per
// job_id; re-polling never mutates": two consecutive GetByID calls return
// identical rows.
func TestJobs_ReadingAJobNeverMutatesIt(t *testing.T) {
	db := migratedAuditJobsDB(t)
	repo := NewJobRepo(db)
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)

	if err := repo.Create(ctx, "job-disc-3", string(JobKindDiscovery), now); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.MarkTerminal(ctx, "job-disc-3", JobCompleted, now, "/api/control/v1/models?account_id=acct-x", nil, DefaultJobRetention); err != nil {
		t.Fatalf("MarkTerminal: %v", err)
	}

	first, ok1, err1 := repo.GetByID(ctx, "job-disc-3")
	second, ok2, err2 := repo.GetByID(ctx, "job-disc-3")
	if err1 != nil || err2 != nil || !ok1 || !ok2 {
		t.Fatalf("GetByID calls: ok1=%v err1=%v ok2=%v err2=%v", ok1, err1, ok2, err2)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("consecutive GetByID calls returned different rows:\nfirst  = %+v\nsecond = %+v", first, second)
	}
}

func TestJobRepo_ReapExpired_RemovesOnlyPastRetention(t *testing.T) {
	db := migratedAuditJobsDB(t)
	repo := NewJobRepo(db)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	if err := repo.Create(ctx, "job-old", "probe", base); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.MarkTerminal(ctx, "job-old", JobCompleted, base, "", nil, time.Hour); err != nil {
		t.Fatalf("MarkTerminal(job-old): %v", err)
	}

	if err := repo.Create(ctx, "job-fresh", "probe", base); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.MarkTerminal(ctx, "job-fresh", JobCompleted, base, "", nil, 48*time.Hour); err != nil {
		t.Fatalf("MarkTerminal(job-fresh): %v", err)
	}

	if err := repo.Create(ctx, "job-pending", "probe", base); err != nil {
		t.Fatalf("Create(job-pending): %v", err)
	}

	// Reap at base + 2h: job-old's retention (base+1h) has passed;
	// job-fresh's (base+48h) has not; job-pending has no retention_until
	// at all (never terminal) and must survive regardless.
	n, err := repo.ReapExpired(ctx, base.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("ReapExpired: %v", err)
	}
	if n != 1 {
		t.Fatalf("ReapExpired removed %d rows, want 1", n)
	}

	if _, ok, _ := repo.GetByID(ctx, "job-old"); ok {
		t.Fatalf("job-old still present after its retention passed")
	}
	if _, ok, _ := repo.GetByID(ctx, "job-fresh"); !ok {
		t.Fatalf("job-fresh was reaped despite its retention not having passed")
	}
	if _, ok, _ := repo.GetByID(ctx, "job-pending"); !ok {
		t.Fatalf("job-pending (never terminal, no retention_until) was reaped")
	}
}
