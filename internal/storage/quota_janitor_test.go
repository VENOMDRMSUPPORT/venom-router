package storage

import (
	"context"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

// seedJanitorReservation inserts a quota_reservations row plus its single
// allocation directly (bypassing QuotaReservationRepo.Reserve) so janitor
// tests get full control over state/dispatched_at/expires_at — the exact
// combinations Reserve's own fixed contract (dispatched_at always NULL,
// expires_at always now+DefaultProcessingDeadline) cannot produce.
func seedJanitorReservation(t *testing.T, db *DB, id, accountID, windowID, state string, dispatchedAt *int64, expiresAt, createdAt int64, estimatedCost float64) {
	t.Helper()
	var dispatchedArg any
	if dispatchedAt != nil {
		dispatchedArg = *dispatchedAt
	}
	if _, err := db.Conn().Exec(
		`INSERT INTO quota_reservations (id, account_id, request_id, attempt_id, state, dispatched_at, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, accountID, id+"-req", id+"-attempt", state, dispatchedArg, expiresAt, createdAt,
	); err != nil {
		t.Fatalf("seed reservation %s: %v", id, err)
	}
	if _, err := db.Conn().Exec(
		`INSERT INTO quota_reservation_allocations (reservation_id, window_id, unit, estimated_cost, estimate_source, actual_cost, state)
		 VALUES (?, ?, 'requests', ?, 'from_request', NULL, 'reserved')`,
		id, windowID, estimatedCost,
	); err != nil {
		t.Fatalf("seed allocation %s: %v", id, err)
	}
}

// TestBranchA_NeverDispatched proves a reserved reservation past its
// processing deadline with dispatched_at IS NULL is released, its
// allocation moves to released, and the window's debited headroom is
// freed — never left leaking.
func TestBranchA_NeverDispatched(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-janitor-a")
	insertAccount(t, db, "acct-janitor-a", "prov-janitor-a")
	seedWindowFull(t, db, "win-janitor-a", "acct-janitor-a", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 5, 1)
	seedJanitorReservation(t, db, "res-a", "acct-janitor-a", "win-janitor-a", "reserved", nil, 1000, 900, 5)

	repo := NewQuotaLifecycleRepo(db, fixedQuotaClock(2000), nil)
	result, err := repo.Janitor(ctx)
	if err != nil {
		t.Fatalf("Janitor: %v", err)
	}
	if result.Released != 1 || result.Pended != 0 || result.UnknownConsumption != 0 {
		t.Fatalf("Janitor() = %+v, want {Released:1}", result)
	}

	state, settledAt := readReservationState(t, db, "res-a")
	if state != "released" || !settledAt.Valid {
		t.Fatalf("reservation (state=%s settledAt.Valid=%v), want (released, true)", state, settledAt.Valid)
	}
	allocState, _ := readAllocation(t, db, "res-a", "win-janitor-a")
	if allocState != "released" {
		t.Fatalf("allocation state = %q, want released", allocState)
	}
	_, _, reserved, _ := readWindowFull(t, db, "win-janitor-a")
	if reserved != 0 {
		t.Fatalf("window reserved = %v, want 0 (headroom freed, never left leaking)", reserved)
	}
}

// TestBranchB_Dispatched proves a reserved reservation past its
// processing deadline WITH dispatched_at set moves to
// reconciliation_pending, and headroom stays fully debited (no window
// arithmetic at all).
func TestBranchB_Dispatched(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-janitor-b")
	insertAccount(t, db, "acct-janitor-b", "prov-janitor-b")
	seedWindowFull(t, db, "win-janitor-b", "acct-janitor-b", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 5, 1)
	dispatchedAt := int64(950)
	seedJanitorReservation(t, db, "res-b", "acct-janitor-b", "win-janitor-b", "reserved", &dispatchedAt, 1000, 900, 5)

	repo := NewQuotaLifecycleRepo(db, fixedQuotaClock(2000), nil)
	result, err := repo.Janitor(ctx)
	if err != nil {
		t.Fatalf("Janitor: %v", err)
	}
	if result.Pended != 1 || result.Released != 0 || result.UnknownConsumption != 0 {
		t.Fatalf("Janitor() = %+v, want {Pended:1}", result)
	}

	state, settledAt := readReservationState(t, db, "res-b")
	if state != "reconciliation_pending" {
		t.Fatalf("reservation state = %q, want reconciliation_pending", state)
	}
	if settledAt.Valid {
		t.Fatalf("settled_at set for a non-terminal state, want NULL")
	}
	allocState, _ := readAllocation(t, db, "res-b", "win-janitor-b")
	if allocState != "reserved" {
		t.Fatalf("allocation state = %q, want reserved (headroom stays debited)", allocState)
	}
	_, _, reserved, _ := readWindowFull(t, db, "win-janitor-b")
	if reserved != 5 {
		t.Fatalf("window reserved = %v, want 5 (unchanged — no arithmetic on this branch)", reserved)
	}
}

// TestBranchC_RetryDeadline proves a reconciliation_pending reservation
// past the retry deadline (now - DefaultRetryDeadline) moves to
// unknown_consumption, terminal, headroom still debited.
func TestBranchC_RetryDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-janitor-c")
	insertAccount(t, db, "acct-janitor-c", "prov-janitor-c")
	seedWindowFull(t, db, "win-janitor-c", "acct-janitor-c", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 5, 1)
	// now = 10000; retry deadline = now - 1800s = 8200. expires_at = 1000
	// is well past it.
	dispatchedAt := int64(500)
	seedJanitorReservation(t, db, "res-c", "acct-janitor-c", "win-janitor-c", "reconciliation_pending", &dispatchedAt, 1000, 400, 5)

	repo := NewQuotaLifecycleRepo(db, fixedQuotaClock(10000), nil)
	result, err := repo.Janitor(ctx)
	if err != nil {
		t.Fatalf("Janitor: %v", err)
	}
	if result.UnknownConsumption != 1 || result.Released != 0 || result.Pended != 0 {
		t.Fatalf("Janitor() = %+v, want {UnknownConsumption:1}", result)
	}

	state, settledAt := readReservationState(t, db, "res-c")
	if state != "unknown_consumption" || !settledAt.Valid {
		t.Fatalf("reservation (state=%s settledAt.Valid=%v), want (unknown_consumption, true)", state, settledAt.Valid)
	}
	allocState, _ := readAllocation(t, db, "res-c", "win-janitor-c")
	if allocState != "unknown_consumption" {
		t.Fatalf("allocation state = %q, want unknown_consumption", allocState)
	}
	_, _, reserved, _ := readWindowFull(t, db, "win-janitor-c")
	if reserved != 5 {
		t.Fatalf("window reserved = %v, want 5 (still debited — usage gap never silently discarded)", reserved)
	}
}

// TestBranchC_WithinRetryDeadline_UntouchedByJanitor proves a
// reconciliation_pending reservation that is past its processing
// deadline but NOT yet past the retry deadline is left exactly as-is —
// the janitor must not race ahead of the retry window.
func TestBranchC_WithinRetryDeadline_UntouchedByJanitor(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-janitor-c2")
	insertAccount(t, db, "acct-janitor-c2", "prov-janitor-c2")
	seedWindowFull(t, db, "win-janitor-c2", "acct-janitor-c2", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 5, 1)
	// now = 2000; retry deadline = 2000 - 1800 = 200. expires_at = 1000 is
	// past the processing deadline but NOT past the retry deadline (1000 > 200).
	dispatchedAt := int64(500)
	seedJanitorReservation(t, db, "res-c2", "acct-janitor-c2", "win-janitor-c2", "reconciliation_pending", &dispatchedAt, 1000, 400, 5)

	repo := NewQuotaLifecycleRepo(db, fixedQuotaClock(2000), nil)
	result, err := repo.Janitor(ctx)
	if err != nil {
		t.Fatalf("Janitor: %v", err)
	}
	if result.UnknownConsumption != 0 {
		t.Fatalf("Janitor() = %+v, want UnknownConsumption:0 (within retry deadline)", result)
	}
	state, _ := readReservationState(t, db, "res-c2")
	if state != "reconciliation_pending" {
		t.Fatalf("reservation state = %q, want reconciliation_pending (untouched)", state)
	}
}

// TestJanitorPendDispatched_OnlyDispatchedRows drives janitorPendDispatched
// directly against a mix of a null-dispatched and a dispatched
// reserved+expired reservation. This is the only place the "AND
// dispatched_at IS NOT NULL" clause is independently observable: inside
// a full Janitor() sweep, Branch A always runs first and already claims
// every null-dispatched row, so by the time Branch B's query runs no
// null-dispatched row is left in state='reserved' to expose a missing
// filter — exactly like TestReserveOnWindow_VersionGuardBites drives
// reserveOnWindow directly because nothing can interleave through the
// top-level call either.
func TestJanitorPendDispatched_OnlyDispatchedRows(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-janitor-b-direct")
	insertAccount(t, db, "acct-janitor-b-direct", "prov-janitor-b-direct")
	seedWindowFull(t, db, "win-janitor-b-direct", "acct-janitor-b-direct", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 5, 1)
	seedJanitorReservation(t, db, "res-b-direct-null", "acct-janitor-b-direct", "win-janitor-b-direct", "reserved", nil, 1000, 900, 5)
	dispatchedAt := int64(950)
	seedJanitorReservation(t, db, "res-b-direct-dispatched", "acct-janitor-b-direct", "win-janitor-b-direct", "reserved", &dispatchedAt, 1000, 900, 5)

	conn, err := db.Conn().Conn(ctx)
	if err != nil {
		t.Fatalf("acquire conn: %v", err)
	}

	count, err := janitorPendDispatched(ctx, conn, 2000, quota.DefaultJanitorBatchSize)
	if err != nil {
		_ = conn.Close()
		t.Fatalf("janitorPendDispatched: %v", err)
	}
	// Close explicitly, BEFORE the verification reads below: those go
	// through the pool (db.Conn().QueryRow), and with SetMaxOpenConns(1)
	// holding this raw conn any longer would deadlock them waiting for a
	// second connection the pool will never hand out.
	if err := conn.Close(); err != nil {
		t.Fatalf("close conn: %v", err)
	}
	if count != 1 {
		t.Fatalf("janitorPendDispatched() = %d, want exactly 1 (only the dispatched row)", count)
	}

	nullState, _ := readReservationState(t, db, "res-b-direct-null")
	if nullState != "reserved" {
		t.Fatalf("null-dispatched reservation state = %q, want reserved (untouched)", nullState)
	}
	dispatchedState, _ := readReservationState(t, db, "res-b-direct-dispatched")
	if dispatchedState != "reconciliation_pending" {
		t.Fatalf("dispatched reservation state = %q, want reconciliation_pending", dispatchedState)
	}
}

// TestFreshReservations_Untouched proves a brand-new, not-yet-expired
// reservation is left completely untouched by any branch.
func TestFreshReservations_Untouched(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-janitor-fresh")
	insertAccount(t, db, "acct-janitor-fresh", "prov-janitor-fresh")
	seedWindowFull(t, db, "win-janitor-fresh", "acct-janitor-fresh", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 5, 1)
	// now = 1000; expires_at = 2000 (in the future) -> not expired.
	seedJanitorReservation(t, db, "res-fresh", "acct-janitor-fresh", "win-janitor-fresh", "reserved", nil, 2000, 1000, 5)

	repo := NewQuotaLifecycleRepo(db, fixedQuotaClock(1000), nil)
	result, err := repo.Janitor(ctx)
	if err != nil {
		t.Fatalf("Janitor: %v", err)
	}
	if result.Released != 0 || result.Pended != 0 || result.UnknownConsumption != 0 {
		t.Fatalf("Janitor() = %+v, want all zero", result)
	}
	state, _ := readReservationState(t, db, "res-fresh")
	if state != "reserved" {
		t.Fatalf("reservation state = %q, want reserved (untouched)", state)
	}
}

// TestJanitor_AuditEmittedAfterCommit proves the janitor logs a
// per-branch audit row (when a sink is configured) only for branches
// with a non-zero count, and does so without deadlocking (after commit,
// not inside the transaction).
func TestJanitor_AuditEmittedAfterCommit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-janitor-audit")
	insertAccount(t, db, "acct-janitor-audit", "prov-janitor-audit")
	seedWindowFull(t, db, "win-janitor-audit", "acct-janitor-audit", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 5, 1)
	seedJanitorReservation(t, db, "res-audit", "acct-janitor-audit", "win-janitor-audit", "reserved", nil, 1000, 900, 5)

	audit := NewAuditEventRepo(db)
	repo := NewQuotaLifecycleRepo(db, fixedQuotaClock(2000), audit)
	if _, err := repo.Janitor(ctx); err != nil {
		t.Fatalf("Janitor: %v", err)
	}

	var count int
	if err := db.Conn().QueryRow(
		`SELECT COUNT(*) FROM audit_events WHERE action = 'quota_janitor_released'`,
	).Scan(&count); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("quota_janitor_released audit rows = %d, want 1", count)
	}
	var pendedCount int
	if err := db.Conn().QueryRow(
		`SELECT COUNT(*) FROM audit_events WHERE action = 'quota_janitor_pended'`,
	).Scan(&pendedCount); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if pendedCount != 0 {
		t.Fatalf("quota_janitor_pended audit rows = %d, want 0 (zero-count branch must not log)", pendedCount)
	}
}
