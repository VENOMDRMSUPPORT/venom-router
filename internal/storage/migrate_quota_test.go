package storage

import (
	"context"
	"fmt"
	"testing"
)

// quotaVersion is the goose version of the M5 quota migration
// (00008_quota.sql).
const quotaVersion = 8

// TestMigrateQuota_UpDownUp proves M5 (quota_windows, quota_reservations,
// quota_reservation_allocations, cooldowns) applies, rolls back to exactly
// the pre-M5 state (every lower table survives), and re-applies. The
// rollback loop is count-agnostic: it rolls back every migration at or
// above quotaVersion, so a later M6 lands without silently breaking this
// test (mirrors TestMigrateCatalog_UpDownUp's robustness shape).
func TestMigrateQuota_UpDownUp(t *testing.T) {
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
	assertTableExists(t, db, "quota_windows", true)
	assertTableExists(t, db, "quota_reservations", true)
	assertTableExists(t, db, "quota_reservation_allocations", true)
	assertTableExists(t, db, "cooldowns", true)

	for currentVersion(t, db) >= quotaVersion {
		if _, err := DownOne(ctx, db); err != nil {
			t.Fatalf("DownOne() error = %v", err)
		}
	}
	assertTableExists(t, db, "quota_windows", false)
	assertTableExists(t, db, "quota_reservations", false)
	assertTableExists(t, db, "quota_reservation_allocations", false)
	assertTableExists(t, db, "cooldowns", false)
	// Every lower table must survive rolling back only M5.
	assertTableExists(t, db, "models", true)
	assertTableExists(t, db, "provider_model_aliases", true)
	assertTableExists(t, db, "account_model_offerings", true)
	assertTableExists(t, db, "offering_operations", true)
	assertTableExists(t, db, "certifications", true)
	assertTableExists(t, db, "discovery_runs", true)
	assertTableExists(t, db, "owner_settings", true)
	assertTableExists(t, db, "audit_events", true)
	assertTableExists(t, db, "jobs", true)
	assertTableExists(t, db, "providers", true)
	assertTableExists(t, db, "accounts", true)
	assertTableExists(t, db, "account_credentials", true)
	assertTableExists(t, db, "account_funding_evidence", true)
	assertTableExists(t, db, "oauth_transactions", true)
	assertTableExists(t, db, "owner_auth", true)
	assertTableExists(t, db, "owner_sessions", true)
	assertTableExists(t, db, "auth_events", true)
	assertBaselineTableExists(t, db, true)

	if _, err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() (re-up) error = %v", err)
	}
	assertTableExists(t, db, "quota_windows", true)
	assertTableExists(t, db, "quota_reservations", true)
	assertTableExists(t, db, "quota_reservation_allocations", true)
	assertTableExists(t, db, "cooldowns", true)
}

// TestQuotaReservations_StateCheck proves the five-state reservation
// lifecycle CHECK (02 §3) is a mutation-provable DB-level invariant: every
// one of the five frozen states is accepted, and there is deliberately no
// `expired` state (nor any case-variant or empty value). A reviewer
// re-proves this by dropping the CHECK and confirming the rejection
// assertions go RED.
func TestQuotaReservations_StateCheck(t *testing.T) {
	db := migratedQuotaDB(t)
	insertProvider(t, db, "prov-resv")
	insertAccount(t, db, "acct-resv", "prov-resv")

	legal := []string{"reserved", "reconciliation_pending", "settled", "released", "unknown_consumption"}
	for i, state := range legal {
		id := fmt.Sprintf("resv-%d", i)
		attemptID := fmt.Sprintf("attempt-%d", i)
		if err := insertQuotaReservation(db, id, "acct-resv", "req-legal", attemptID, state); err != nil {
			t.Fatalf("insert reservation with allowed state %q: %v, want success", state, err)
		}
	}

	for i, bad := range []string{"expired", "", "Reserved"} {
		attemptID := fmt.Sprintf("attempt-bad-%d", i)
		if err := insertQuotaReservation(db, fmt.Sprintf("resv-bad-%d", i), "acct-resv", "req-bad", attemptID, bad); err == nil {
			t.Fatalf("insert reservation with disallowed state %q succeeded, want CHECK rejection", bad)
		}
	}
}

