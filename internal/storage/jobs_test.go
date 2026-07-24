package storage

import (
	"context"
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
