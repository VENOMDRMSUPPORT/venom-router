package storage

import (
	"context"
	"testing"
	"time"
)

func TestAuthEventRepo_Append_InsertsOneRow(t *testing.T) {
	db := migratedOwnerAuthDB(t)
	repo := NewAuthEventRepo(db)
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	if err := repo.Append(ctx, "login", "failure", "invalid_credentials", now); err != nil {
		t.Fatalf("Append: unexpected error: %v", err)
	}
	assertRowCount(t, db, "auth_events", 1)

	var action, result, reasonCode string
	if err := db.Conn().QueryRow(`SELECT action, result, reason_code FROM auth_events LIMIT 1`).Scan(&action, &result, &reasonCode); err != nil {
		t.Fatalf("query inserted row: %v", err)
	}
	if action != "login" || result != "failure" || reasonCode != "invalid_credentials" {
		t.Fatalf("row = {%q %q %q}, want {login failure invalid_credentials}", action, result, reasonCode)
	}
}

func TestAuthEventRepo_Append_EmptyReasonCodeStoresNull(t *testing.T) {
	db := migratedOwnerAuthDB(t)
	repo := NewAuthEventRepo(db)
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	if err := repo.Append(ctx, "login", "success", "", now); err != nil {
		t.Fatalf("Append: unexpected error: %v", err)
	}

	var reasonCode *string
	if err := db.Conn().QueryRow(`SELECT reason_code FROM auth_events LIMIT 1`).Scan(&reasonCode); err != nil {
		t.Fatalf("query: %v", err)
	}
	if reasonCode != nil {
		t.Fatalf("reason_code = %v, want NULL for an empty reasonCode", *reasonCode)
	}
}

func TestAuthEventRepo_FailureStreak_CountsConsecutiveFailures(t *testing.T) {
	db := migratedOwnerAuthDB(t)
	repo := NewAuthEventRepo(db)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := 0; i < 3; i++ {
		if err := repo.Append(ctx, "login", "failure", "invalid_credentials", base.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("Append(%d): %v", i, err)
		}
	}

	count, oldest, err := repo.FailureStreak(ctx, "login", base.Add(-15*time.Minute))
	if err != nil {
		t.Fatalf("FailureStreak: %v", err)
	}
	if count != 3 {
		t.Fatalf("count = %d, want 3", count)
	}
	if !oldest.Equal(base) {
		t.Fatalf("oldest = %v, want %v (the first of the 3 failures)", oldest, base)
	}
}

func TestAuthEventRepo_FailureStreak_ResetBySuccess(t *testing.T) {
	db := migratedOwnerAuthDB(t)
	repo := NewAuthEventRepo(db)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	if err := repo.Append(ctx, "login", "failure", "invalid_credentials", base); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := repo.Append(ctx, "login", "failure", "invalid_credentials", base.Add(time.Second)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := repo.Append(ctx, "login", "success", "", base.Add(2*time.Second)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := repo.Append(ctx, "login", "failure", "invalid_credentials", base.Add(3*time.Second)); err != nil {
		t.Fatalf("Append: %v", err)
	}

	count, oldest, err := repo.FailureStreak(ctx, "login", base.Add(-15*time.Minute))
	if err != nil {
		t.Fatalf("FailureStreak: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1 (only the failure AFTER the success counts)", count)
	}
	if !oldest.Equal(base.Add(3 * time.Second)) {
		t.Fatalf("oldest = %v, want %v", oldest, base.Add(3*time.Second))
	}
}

func TestAuthEventRepo_FailureStreak_ExcludesFailuresOlderThanSince(t *testing.T) {
	db := migratedOwnerAuthDB(t)
	repo := NewAuthEventRepo(db)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// A failure well before the 15-minute window...
	if err := repo.Append(ctx, "login", "failure", "invalid_credentials", base.Add(-20*time.Minute)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	// ...then two within it.
	if err := repo.Append(ctx, "login", "failure", "invalid_credentials", base.Add(-2*time.Minute)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := repo.Append(ctx, "login", "failure", "invalid_credentials", base.Add(-time.Minute)); err != nil {
		t.Fatalf("Append: %v", err)
	}

	count, _, err := repo.FailureStreak(ctx, "login", base.Add(-15*time.Minute))
	if err != nil {
		t.Fatalf("FailureStreak: %v", err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2 (the 20-min-old failure is outside the window)", count)
	}
}

func TestAuthEventRepo_FailureStreak_SeparateActionsIndependent(t *testing.T) {
	db := migratedOwnerAuthDB(t)
	repo := NewAuthEventRepo(db)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := 0; i < 5; i++ {
		if err := repo.Append(ctx, "login", "failure", "invalid_credentials", base.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("Append(login,%d): %v", i, err)
		}
	}
	if err := repo.Append(ctx, "reverify", "failure", "invalid_credentials", base); err != nil {
		t.Fatalf("Append(reverify): %v", err)
	}

	loginCount, _, err := repo.FailureStreak(ctx, "login", base.Add(-15*time.Minute))
	if err != nil {
		t.Fatalf("FailureStreak(login): %v", err)
	}
	if loginCount != 5 {
		t.Fatalf("login count = %d, want 5", loginCount)
	}

	reverifyCount, _, err := repo.FailureStreak(ctx, "reverify", base.Add(-15*time.Minute))
	if err != nil {
		t.Fatalf("FailureStreak(reverify): %v", err)
	}
	if reverifyCount != 1 {
		t.Fatalf("reverify count = %d, want 1 (independent of login's streak)", reverifyCount)
	}
}
