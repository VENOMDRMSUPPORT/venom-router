package storage

import (
	"context"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

// seedPendingReservation seeds a reconciliation_pending reservation
// (dispatched, already past its processing deadline — the only legal way
// a real reservation ever reaches this state, per the janitor's Branch B)
// with one allocation against windowID.
func seedPendingReservation(t *testing.T, db *DB, id, accountID, windowID string, dispatchedAt, expiresAt, createdAt int64, estimatedCost float64) {
	t.Helper()
	seedJanitorReservation(t, db, id, accountID, windowID, "reconciliation_pending", &dispatchedAt, expiresAt, createdAt, estimatedCost)
}

func TestReconcileOne_Settles(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-reconcile-settle")
	insertAccount(t, db, "acct-reconcile-settle", "prov-reconcile-settle")
	seedWindowFull(t, db, "win-reconcile-settle", "acct-reconcile-settle", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 5, 1)
	// now=2000; expires_at=1000 -> 1000s elapsed, well under the default
	// policy's retry-exhaustion boundary (5 * 30s = 150s).
	seedPendingReservation(t, db, "res-reconcile-settle", "acct-reconcile-settle", "win-reconcile-settle", 950, 1900, 900, 5)

	lifecycle := NewQuotaLifecycleRepo(db, fixedQuotaClock(2000), nil)
	repo := NewReconciliationRepo(db, fixedQuotaClock(2000), quota.DefaultReconciliationPolicy(), lifecycle, nil)

	pending := PendingReservation{ReservationID: "res-reconcile-settle", AccountID: "acct-reconcile-settle", ExpiresAt: 1900}
	outcome, err := repo.ReconcileOne(ctx, pending)
	if err != nil {
		t.Fatalf("ReconcileOne: %v", err)
	}
	if outcome.Outcome != quota.ReservationSettled {
		t.Fatalf("outcome.Outcome = %q, want settled", outcome.Outcome)
	}
	state, settledAt := readReservationState(t, db, "res-reconcile-settle")
	if state != "settled" || !settledAt.Valid {
		t.Fatalf("reservation (state=%s settledAt.Valid=%v), want (settled, true)", state, settledAt.Valid)
	}
	_, _, reserved, _ := readWindowFull(t, db, "win-reconcile-settle")
	if reserved != 0 {
		t.Fatalf("window reserved = %v, want 0 (settled at its own estimate)", reserved)
	}
}

func TestReconcileOne_Idempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-reconcile-idem")
	insertAccount(t, db, "acct-reconcile-idem", "prov-reconcile-idem")
	seedWindowFull(t, db, "win-reconcile-idem", "acct-reconcile-idem", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 5, 1)
	seedPendingReservation(t, db, "res-reconcile-idem", "acct-reconcile-idem", "win-reconcile-idem", 950, 1900, 900, 5)

	lifecycle := NewQuotaLifecycleRepo(db, fixedQuotaClock(2000), nil)
	repo := NewReconciliationRepo(db, fixedQuotaClock(2000), quota.DefaultReconciliationPolicy(), lifecycle, nil)
	pending := PendingReservation{ReservationID: "res-reconcile-idem", AccountID: "acct-reconcile-idem", ExpiresAt: 1900}

	first, err := repo.ReconcileOne(ctx, pending)
	if err != nil {
		t.Fatalf("first ReconcileOne: %v", err)
	}
	before := snapshotWindow(t, db, "win-reconcile-idem")

	second, err := repo.ReconcileOne(ctx, pending)
	if err != nil {
		t.Fatalf("second ReconcileOne: %v, want success (no-op)", err)
	}
	if second.Outcome != first.Outcome {
		t.Fatalf("second outcome = %q, want %q (same as first)", second.Outcome, first.Outcome)
	}
	after := snapshotWindow(t, db, "win-reconcile-idem")
	if !after.equal(before) {
		t.Fatalf("second ReconcileOne changed the window: before=%+v after=%+v", before, after)
	}
}

