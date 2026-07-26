package storage

import (
	"context"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

// TestFlagRebaseline_IsIdempotentAndOrdered proves flagging the same
// account twice keeps the FIRST flagged_at and reason (the first
// signal is the one that matters), that two distinct accounts come back
// from RebaselineFlagged in deterministic order, and that empty inputs
// are rejected with a typed error.
func TestFlagRebaseline_IsIdempotentAndOrdered(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-rebaseline-flag")
	insertAccount(t, db, "acct-rebaseline-b", "prov-rebaseline-flag")
	insertAccount(t, db, "acct-rebaseline-a", "prov-rebaseline-flag")

	repo := NewReconciliationRepo(db, fixedQuotaClock(1000), quota.DefaultReconciliationPolicy(), NewQuotaLifecycleRepo(db, fixedQuotaClock(1000), nil), nil)

	if err := repo.FlagRebaseline(ctx, "acct-rebaseline-b", "usage_gap"); err != nil {
		t.Fatalf("first FlagRebaseline: %v", err)
	}

	// Re-flag with a different reason at a later clock; the FIRST
	// flagged_at/reason must survive.
	later := NewReconciliationRepo(db, fixedQuotaClock(9999), quota.DefaultReconciliationPolicy(), NewQuotaLifecycleRepo(db, fixedQuotaClock(9999), nil), nil)
	if err := later.FlagRebaseline(ctx, "acct-rebaseline-b", "estimate_settled_low_confidence"); err != nil {
		t.Fatalf("second FlagRebaseline: %v, want success (idempotent no-op)", err)
	}

	var reasonCode string
	var flaggedAt int64
	if err := db.Conn().QueryRow(`SELECT reason_code, flagged_at FROM quota_rebaseline_flags WHERE account_id = ?`, "acct-rebaseline-b").Scan(&reasonCode, &flaggedAt); err != nil {
		t.Fatalf("read flag: %v", err)
	}
	if reasonCode != "usage_gap" || flaggedAt != 1000 {
		t.Fatalf("flag = (reason=%q flagged_at=%d), want (usage_gap, 1000) — the FIRST call's values", reasonCode, flaggedAt)
	}

	if err := repo.FlagRebaseline(ctx, "acct-rebaseline-a", "usage_gap"); err != nil {
		t.Fatalf("FlagRebaseline for second account: %v", err)
	}

	flagged, err := repo.RebaselineFlagged(ctx)
	if err != nil {
		t.Fatalf("RebaselineFlagged: %v", err)
	}
	if len(flagged) != 2 || flagged[0] != "acct-rebaseline-a" || flagged[1] != "acct-rebaseline-b" {
		t.Fatalf("RebaselineFlagged() = %v, want [acct-rebaseline-a acct-rebaseline-b] (deterministic order)", flagged)
	}

	if err := repo.FlagRebaseline(ctx, "", "usage_gap"); err == nil {
		t.Fatal("FlagRebaseline with empty account id succeeded, want rejection")
	}
	if err := repo.FlagRebaseline(ctx, "acct-rebaseline-a", ""); err == nil {
		t.Fatal("FlagRebaseline with empty reason code succeeded, want rejection")
	}
}

// TestClearRebaseline_RemovesTheFlag proves ClearRebaseline removes an
// existing flag and is a harmless no-op for an account with no flag.
func TestClearRebaseline_RemovesTheFlag(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-rebaseline-clear")
	insertAccount(t, db, "acct-rebaseline-clear", "prov-rebaseline-clear")

	repo := NewReconciliationRepo(db, fixedQuotaClock(1000), quota.DefaultReconciliationPolicy(), NewQuotaLifecycleRepo(db, fixedQuotaClock(1000), nil), nil)
	if err := repo.FlagRebaseline(ctx, "acct-rebaseline-clear", "usage_gap"); err != nil {
		t.Fatalf("FlagRebaseline: %v", err)
	}
	if err := repo.ClearRebaseline(ctx, "acct-rebaseline-clear"); err != nil {
		t.Fatalf("ClearRebaseline: %v", err)
	}

	flagged, err := repo.RebaselineFlagged(ctx)
	if err != nil {
		t.Fatalf("RebaselineFlagged: %v", err)
	}
	if len(flagged) != 0 {
		t.Fatalf("RebaselineFlagged() = %v, want empty", flagged)
	}

	// Clearing an account with no flag is a harmless no-op.
	if err := repo.ClearRebaseline(ctx, "acct-never-flagged"); err != nil {
		t.Fatalf("ClearRebaseline on unflagged account: %v, want success (no-op)", err)
	}
}