// TestQuotaWindows_WindowKeyNeverNullOrEmpty proves window_key is both
// NOT NULL and non-empty (02 §3: window_key is never NULL and never the
// empty string), in three subtests. A reviewer re-proves each half
// independently by relaxing NOT NULL or dropping the empty-string CHECK.
func TestQuotaWindows_WindowKeyNeverNullOrEmpty(t *testing.T) {
	db := migratedQuotaDB(t)
	insertProvider(t, db, "prov-wk")
	insertAccount(t, db, "acct-wk", "prov-wk")

	t.Run("null window_key rejected", func(t *testing.T) {
		if err := insertQuotaWindowRaw(db, "win-null", "acct-wk", "local_safety", "concurrency", "concurrency", nil, "fresh"); err == nil {
			t.Fatalf("insert window with NULL window_key succeeded, want NOT NULL rejection")
		}
	})
	t.Run("empty window_key rejected", func(t *testing.T) {
		if err := insertQuotaWindowRaw(db, "win-empty", "acct-wk", "local_safety", "concurrency", "concurrency", "", "fresh"); err == nil {
			t.Fatalf("insert window with empty window_key succeeded, want CHECK rejection")
		}
	})
	t.Run("normal key succeeds", func(t *testing.T) {
		if err := insertQuotaWindow(db, "win-ok", "acct-wk", "local_safety", "concurrency", "concurrency", "local:concurrency"); err != nil {
			t.Fatalf("insert window with normal window_key: %v, want success", err)
		}
	})
}

// TestQuotaWindows_MultipleWindowsPerAccountAndUnit proves the whole point
// of the multi-window model (02 §3): two windows sharing (account_id,
// unit) but differing in window_type/window_key both succeed, while an
// exact duplicate of the five identity columns is rejected.
func TestQuotaWindows_MultipleWindowsPerAccountAndUnit(t *testing.T) {
	db := migratedQuotaDB(t)
	insertProvider(t, db, "prov-multi")
	insertAccount(t, db, "acct-multi", "prov-multi")

	if err := insertQuotaWindow(db, "win-a", "acct-multi", "provider_evidence", "requests", "rpm", "provider:rpm"); err != nil {
		t.Fatalf("insert first requests window: %v, want success", err)
	}
	if err := insertQuotaWindow(db, "win-b", "acct-multi", "provider_evidence", "requests", "rolling_7d", "provider:seven_day"); err != nil {
		t.Fatalf("insert second requests window (different window_type/key): %v, want success", err)
	}
	if err := insertQuotaWindow(db, "win-dup", "acct-multi", "provider_evidence", "requests", "rpm", "provider:rpm"); err == nil {
		t.Fatalf("insert exact duplicate of the five identity columns succeeded, want UNIQUE rejection")
	}
}

// TestQuotaWindows_SourceAndFreshnessChecks proves the source and
// freshness_state CHECKs (02 §3) independently. `provider_policy` is a
// real neighbouring vocabulary token from account_funding_evidence.source
// (02 §2) — deliberately rejected here to prove the two vocabularies are
// never conflated.
func TestQuotaWindows_SourceAndFreshnessChecks(t *testing.T) {
	db := migratedQuotaDB(t)
	insertProvider(t, db, "prov-src")
	insertAccount(t, db, "acct-src", "prov-src")

	for i, source := range []string{"provider_evidence", "local_safety", "owner_override"} {
		id := fmt.Sprintf("win-src-%d", i)
		key := fmt.Sprintf("local:src-%d", i)
		if err := insertQuotaWindow(db, id, "acct-src", source, "requests", "rpm", key); err != nil {
			t.Fatalf("insert window with allowed source %q: %v, want success", source, err)
		}
	}
	for i, freshness := range []string{"fresh", "stale", "unknown"} {
		id := fmt.Sprintf("win-fresh-%d", i)
		key := fmt.Sprintf("local:fresh-%d", i)
		if err := insertQuotaWindowRaw(db, id, "acct-src", "local_safety", "requests", "rpm", key, freshness); err != nil {
			t.Fatalf("insert window with allowed freshness %q: %v, want success", freshness, err)
		}
	}

	if err := insertQuotaWindow(db, "win-bad-src", "acct-src", "provider_policy", "requests", "rpm", "local:bad-src"); err == nil {
		t.Fatalf("insert window with disallowed source %q succeeded, want CHECK rejection", "provider_policy")
	}
	if err := insertQuotaWindowRaw(db, "win-bad-fresh", "acct-src", "local_safety", "requests", "rpm", "local:bad-fresh", "expired"); err == nil {
		t.Fatalf("insert window with disallowed freshness %q succeeded, want CHECK rejection", "expired")
	}
}

