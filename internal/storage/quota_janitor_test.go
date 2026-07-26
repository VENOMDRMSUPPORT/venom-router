package storage

import (
	"context"
	"database/sql"
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

// TestBranchC1_ReclaimDoesNotTerminalize proves a reconciliation_pending
// reservation with an EXPIRED lease and reconcile_attempts below
// MaxRetries is reclaimed (lease cleared, Reclaimed counted) and NOT
// terminalized — the janitor must never race ahead of the worker's own
// retry budget just because a lease expired.
func TestBranchC1_ReclaimDoesNotTerminalize(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-janitor-c1")
	insertAccount(t, db, "acct-janitor-c1", "prov-janitor-c1")
	seedWindowFull(t, db, "win-janitor-c1", "acct-janitor-c1", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 5, 1)
	dispatchedAt := int64(500)
	seedJanitorReservation(t, db, "res-c1", "acct-janitor-c1", "win-janitor-c1", "reconciliation_pending", &dispatchedAt, 1000, 400, 5)
	// A lease from a crashed worker, already expired as of now=2000.
	if _, err := db.Conn().Exec(`UPDATE quota_reservations SET lease_owner = ?, lease_expires_at = ? WHERE id = ?`, "worker-dead", 1500, "res-c1"); err != nil {
		t.Fatalf("seed expired lease: %v", err)
	}

	policy := quota.DefaultReconciliationPolicy() // reconcile_attempts=0 is well below MaxRetries=5
	repo := NewQuotaLifecycleRepo(db, fixedQuotaClock(2000), nil).WithPolicy(policy)
	result, err := repo.Janitor(ctx)
	if err != nil {
		t.Fatalf("Janitor: %v", err)
	}
	if result.Reclaimed != 1 || result.UnknownConsumption != 0 {
		t.Fatalf("Janitor() = %+v, want {Reclaimed:1, UnknownConsumption:0}", result)
	}

	state, _ := readReservationState(t, db, "res-c1")
	if state != "reconciliation_pending" {
		t.Fatalf("reservation state = %q, want reconciliation_pending (reclaimed, not terminalized)", state)
	}
	allocState, _ := readAllocation(t, db, "res-c1", "win-janitor-c1")
	if allocState != "reserved" {
		t.Fatalf("allocation state = %q, want reserved (untouched)", allocState)
	}
	var leaseOwner sql.NullString
	var leaseExpiresAt sql.NullInt64
	if err := db.Conn().QueryRow(`SELECT lease_owner, lease_expires_at FROM quota_reservations WHERE id = ?`, "res-c1").Scan(&leaseOwner, &leaseExpiresAt); err != nil {
		t.Fatalf("read lease columns: %v", err)
	}
	if leaseOwner.Valid || leaseExpiresAt.Valid {
		t.Fatalf("lease = (owner=%v expires=%v), want both NULL (reclaimed)", leaseOwner, leaseExpiresAt)
	}
}

// TestBranchC2_TerminalOnlyAtRetryBoundary proves the SAME reservation
// shape as TestBranchC1_ReclaimDoesNotTerminalize, but with
// reconcile_attempts already AT MaxRetries, becomes unknown_consumption
// with a per-reservation usage_gap audit row — a summary count is not
// sufficient: 05 §4 requires the gap surfaceable in diagnostics and
// re-baselined at the next authoritative quota sync, both of which need
// the affected reservation identified.
func TestBranchC2_TerminalOnlyAtRetryBoundary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-janitor-c2")
	insertAccount(t, db, "acct-janitor-c2", "prov-janitor-c2")
	seedWindowFull(t, db, "win-janitor-c2", "acct-janitor-c2", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 5, 1)
	dispatchedAt := int64(500)
	seedJanitorReservation(t, db, "res-c2", "acct-janitor-c2", "win-janitor-c2", "reconciliation_pending", &dispatchedAt, 1000, 400, 5)

	policy := quota.DefaultReconciliationPolicy()
	if _, err := db.Conn().Exec(`UPDATE quota_reservations SET reconcile_attempts = ? WHERE id = ?`, policy.MaxRetries, "res-c2"); err != nil {
		t.Fatalf("seed reconcile_attempts at the boundary: %v", err)
	}

	repo := NewQuotaLifecycleRepo(db, fixedQuotaClock(2000), NewAuditEventRepo(db)).WithPolicy(policy)
	result, err := repo.Janitor(ctx)
	if err != nil {
		t.Fatalf("Janitor: %v", err)
	}
	if result.UnknownConsumption != 1 || result.Reclaimed != 0 {
		t.Fatalf("Janitor() = %+v, want {UnknownConsumption:1, Reclaimed:0}", result)
	}

	state, settledAt := readReservationState(t, db, "res-c2")
	if state != "unknown_consumption" || !settledAt.Valid {
		t.Fatalf("reservation (state=%s settledAt.Valid=%v), want (unknown_consumption, true)", state, settledAt.Valid)
	}
	allocState, _ := readAllocation(t, db, "res-c2", "win-janitor-c2")
	if allocState != "unknown_consumption" {
		t.Fatalf("allocation state = %q, want unknown_consumption", allocState)
	}
	_, _, reserved, _ := readWindowFull(t, db, "win-janitor-c2")
	if reserved != 5 {
		t.Fatalf("window reserved = %v, want 5 (still debited — usage gap never silently discarded)", reserved)
	}

	var n int
	if err := db.Conn().QueryRow(
		`SELECT COUNT(*) FROM audit_events WHERE action = ? AND entity_id = ?`,
		AuditActionUsageGap, "res-c2",
	).Scan(&n); err != nil {
		t.Fatalf("count usage_gap rows: %v", err)
	}
	if n != 1 {
		t.Fatalf("usage_gap rows = %d, want exactly 1 (per-reservation, not a summary count)", n)
	}
}