func TestReconcileOne_TerminalRetryBoundary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-reconcile-terminal")
	insertAccount(t, db, "acct-reconcile-terminal", "prov-reconcile-terminal")
	seedWindowFull(t, db, "win-reconcile-terminal", "acct-reconcile-terminal", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 5, 1)
	// now=100000; expires_at=1000 -> 99000s elapsed, far past the default
	// policy's retry-exhaustion boundary (5 * 30s = 150s).
	seedPendingReservation(t, db, "res-reconcile-terminal", "acct-reconcile-terminal", "win-reconcile-terminal", 950, 1000, 900, 5)

	lifecycle := NewQuotaLifecycleRepo(db, fixedQuotaClock(100000), nil)
	repo := NewReconciliationRepo(db, fixedQuotaClock(100000), quota.DefaultReconciliationPolicy(), lifecycle, nil)
	pending := PendingReservation{ReservationID: "res-reconcile-terminal", AccountID: "acct-reconcile-terminal", ExpiresAt: 1000}

	outcome, err := repo.ReconcileOne(ctx, pending)
	if err != nil {
		t.Fatalf("ReconcileOne: %v", err)
	}
	if outcome.Outcome != quota.ReservationUnknownConsumption {
		t.Fatalf("outcome.Outcome = %q, want unknown_consumption", outcome.Outcome)
	}
	state, settledAt := readReservationState(t, db, "res-reconcile-terminal")
	if state != "unknown_consumption" || !settledAt.Valid {
		t.Fatalf("reservation (state=%s settledAt.Valid=%v), want (unknown_consumption, true)", state, settledAt.Valid)
	}
	_, _, reserved, _ := readWindowFull(t, db, "win-reconcile-terminal")
	if reserved != 5 {
		t.Fatalf("window reserved = %v, want 5 (still debited — usage gap never discarded)", reserved)
	}
}

func TestPendingReservations_ReturnsOnlyPendingPastDeadlineOrderedAndBatched(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-pending-list")
	insertAccount(t, db, "acct-pending-list", "prov-pending-list")
	seedWindowFull(t, db, "win-pending-list", "acct-pending-list", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 5, 1)

	seedPendingReservation(t, db, "res-pending-2", "acct-pending-list", "win-pending-list", 950, 1200, 900, 1)
	seedPendingReservation(t, db, "res-pending-1", "acct-pending-list", "win-pending-list", 950, 1100, 900, 1)
	// A reserved (not yet pending) row must never be returned.
	seedJanitorReservation(t, db, "res-not-pending", "acct-pending-list", "win-pending-list", "reserved", nil, 1100, 900, 1)

	policy := quota.DefaultReconciliationPolicy()
	policy.BatchSize = 1
	repo := NewReconciliationRepo(db, fixedQuotaClock(2000), policy, NewQuotaLifecycleRepo(db, fixedQuotaClock(2000), nil), nil)

	pending, err := repo.PendingReservations(ctx)
	if err != nil {
		t.Fatalf("PendingReservations: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("len(pending) = %d, want 1 (BatchSize=1)", len(pending))
	}
	if pending[0].ReservationID != "res-pending-1" {
		t.Fatalf("pending[0].ReservationID = %q, want res-pending-1 (earliest expires_at first)", pending[0].ReservationID)
	}
}

func TestPendingReservation_LoadAllocations(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-pending-allocs")
	insertAccount(t, db, "acct-pending-allocs", "prov-pending-allocs")
	seedWindowFull(t, db, "win-pending-allocs", "acct-pending-allocs", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 7, 1)
	seedPendingReservation(t, db, "res-pending-allocs", "acct-pending-allocs", "win-pending-allocs", 950, 1900, 900, 7)

	pending := PendingReservation{ReservationID: "res-pending-allocs"}
	allocs, err := pending.LoadAllocations(ctx, db)
	if err != nil {
		t.Fatalf("LoadAllocations: %v", err)
	}
	if len(allocs) != 1 || allocs[0].WindowID != "win-pending-allocs" || allocs[0].Estimated != 7 {
		t.Fatalf("allocs = %+v, want [{win-pending-allocs 7}]", allocs)
	}
}