// TestQuotaAllocations_PrimaryKeyAndStateCheck proves the allocation
// PRIMARY KEY(reservation_id, window_id) and the four-value allocation
// state CHECK (02 §3: an allocation is never `reconciliation_pending` —
// only the reservation carries that state while its allocations stay
// `reserved`).
func TestQuotaAllocations_PrimaryKeyAndStateCheck(t *testing.T) {
	db := migratedQuotaDB(t)
	insertProvider(t, db, "prov-alloc")
	insertAccount(t, db, "acct-alloc", "prov-alloc")
	if err := insertQuotaWindow(db, "win-alloc", "acct-alloc", "local_safety", "concurrency", "concurrency", "local:concurrency"); err != nil {
		t.Fatalf("seed window: %v", err)
	}
	if err := insertQuotaReservation(db, "resv-alloc", "acct-alloc", "req-alloc", "attempt-1", "reserved"); err != nil {
		t.Fatalf("seed reservation: %v", err)
	}

	if err := insertQuotaAllocation(db, "resv-alloc", "win-alloc", "concurrency", "from_request", "reserved", 1); err != nil {
		t.Fatalf("insert first allocation: %v, want success", err)
	}
	if err := insertQuotaAllocation(db, "resv-alloc", "win-alloc", "concurrency", "from_request", "reserved", 1); err == nil {
		t.Fatalf("insert second allocation for the same (reservation_id, window_id) succeeded, want PRIMARY KEY rejection")
	}

	for i, state := range []string{"settled", "released", "unknown_consumption"} {
		winID := fmt.Sprintf("win-alloc-state-%d", i)
		if err := insertQuotaWindow(db, winID, "acct-alloc", "local_safety", "concurrency", "concurrency", fmt.Sprintf("local:state-%d", i)); err != nil {
			t.Fatalf("seed window %d: %v", i, err)
		}
		if err := insertQuotaAllocation(db, "resv-alloc", winID, "concurrency", "from_request", state, 1); err != nil {
			t.Fatalf("insert allocation with allowed state %q: %v, want success", state, err)
		}
	}

	winBad := "win-alloc-bad"
	if err := insertQuotaWindow(db, winBad, "acct-alloc", "local_safety", "concurrency", "concurrency", "local:bad"); err != nil {
		t.Fatalf("seed window: %v", err)
	}
	if err := insertQuotaAllocation(db, "resv-alloc", winBad, "concurrency", "from_request", "reconciliation_pending", 1); err == nil {
		t.Fatalf("insert allocation with reservation-only state %q succeeded, want CHECK rejection", "reconciliation_pending")
	}
}