// TestBranchC_EmitsUsageGapPerReservation proves the per-reservation
// (not a summary count) shape of Branch C2's usage_gap event across
// MULTIPLE reservations reaching the boundary in the same sweep.
func TestBranchC_EmitsUsageGapPerReservation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-janitor-gap")
	insertAccount(t, db, "acct-janitor-gap", "prov-janitor-gap")
	seedWindowFull(t, db, "win-janitor-gap", "acct-janitor-gap", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 10, 1)
	dispatchedAt := int64(500)
	seedJanitorReservation(t, db, "res-gap-1", "acct-janitor-gap", "win-janitor-gap", "reconciliation_pending", &dispatchedAt, 1000, 400, 5)
	seedJanitorReservation(t, db, "res-gap-2", "acct-janitor-gap", "win-janitor-gap", "reconciliation_pending", &dispatchedAt, 1001, 400, 5)

	policy := quota.DefaultReconciliationPolicy()
	for _, id := range []string{"res-gap-1", "res-gap-2"} {
		if _, err := db.Conn().Exec(`UPDATE quota_reservations SET reconcile_attempts = ? WHERE id = ?`, policy.MaxRetries, id); err != nil {
			t.Fatalf("seed reconcile_attempts for %s: %v", id, err)
		}
	}

	repo := NewQuotaLifecycleRepo(db, fixedQuotaClock(10000), NewAuditEventRepo(db)).WithPolicy(policy)
	result, err := repo.Janitor(ctx)
	if err != nil {
		t.Fatalf("Janitor: %v", err)
	}
	if result.UnknownConsumption != 2 {
		t.Fatalf("UnknownConsumption = %d, want 2", result.UnknownConsumption)
	}

	for _, id := range []string{"res-gap-1", "res-gap-2"} {
		var n int
		if err := db.Conn().QueryRow(
			`SELECT COUNT(*) FROM audit_events WHERE action = ? AND entity_id = ?`,
			AuditActionUsageGap, id,
		).Scan(&n); err != nil {
			t.Fatalf("count usage_gap rows for %s: %v", id, err)
		}
		if n != 1 {
			t.Fatalf("usage_gap rows for %s = %d, want exactly 1 (per-reservation, not a summary count)", id, n)
		}
	}
}

