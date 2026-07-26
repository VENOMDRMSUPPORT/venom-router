package storage

import (
	"context"
	"testing"
)

// quotaReconcileVersion is the goose version of the reconciliation-fix
// migration (00009_quota_reconciliation.sql).
const quotaReconcileVersion = 9

// TestMigrateQuotaReconciliation_UpDownUp proves 00009 adds the retry
// counter, lease, and confidence columns plus the quota_rebaseline_flags
// table, rolls back to exactly the pre-00009 shape (every M5 table and
// column survives, and a reservation can still be inserted), and
// re-applies. The rollback loop is count-agnostic: it rolls back every
// migration at or above quotaReconcileVersion, so a later migration lands
// without silently breaking this test (mirrors TestMigrateQuota_UpDownUp's
// robustness shape).
func TestMigrateQuotaReconciliation_UpDownUp(t *testing.T) {
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
	assertTableExists(t, db, "quota_rebaseline_flags", true)
	assertColumnExists(t, db, "quota_reservations", "reconcile_attempts", true)
	assertColumnExists(t, db, "quota_reservations", "lease_owner", true)
	assertColumnExists(t, db, "quota_reservations", "lease_expires_at", true)
	assertColumnExists(t, db, "quota_reservation_allocations", "actual_confidence", true)

	for currentVersion(t, db) >= quotaReconcileVersion {
		if _, err := DownOne(ctx, db); err != nil {
			t.Fatalf("DownOne() error = %v", err)
		}
	}
	assertTableExists(t, db, "quota_rebaseline_flags", false)
	assertColumnExists(t, db, "quota_reservations", "reconcile_attempts", false)
	assertColumnExists(t, db, "quota_reservations", "lease_owner", false)
	assertColumnExists(t, db, "quota_reservations", "lease_expires_at", false)
	assertColumnExists(t, db, "quota_reservation_allocations", "actual_confidence", false)
	// Every M5 table (and its own columns) must survive rolling back only
	// 00009.
	assertTableExists(t, db, "quota_windows", true)
	assertTableExists(t, db, "quota_reservations", true)
	assertTableExists(t, db, "quota_reservation_allocations", true)
	assertTableExists(t, db, "cooldowns", true)
	insertProvider(t, db, "prov-rollback-09")
	insertAccount(t, db, "acct-rollback-09", "prov-rollback-09")
	if err := insertQuotaReservation(db, "resv-rollback-09", "acct-rollback-09", "req-rollback-09", "attempt-rollback-09", "reserved"); err != nil {
		t.Fatalf("insert reservation after rollback: %v, want success (M5 shape must still work)", err)
	}
	assertTableExists(t, db, "models", true)
	assertTableExists(t, db, "owner_settings", true)
	assertTableExists(t, db, "providers", true)
	assertTableExists(t, db, "accounts", true)
	assertBaselineTableExists(t, db, true)

	if _, err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() (re-up) error = %v", err)
	}
	assertTableExists(t, db, "quota_rebaseline_flags", true)
	assertColumnExists(t, db, "quota_reservations", "reconcile_attempts", true)
	assertColumnExists(t, db, "quota_reservations", "lease_owner", true)
	assertColumnExists(t, db, "quota_reservations", "lease_expires_at", true)
	assertColumnExists(t, db, "quota_reservation_allocations", "actual_confidence", true)
}

// TestQuotaReservations_NewColumnDefaults proves a reservation inserted
// with only the M5 columns (exactly what every pre-00009 INSERT
// statement in this codebase already does) gets safe defaults for the
// three new columns: reconcile_attempts = 0, lease_owner and
// lease_expires_at both NULL. This is what makes the migration safe for
// reservations already written by code that predates it.
func TestQuotaReservations_NewColumnDefaults(t *testing.T) {
	db := migratedQuotaDB(t)
	insertProvider(t, db, "prov-newcols")
	insertAccount(t, db, "acct-newcols", "prov-newcols")

	if err := insertQuotaReservation(db, "resv-newcols", "acct-newcols", "req-newcols", "attempt-newcols", "reserved"); err != nil {
		t.Fatalf("insert reservation with only M5 columns: %v, want success", err)
	}

	var reconcileAttempts int64
	var leaseOwner, leaseExpiresAt any
	if err := db.Conn().QueryRow(
		`SELECT reconcile_attempts, lease_owner, lease_expires_at FROM quota_reservations WHERE id = ?`, "resv-newcols",
	).Scan(&reconcileAttempts, &leaseOwner, &leaseExpiresAt); err != nil {
		t.Fatalf("query new columns: %v", err)
	}
	if reconcileAttempts != 0 {
		t.Fatalf("reconcile_attempts default = %d, want 0", reconcileAttempts)
	}
	if leaseOwner != nil {
		t.Fatalf("lease_owner default = %v, want NULL", leaseOwner)
	}
	if leaseExpiresAt != nil {
		t.Fatalf("lease_expires_at default = %v, want NULL", leaseExpiresAt)
	}
}