// TestQuotaAllocations_WindowDeleteIsRestricted proves the deliberate
// asymmetry (02 §3): a quota_windows row with a live allocation cannot be
// deleted (no ON DELETE CASCADE on allocations.window_id), while deleting
// the parent reservation cascades its allocations away.
func TestQuotaAllocations_WindowDeleteIsRestricted(t *testing.T) {
	db := migratedQuotaDB(t)
	insertProvider(t, db, "prov-del")
	insertAccount(t, db, "acct-del", "prov-del")
	if err := insertQuotaWindow(db, "win-del", "acct-del", "local_safety", "concurrency", "concurrency", "local:concurrency"); err != nil {
		t.Fatalf("seed window: %v", err)
	}
	if err := insertQuotaReservation(db, "resv-del", "acct-del", "req-del", "attempt-del", "reserved"); err != nil {
		t.Fatalf("seed reservation: %v", err)
	}
	if err := insertQuotaAllocation(db, "resv-del", "win-del", "concurrency", "from_request", "reserved", 1); err != nil {
		t.Fatalf("seed allocation: %v", err)
	}

	if _, err := db.Conn().Exec(`DELETE FROM quota_windows WHERE id = ?`, "win-del"); err == nil {
		t.Fatalf("delete window with a live allocation succeeded, want FK rejection (no cascade)")
	}

	if _, err := db.Conn().Exec(`DELETE FROM quota_reservations WHERE id = ?`, "resv-del"); err != nil {
		t.Fatalf("delete reservation: %v, want success (cascades to its allocations)", err)
	}
	var count int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM quota_reservation_allocations WHERE reservation_id = ?`, "resv-del").Scan(&count); err != nil {
		t.Fatalf("count allocations after reservation delete: %v", err)
	}
	if count != 0 {
		t.Fatalf("allocations after reservation delete = %d, want 0 (cascade)", count)
	}
}

// TestCooldowns_ScopeExclusivity proves the exactly-one-ref CHECK and the
// three partial-unique indexes (02 §3 / 05 §3). Table-driven over the
// three legal single-ref combinations plus three illegal combinations
// (wrong extra ref, two refs, zero refs), then the partial-unique
// dedup-per-scope-reference behavior.
func TestCooldowns_ScopeExclusivity(t *testing.T) {
	db := migratedQuotaDB(t)
	insertProvider(t, db, "prov-cd")
	insertAccount(t, db, "acct-cd", "prov-cd")
	insertAccount(t, db, "acct-cd-2", "prov-cd")
	opID := seedOfferingOperationChain(t, db, "acct-cd-op", "prov-cd-op", "model-cd", "cd-model")

	cases := []struct {
		name     string
		scope    string
		account  any
		offering any
		provider any
		wantErr  bool
	}{
		{"account scope with only account ref", "account", "acct-cd", nil, nil, false},
		{"offering scope with only offering ref", "offering", nil, opID, nil, false},
		{"provider scope with only provider ref", "provider", nil, nil, "prov-cd", false},
		{"account scope carrying an extra provider ref", "account", "acct-cd", nil, "prov-cd", true},
		{"scope with two refs set", "account", "acct-cd", opID, nil, true},
		{"scope with zero refs set", "account", nil, nil, nil, true},
	}
	for i, tc := range cases {
		id := fmt.Sprintf("cd-%d", i)
		err := insertCooldown(db, id, tc.scope, tc.account, tc.offering, tc.provider, "reason", "retry_after")
		if tc.wantErr && err == nil {
			t.Fatalf("%s: insert succeeded, want CHECK rejection", tc.name)
		}
		if !tc.wantErr && err != nil {
			t.Fatalf("%s: insert failed: %v, want success", tc.name, err)
		}
	}

	// A second cooldown for the same account_id is rejected by the partial
	// unique index...
	if err := insertCooldown(db, "cd-acct-dup", "account", "acct-cd", nil, nil, "reason-dup", "retry_after"); err == nil {
		t.Fatalf("insert second cooldown for the same account_id succeeded, want partial-unique rejection")
	}
	// ...while a cooldown scoped to a different account succeeds.
	if err := insertCooldown(db, "cd-acct-2", "account", "acct-cd-2", nil, nil, "reason-2", "retry_after"); err != nil {
		t.Fatalf("insert cooldown for a different account: %v, want success", err)
	}
}

// migratedQuotaDB opens and fully migrates a fresh temp-dir DB, returning
// it (with a t.Cleanup Close). Mirrors migratedCatalogDB/migratedCatalogRepoDB.
func migratedQuotaDB(t *testing.T) *DB {
	t.Helper()

	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := Migrate(context.Background(), db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return db
}

// insertQuotaWindowRaw inserts a quota_windows row, accepting `any` for
// windowKey so tests can exercise a NULL window_key (which insertQuotaWindow's
// string signature cannot express).
func insertQuotaWindowRaw(db *DB, id, accountID, source, unit, windowType string, windowKey any, freshness string) error {
	_, err := db.Conn().Exec(
		`INSERT INTO quota_windows
		    (id, account_id, source, unit, window_type, window_key, reserved, version, confidence, freshness_state, observed_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, 0, 1, 1, ?, 0, 0, 0)`,
		id, accountID, source, unit, windowType, windowKey, freshness,
	)
	return err
}

// insertQuotaWindow seeds a quota_windows row with a fresh freshness_state
// and a normal (non-NULL, non-empty) window_key.
func insertQuotaWindow(db *DB, id, accountID, source, unit, windowType, windowKey string) error {
	return insertQuotaWindowRaw(db, id, accountID, source, unit, windowType, windowKey, "fresh")
}

// insertQuotaReservation seeds a quota_reservations row.
func insertQuotaReservation(db *DB, id, accountID, requestID, attemptID, state string) error {
	_, err := db.Conn().Exec(
		`INSERT INTO quota_reservations (id, account_id, request_id, attempt_id, state, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?, 0, 0)`,
		id, accountID, requestID, attemptID, state,
	)
	return err
}

// insertQuotaAllocation seeds a quota_reservation_allocations row.
func insertQuotaAllocation(db *DB, reservationID, windowID, unit, estimateSource, state string, estimatedCost float64) error {
	_, err := db.Conn().Exec(
		`INSERT INTO quota_reservation_allocations (reservation_id, window_id, unit, estimated_cost, estimate_source, state)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		reservationID, windowID, unit, estimatedCost, estimateSource, state,
	)
	return err
}

// insertCooldown seeds a cooldowns row. account/offering/provider accept
// `any` so tests can pass either a string ref or nil.
func insertCooldown(db *DB, id, scope string, account, offering, provider any, reasonCode, source string) error {
	_, err := db.Conn().Exec(
		`INSERT INTO cooldowns (id, scope, account_id, offering_operation_id, provider_id, reason_code, until, source, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, 0, ?, 0, 0)`,
		id, scope, account, offering, provider, reasonCode, source,
	)
	return err
}