// TestJanitorAndWorkerShareOneTerminalBoundary proves the janitor and
// the reconciliation worker can never disagree about the terminal
// boundary, because both call quota.RetryExhausted and nothing else.
// Below the boundary (attempts=MaxRetries-1), a janitor sweep leaves the
// reservation as reconciliation_pending (refuses to terminalize); the
// SAME reservation, reconciled by a worker whose own increment reaches
// MaxRetries, DOES terminalize — proving it is the worker's increment
// crossing the boundary, not a difference in how the two decide. A
// SEPARATE reservation already AT the boundary (reconcile_attempts =
// MaxRetries, unreachable by any future ClaimPending since its own
// filter is reconcile_attempts < MaxRetries — only the janitor can act
// on it again) is terminalized by the janitor too.
func TestJanitorAndWorkerShareOneTerminalBoundary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-shared-boundary")
	insertAccount(t, db, "acct-shared-boundary", "prov-shared-boundary")
	seedWindowFull(t, db, "win-shared-boundary", "acct-shared-boundary", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 10, 1)

	policy := quota.DefaultReconciliationPolicy() // MaxRetries=5
	dispatchedAt := int64(500)

	// --- Below the boundary: attempts = MaxRetries-1 = 4. ---
	seedJanitorReservation(t, db, "res-below", "acct-shared-boundary", "win-shared-boundary", "reconciliation_pending", &dispatchedAt, 1000, 400, 5)
	if _, err := db.Conn().Exec(`UPDATE quota_reservations SET reconcile_attempts = ? WHERE id = ?`, policy.MaxRetries-1, "res-below"); err != nil {
		t.Fatalf("seed res-below attempts: %v", err)
	}

	// now must be past expires_at + BackoffFor(policy, MaxRetries-1) —
	// MaxBackoff (30m=1800s) at attempts=4 — for either mechanism to act
	// on it at all: 1000 + 1800 = 2800.
	sharedNow := int64(3000)
	janitor := NewQuotaLifecycleRepo(db, fixedQuotaClock(sharedNow), nil).WithPolicy(policy)
	resultBelow, err := janitor.Janitor(ctx)
	if err != nil {
		t.Fatalf("Janitor (below boundary): %v", err)
	}
	if resultBelow.UnknownConsumption != 0 {
		t.Fatalf("Janitor().UnknownConsumption = %d below the boundary, want 0 (janitor refuses)", resultBelow.UnknownConsumption)
	}
	state, _ := readReservationState(t, db, "res-below")
	if state != "reconciliation_pending" {
		t.Fatalf("res-below state = %q, want reconciliation_pending (janitor refused to terminalize)", state)
	}

	// The worker, on this SAME reservation, increments attempts to
	// MaxRetries (5) and DOES terminalize.
	repo := NewReconciliationRepo(db, fixedQuotaClock(sharedNow), policy, NewQuotaLifecycleRepo(db, fixedQuotaClock(sharedNow), nil), nil)
	claimed, err := repo.ClaimPending(ctx, "worker-shared", quota.DefaultLeaseTTL)
	if err != nil {
		t.Fatalf("ClaimPending: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ReservationID != "res-below" {
		t.Fatalf("claimed = %+v, want exactly [res-below]", claimed)
	}
	outcome, err := repo.ReconcileOne(ctx, "worker-shared", claimed[0])
	if err != nil {
		t.Fatalf("ReconcileOne: %v", err)
	}
	if outcome.Outcome != quota.ReservationUnknownConsumption {
		t.Fatalf("worker outcome = %q at the boundary, want unknown_consumption (worker terminalizes once its own increment crosses the boundary)", outcome.Outcome)
	}

	// --- At the boundary already: attempts = MaxRetries = 5, reached by
	// some prior mechanism, and therefore unreachable by any future
	// ClaimPending — only the janitor can act on it, and it must
	// terminalize.
	seedJanitorReservation(t, db, "res-at-boundary", "acct-shared-boundary", "win-shared-boundary", "reconciliation_pending", &dispatchedAt, 1001, 400, 5)
	if _, err := db.Conn().Exec(`UPDATE quota_reservations SET reconcile_attempts = ? WHERE id = ?`, policy.MaxRetries, "res-at-boundary"); err != nil {
		t.Fatalf("seed res-at-boundary attempts: %v", err)
	}

	resultAt, err := janitor.Janitor(ctx)
	if err != nil {
		t.Fatalf("Janitor (at boundary): %v", err)
	}
	if resultAt.UnknownConsumption != 1 {
		t.Fatalf("Janitor().UnknownConsumption = %d at the boundary, want 1 (janitor terminalizes)", resultAt.UnknownConsumption)
	}
	stateAt, _ := readReservationState(t, db, "res-at-boundary")
	if stateAt != "unknown_consumption" {
		t.Fatalf("res-at-boundary state = %q, want unknown_consumption", stateAt)
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