// insertQuotaAllocationWithConfidence seeds a quota_reservation_allocations
// row with an explicit actual_confidence value, accepting `any` so a test
// can pass nil for NULL.
func insertQuotaAllocationWithConfidence(db *DB, reservationID, windowID string, confidence any) error {
	_, err := db.Conn().Exec(
		`INSERT INTO quota_reservation_allocations (reservation_id, window_id, unit, estimated_cost, estimate_source, state, actual_confidence)
		 VALUES (?, ?, 'concurrency', 1, 'from_request', 'reserved', ?)`,
		reservationID, windowID, confidence,
	)
	return err
}

// TestQuotaAllocations_ActualConfidenceCheck proves actual_confidence
// accepts exactly 'high', 'low', and NULL, and rejects everything else —
// both directions, including a near-miss case variant.
func TestQuotaAllocations_ActualConfidenceCheck(t *testing.T) {
	db := migratedQuotaDB(t)
	insertProvider(t, db, "prov-confidence")
	insertAccount(t, db, "acct-confidence", "prov-confidence")
	if err := insertQuotaReservation(db, "resv-confidence", "acct-confidence", "req-confidence", "attempt-confidence", "reserved"); err != nil {
		t.Fatalf("seed reservation: %v", err)
	}

	for i, good := range []any{"high", "low", nil} {
		winID := "win-confidence-good-" + string(rune('a'+i))
		if err := insertQuotaWindow(db, winID, "acct-confidence", "local_safety", "concurrency", "concurrency", "local:good-"+string(rune('a'+i))); err != nil {
			t.Fatalf("seed window %d: %v", i, err)
		}
		if err := insertQuotaAllocationWithConfidence(db, "resv-confidence", winID, good); err != nil {
			t.Fatalf("insert allocation with actual_confidence=%v: %v, want success", good, err)
		}
	}

	for i, bad := range []any{"medium", "", "High"} {
		winID := "win-confidence-bad-" + string(rune('a'+i))
		if err := insertQuotaWindow(db, winID, "acct-confidence", "local_safety", "concurrency", "concurrency", "local:bad-"+string(rune('a'+i))); err != nil {
			t.Fatalf("seed window %d: %v", i, err)
		}
		if err := insertQuotaAllocationWithConfidence(db, "resv-confidence", winID, bad); err == nil {
			t.Fatalf("insert allocation with actual_confidence=%q succeeded, want CHECK rejection", bad)
		}
	}
}

// insertRebaselineFlag seeds a quota_rebaseline_flags row.
func insertRebaselineFlag(db *DB, accountID, reasonCode string, flaggedAt int64) error {
	_, err := db.Conn().Exec(
		`INSERT INTO quota_rebaseline_flags (account_id, reason_code, flagged_at) VALUES (?, ?, ?)`,
		accountID, reasonCode, flaggedAt,
	)
	return err
}

// TestQuotaRebaselineFlags_PrimaryKeyAndCascade proves exactly one flag
// per account (PRIMARY KEY(account_id) rejects a second insert for the
// same account) and that deleting the account cascades the flag away.
func TestQuotaRebaselineFlags_PrimaryKeyAndCascade(t *testing.T) {
	db := migratedQuotaDB(t)
	insertProvider(t, db, "prov-rebaseline")
	insertAccount(t, db, "acct-rebaseline", "prov-rebaseline")

	if err := insertRebaselineFlag(db, "acct-rebaseline", "usage_gap", 1000); err != nil {
		t.Fatalf("insert first flag: %v, want success", err)
	}
	if err := insertRebaselineFlag(db, "acct-rebaseline", "estimate_settled_low_confidence", 2000); err == nil {
		t.Fatalf("insert second flag for the same account succeeded, want PRIMARY KEY rejection")
	}

	if _, err := db.Conn().Exec(`DELETE FROM accounts WHERE id = ?`, "acct-rebaseline"); err != nil {
		t.Fatalf("delete account: %v, want success (cascades to its flag)", err)
	}
	var count int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM quota_rebaseline_flags WHERE account_id = ?`, "acct-rebaseline").Scan(&count); err != nil {
		t.Fatalf("count flags after account delete: %v", err)
	}
	if count != 0 {
		t.Fatalf("flags after account delete = %d, want 0 (cascade)", count)
	}
}
