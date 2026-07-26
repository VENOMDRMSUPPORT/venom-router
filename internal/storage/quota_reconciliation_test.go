package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

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

// NOTE (P3b-FIX-LEASE): TestReconcileOne_Idempotent (the old three-arg
// ReconcileOne(ctx, pending) shape) was REMOVED, not just rewritten. Its
// premise — calling ReconcileOne twice with the same hand-built
// PendingReservation succeeds as a no-op both times — no longer holds
// once ReconcileOne requires a held lease: the first call clears the
// lease on its terminal outcome, so a second call with the same owner
// and the SAME stale snapshot now correctly returns
// quota.ErrLeaseNotHeld rather than silently no-oping. That is exactly
// TestReconcileOne_RequiresTheLease's scenario below, and the real
// "call it again for the same logical unit of work" path is
// TestReconcileOne_IncrementsAttempts, which drives a full
// claim/reconcile cycle twice. This is a disclosed removal, not a
// silent narrowing.

func TestReconcileOne_Settles(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-reconcile-settle")
	insertAccount(t, db, "acct-reconcile-settle", "prov-reconcile-settle")
	seedWindowFull(t, db, "win-reconcile-settle", "acct-reconcile-settle", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 5, 1)
	// now=2000; expires_at=1900, first-attempt backoff (attempts=0) is
	// 30s, so it is claimable (1900+30 <= 2000); reconcile_attempts=0 is
	// far below the default policy's MaxRetries (5), so one attempt
	// (->1) is not retry-exhausted.
	seedPendingReservation(t, db, "res-reconcile-settle", "acct-reconcile-settle", "win-reconcile-settle", 950, 1900, 900, 5)

	lifecycle := NewQuotaLifecycleRepo(db, fixedQuotaClock(2000), nil)
	repo := NewReconciliationRepo(db, fixedQuotaClock(2000), quota.DefaultReconciliationPolicy(), lifecycle, nil)

	claimed, err := repo.ClaimPending(ctx, "worker-settle", quota.DefaultLeaseTTL)
	if err != nil {
		t.Fatalf("ClaimPending: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("len(claimed) = %d, want 1", len(claimed))
	}

	outcome, err := repo.ReconcileOne(ctx, "worker-settle", claimed[0])
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

	var leaseOwner sql.NullString
	if err := db.Conn().QueryRow(`SELECT lease_owner FROM quota_reservations WHERE id = ?`, "res-reconcile-settle").Scan(&leaseOwner); err != nil {
		t.Fatalf("read lease_owner: %v", err)
	}
	if leaseOwner.Valid {
		t.Fatalf("lease_owner = %q, want NULL (cleared on terminal outcome)", leaseOwner.String)
	}
}

func TestReconcileOne_TerminalRetryBoundary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-reconcile-terminal")
	insertAccount(t, db, "acct-reconcile-terminal", "prov-reconcile-terminal")
	seedWindowFull(t, db, "win-reconcile-terminal", "acct-reconcile-terminal", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 5, 1)
	seedPendingReservation(t, db, "res-reconcile-terminal", "acct-reconcile-terminal", "win-reconcile-terminal", 950, 1000, 900, 5)

	policy := quota.DefaultReconciliationPolicy() // MaxRetries=5
	// Force this reservation to its LAST retry: one more attempt (this
	// one) crosses quota.RetryExhausted's boundary.
	if _, err := db.Conn().Exec(`UPDATE quota_reservations SET reconcile_attempts = ? WHERE id = ?`, policy.MaxRetries-1, "res-reconcile-terminal"); err != nil {
		t.Fatalf("seed reconcile_attempts: %v", err)
	}

	lifecycle := NewQuotaLifecycleRepo(db, fixedQuotaClock(100000), nil)
	repo := NewReconciliationRepo(db, fixedQuotaClock(100000), policy, lifecycle, nil)

	claimed, err := repo.ClaimPending(ctx, "worker-terminal", quota.DefaultLeaseTTL)
	if err != nil {
		t.Fatalf("ClaimPending: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("len(claimed) = %d, want 1", len(claimed))
	}

	outcome, err := repo.ReconcileOne(ctx, "worker-terminal", claimed[0])
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

// TestReconcileOne_TerminalRetryBoundary_EmitsUsageGap pins the other half
// of 02 §3's usage_gap requirement: the WORKER's terminal path must record
// it too, not just the janitor's. Without this, a reservation whose retry
// budget the worker exhausts would reach unknown_consumption with no
// record of the gap anywhere.
func TestReconcileOne_TerminalRetryBoundary_EmitsUsageGap(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-gap-worker")
	insertAccount(t, db, "acct-gap-worker", "prov-gap-worker")
	seedWindowFull(t, db, "win-gap-worker", "acct-gap-worker", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 5, 1)
	seedPendingReservation(t, db, "res-gap-worker", "acct-gap-worker", "win-gap-worker", 950, 1000, 900, 5)

	policy := quota.DefaultReconciliationPolicy()
	if _, err := db.Conn().Exec(`UPDATE quota_reservations SET reconcile_attempts = ? WHERE id = ?`, policy.MaxRetries-1, "res-gap-worker"); err != nil {
		t.Fatalf("seed reconcile_attempts: %v", err)
	}

	audit := NewAuditEventRepo(db)
	lifecycle := NewQuotaLifecycleRepo(db, fixedQuotaClock(100000), audit)
	repo := NewReconciliationRepo(db, fixedQuotaClock(100000), policy, lifecycle, audit)

	claimed, err := repo.ClaimPending(ctx, "worker-gap", quota.DefaultLeaseTTL)
	if err != nil {
		t.Fatalf("ClaimPending: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("len(claimed) = %d, want 1", len(claimed))
	}

	outcome, err := repo.ReconcileOne(ctx, "worker-gap", claimed[0])
	if err != nil {
		t.Fatalf("ReconcileOne: %v", err)
	}
	if outcome.Outcome != quota.ReservationUnknownConsumption {
		t.Fatalf("outcome = %q, want unknown_consumption", outcome.Outcome)
	}

	var n int
	if err := db.Conn().QueryRow(
		`SELECT COUNT(*) FROM audit_events WHERE action = ? AND entity_id = ?`,
		AuditActionUsageGap, "res-gap-worker",
	).Scan(&n); err != nil {
		t.Fatalf("count usage_gap rows: %v", err)
	}
	if n != 1 {
		t.Fatalf("usage_gap rows = %d, want exactly 1", n)
	}
}

// TestClaimPending_LeasesAndExcludesAlreadyLeased proves both directions
// of the lease contract in one test: a second worker claiming
// immediately after the first gets nothing, and the SAME worker (or any
// worker) claims it again once the first lease has expired.
func TestClaimPending_LeasesAndExcludesAlreadyLeased(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-claim-lease")
	insertAccount(t, db, "acct-claim-lease", "prov-claim-lease")
	seedWindowFull(t, db, "win-claim-lease", "acct-claim-lease", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 5, 1)
	seedPendingReservation(t, db, "res-claim-lease", "acct-claim-lease", "win-claim-lease", 950, 1000, 900, 5)

	policy := quota.DefaultReconciliationPolicy()
	claimTime := int64(2000)
	repo := NewReconciliationRepo(db, fixedQuotaClock(claimTime), policy, NewQuotaLifecycleRepo(db, fixedQuotaClock(claimTime), nil), nil)

	claimedA, err := repo.ClaimPending(ctx, "worker-a", quota.DefaultLeaseTTL)
	if err != nil {
		t.Fatalf("worker-a ClaimPending: %v", err)
	}
	if len(claimedA) != 1 {
		t.Fatalf("worker-a claimed = %d, want 1", len(claimedA))
	}

	claimedB, err := repo.ClaimPending(ctx, "worker-b", quota.DefaultLeaseTTL)
	if err != nil {
		t.Fatalf("worker-b ClaimPending: %v", err)
	}
	if len(claimedB) != 0 {
		t.Fatalf("worker-b claimed = %d, want 0 (already leased by worker-a)", len(claimedB))
	}

	// Advance the clock past DefaultLeaseTTL (5m = 300s): lease_expires_at
	// was claimTime+300=2300, so 2301 is past it.
	afterLeaseExpiry := claimTime + int64(quota.DefaultLeaseTTL.Seconds()) + 1
	repoLater := NewReconciliationRepo(db, fixedQuotaClock(afterLeaseExpiry), policy, NewQuotaLifecycleRepo(db, fixedQuotaClock(afterLeaseExpiry), nil), nil)
	claimedC, err := repoLater.ClaimPending(ctx, "worker-c", quota.DefaultLeaseTTL)
	if err != nil {
		t.Fatalf("worker-c ClaimPending: %v", err)
	}
	if len(claimedC) != 1 || claimedC[0].ReservationID != "res-claim-lease" {
		t.Fatalf("worker-c claimed %+v, want exactly [res-claim-lease] (lease now expired)", claimedC)
	}
}

// TestClaimPending_RespectsBackoff proves both sides of the backoff
// boundary for a reservation on its second attempt (BackoffFor(...,1) =
// 5m): invisible one second before expires_at+5m, visible at exactly
// that instant.
func TestClaimPending_RespectsBackoff(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-claim-backoff")
	insertAccount(t, db, "acct-claim-backoff", "prov-claim-backoff")
	seedWindowFull(t, db, "win-claim-backoff", "acct-claim-backoff", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 5, 1)
	seedPendingReservation(t, db, "res-claim-backoff", "acct-claim-backoff", "win-claim-backoff", 950, 1000, 900, 5)
	if _, err := db.Conn().Exec(`UPDATE quota_reservations SET reconcile_attempts = 1 WHERE id = ?`, "res-claim-backoff"); err != nil {
		t.Fatalf("seed reconcile_attempts: %v", err)
	}

	policy := quota.DefaultReconciliationPolicy() // BackoffFor(policy, 1) = 5m = 300s

	before := NewReconciliationRepo(db, fixedQuotaClock(1299), policy, NewQuotaLifecycleRepo(db, fixedQuotaClock(1299), nil), nil)
	claimedBefore, err := before.ClaimPending(ctx, "worker-early", quota.DefaultLeaseTTL)
	if err != nil {
		t.Fatalf("ClaimPending before backoff elapsed: %v", err)
	}
	if len(claimedBefore) != 0 {
		t.Fatalf("claimed before backoff elapsed = %d, want 0", len(claimedBefore))
	}

	atBoundary := NewReconciliationRepo(db, fixedQuotaClock(1300), policy, NewQuotaLifecycleRepo(db, fixedQuotaClock(1300), nil), nil)
	claimedAfter, err := atBoundary.ClaimPending(ctx, "worker-late", quota.DefaultLeaseTTL)
	if err != nil {
		t.Fatalf("ClaimPending at backoff boundary: %v", err)
	}
	if len(claimedAfter) != 1 {
		t.Fatalf("claimed at backoff boundary = %d, want 1", len(claimedAfter))
	}
}

// TestClaimPending_SkipsRetryExhausted proves an item already at
// reconcile_attempts = MaxRetries is never claimed by ClaimPending —
// that item belongs exclusively to the janitor's Branch C2.
func TestClaimPending_SkipsRetryExhausted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-claim-exhausted")
	insertAccount(t, db, "acct-claim-exhausted", "prov-claim-exhausted")
	seedWindowFull(t, db, "win-claim-exhausted", "acct-claim-exhausted", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 5, 1)
	seedPendingReservation(t, db, "res-claim-exhausted", "acct-claim-exhausted", "win-claim-exhausted", 950, 1000, 900, 5)

	policy := quota.DefaultReconciliationPolicy()
	if _, err := db.Conn().Exec(`UPDATE quota_reservations SET reconcile_attempts = ? WHERE id = ?`, policy.MaxRetries, "res-claim-exhausted"); err != nil {
		t.Fatalf("seed reconcile_attempts: %v", err)
	}

	// Far in the future so backoff elapsed is never the reason for
	// exclusion — only the retry-exhaustion filter is being isolated here.
	repo := NewReconciliationRepo(db, fixedQuotaClock(1000000), policy, NewQuotaLifecycleRepo(db, fixedQuotaClock(1000000), nil), nil)
	claimed, err := repo.ClaimPending(ctx, "worker-exhausted", quota.DefaultLeaseTTL)
	if err != nil {
		t.Fatalf("ClaimPending: %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claimed = %d, want 0 (retry-exhausted; belongs to the janitor's Branch C2)", len(claimed))
	}
}

// TestReconcileOne_RequiresTheLease proves calling ReconcileOne with the
// WRONG owner is rejected with quota.ErrLeaseNotHeld and leaves state,
// allocations, and reconcile_attempts completely unchanged — a worker
// whose lease was reassigned must never settle.
func TestReconcileOne_RequiresTheLease(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-wrong-owner")
	insertAccount(t, db, "acct-wrong-owner", "prov-wrong-owner")
	seedWindowFull(t, db, "win-wrong-owner", "acct-wrong-owner", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 5, 1)
	seedPendingReservation(t, db, "res-wrong-owner", "acct-wrong-owner", "win-wrong-owner", 950, 1900, 900, 5)

	policy := quota.DefaultReconciliationPolicy()
	lifecycle := NewQuotaLifecycleRepo(db, fixedQuotaClock(2000), nil)
	repo := NewReconciliationRepo(db, fixedQuotaClock(2000), policy, lifecycle, nil)

	claimed, err := repo.ClaimPending(ctx, "worker-real", quota.DefaultLeaseTTL)
	if err != nil {
		t.Fatalf("ClaimPending: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("len(claimed) = %d, want 1", len(claimed))
	}

	if _, err := repo.ReconcileOne(ctx, "worker-impostor", claimed[0]); !errors.Is(err, quota.ErrLeaseNotHeld) {
		t.Fatalf("ReconcileOne with wrong owner error = %v, want quota.ErrLeaseNotHeld", err)
	}

	state, _ := readReservationState(t, db, "res-wrong-owner")
	if state != "reconciliation_pending" {
		t.Fatalf("state = %q, want reconciliation_pending (unchanged)", state)
	}
	allocState, _ := readAllocation(t, db, "res-wrong-owner", "win-wrong-owner")
	if allocState != "reserved" {
		t.Fatalf("allocation state = %q, want reserved (unchanged)", allocState)
	}
	var attempts int64
	if err := db.Conn().QueryRow(`SELECT reconcile_attempts FROM quota_reservations WHERE id = ?`, "res-wrong-owner").Scan(&attempts); err != nil {
		t.Fatalf("read reconcile_attempts: %v", err)
	}
	if attempts != 0 {
		t.Fatalf("reconcile_attempts = %d, want 0 (unchanged)", attempts)
	}
}

// TestReconcileOne_IncrementsAttempts drives an item that keeps failing
// (re-pended by raw SQL after each settle, standing in for whatever
// production mechanism would re-surface it) through two full
// claim/reconcile cycles, proving reconcile_attempts accumulates 0->1->2
// rather than resetting or staying fixed.
func TestReconcileOne_IncrementsAttempts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-attempts")
	insertAccount(t, db, "acct-attempts", "prov-attempts")
	seedWindowFull(t, db, "win-attempts", "acct-attempts", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 5, 1)
	seedPendingReservation(t, db, "res-attempts", "acct-attempts", "win-attempts", 950, 1000, 900, 5)

	policy := quota.DefaultReconciliationPolicy()
	now := int64(1000000) // far past any backoff, for every round
	lifecycle := NewQuotaLifecycleRepo(db, fixedQuotaClock(now), nil)
	repo := NewReconciliationRepo(db, fixedQuotaClock(now), policy, lifecycle, nil)

	for round := 0; round < 2; round++ {
		wantAttempts := int64(round + 1)
		owner := fmt.Sprintf("worker-round-%d", round)

		claimed, err := repo.ClaimPending(ctx, owner, quota.DefaultLeaseTTL)
		if err != nil {
			t.Fatalf("round %d ClaimPending: %v", round, err)
		}
		if len(claimed) != 1 {
			t.Fatalf("round %d claimed = %d, want 1", round, len(claimed))
		}

		if _, err := repo.ReconcileOne(ctx, owner, claimed[0]); err != nil {
			t.Fatalf("round %d ReconcileOne: %v", round, err)
		}

		var attempts int64
		if err := db.Conn().QueryRow(`SELECT reconcile_attempts FROM quota_reservations WHERE id = ?`, "res-attempts").Scan(&attempts); err != nil {
			t.Fatalf("round %d read reconcile_attempts: %v", round, err)
		}
		if attempts != wantAttempts {
			t.Fatalf("round %d reconcile_attempts = %d, want %d", round, attempts, wantAttempts)
		}

		if _, err := db.Conn().Exec(`UPDATE quota_reservations SET state = 'reconciliation_pending' WHERE id = ?`, "res-attempts"); err != nil {
			t.Fatalf("round %d re-pend: %v", round, err)
		}
	}
}

// TestWorkerCrash_LeaseExpiryReclaims is the named card test for
// P3b-QUOTA-006's lease requirement: a worker claims a reservation and
// then crashes before ever calling ReconcileOne. Once the lease expires,
// the janitor must reclaim it — clear the lease so the NEXT
// ClaimPending can re-pick it — WITHOUT terminalizing it, without
// touching its allocations or window headroom, and without moving
// reconcile_attempts (reclaiming is not an attempt).
// TestReconcileOne_FlagsRebaselineOnBothPaths proves both of ReconcileOne's
// outcomes leave the account flagged for re-baseline (05 §4: "flag the
// account for re-baseline at the next quota sync"), each with its own
// distinct reason code: estimate_settled_low_confidence for the
// low-confidence settle path, usage_gap for the terminal path.
func TestReconcileOne_FlagsRebaselineOnBothPaths(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	policy := quota.DefaultReconciliationPolicy()

	t.Run("low-confidence settle path", func(t *testing.T) {
		insertProvider(t, db, "prov-flag-settle")
		insertAccount(t, db, "acct-flag-settle", "prov-flag-settle")
		seedWindowFull(t, db, "win-flag-settle", "acct-flag-settle", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 5, 1)
		seedPendingReservation(t, db, "res-flag-settle", "acct-flag-settle", "win-flag-settle", 950, 1900, 900, 5)

		repo := NewReconciliationRepo(db, fixedQuotaClock(2000), policy, NewQuotaLifecycleRepo(db, fixedQuotaClock(2000), nil), nil)
		claimed, err := repo.ClaimPending(ctx, "worker-flag-settle", quota.DefaultLeaseTTL)
		if err != nil || len(claimed) != 1 {
			t.Fatalf("ClaimPending: claimed=%v err=%v", claimed, err)
		}
		if _, err := repo.ReconcileOne(ctx, "worker-flag-settle", claimed[0]); err != nil {
			t.Fatalf("ReconcileOne: %v", err)
		}

		var reasonCode string
		if err := db.Conn().QueryRow(`SELECT reason_code FROM quota_rebaseline_flags WHERE account_id = ?`, "acct-flag-settle").Scan(&reasonCode); err != nil {
			t.Fatalf("read rebaseline flag: %v", err)
		}
		if reasonCode != "estimate_settled_low_confidence" {
			t.Fatalf("reason_code = %q, want estimate_settled_low_confidence", reasonCode)
		}
	})

	t.Run("terminal path", func(t *testing.T) {
		insertProvider(t, db, "prov-flag-terminal")
		insertAccount(t, db, "acct-flag-terminal", "prov-flag-terminal")
		seedWindowFull(t, db, "win-flag-terminal", "acct-flag-terminal", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 5, 1)
		seedPendingReservation(t, db, "res-flag-terminal", "acct-flag-terminal", "win-flag-terminal", 950, 1000, 900, 5)
		if _, err := db.Conn().Exec(`UPDATE quota_reservations SET reconcile_attempts = ? WHERE id = ?`, policy.MaxRetries-1, "res-flag-terminal"); err != nil {
			t.Fatalf("seed reconcile_attempts: %v", err)
		}

		repo := NewReconciliationRepo(db, fixedQuotaClock(100000), policy, NewQuotaLifecycleRepo(db, fixedQuotaClock(100000), nil), nil)
		claimed, err := repo.ClaimPending(ctx, "worker-flag-terminal", quota.DefaultLeaseTTL)
		if err != nil || len(claimed) != 1 {
			t.Fatalf("ClaimPending: claimed=%v err=%v", claimed, err)
		}
		if _, err := repo.ReconcileOne(ctx, "worker-flag-terminal", claimed[0]); err != nil {
			t.Fatalf("ReconcileOne: %v", err)
		}

		var reasonCode string
		if err := db.Conn().QueryRow(`SELECT reason_code FROM quota_rebaseline_flags WHERE account_id = ?`, "acct-flag-terminal").Scan(&reasonCode); err != nil {
			t.Fatalf("read rebaseline flag: %v", err)
		}
		if reasonCode != "usage_gap" {
			t.Fatalf("reason_code = %q, want usage_gap", reasonCode)
		}
	})
}

func TestWorkerCrash_LeaseExpiryReclaims(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-crash")
	insertAccount(t, db, "acct-crash", "prov-crash")
	seedWindowFull(t, db, "win-crash", "acct-crash", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 5, 1)
	seedPendingReservation(t, db, "res-crash", "acct-crash", "win-crash", 950, 1000, 900, 5)

	policy := quota.DefaultReconciliationPolicy()
	claimTime := int64(2000)
	repo := NewReconciliationRepo(db, fixedQuotaClock(claimTime), policy, NewQuotaLifecycleRepo(db, fixedQuotaClock(claimTime), nil), nil)

	claimed, err := repo.ClaimPending(ctx, "worker-crash", quota.DefaultLeaseTTL)
	if err != nil {
		t.Fatalf("ClaimPending: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed = %d, want 1", len(claimed))
	}

	beforeAllocState, _ := readAllocation(t, db, "res-crash", "win-crash")
	beforeWindow := snapshotWindow(t, db, "win-crash")
	var beforeAttempts int64
	if err := db.Conn().QueryRow(`SELECT reconcile_attempts FROM quota_reservations WHERE id = ?`, "res-crash").Scan(&beforeAttempts); err != nil {
		t.Fatalf("read reconcile_attempts before: %v", err)
	}

	// The worker crashes here — it never calls ReconcileOne. Advance the
	// clock past DefaultLeaseTTL and run the janitor.
	afterCrash := claimTime + int64(quota.DefaultLeaseTTL.Seconds()) + 1
	lifecycle := NewQuotaLifecycleRepo(db, fixedQuotaClock(afterCrash), nil).WithPolicy(policy)
	result, err := lifecycle.Janitor(ctx)
	if err != nil {
		t.Fatalf("Janitor: %v", err)
	}
	if result.Reclaimed != 1 {
		t.Fatalf("Janitor().Reclaimed = %d, want 1", result.Reclaimed)
	}
	if result.UnknownConsumption != 0 {
		t.Fatalf("Janitor().UnknownConsumption = %d, want 0 (not yet retry-exhausted)", result.UnknownConsumption)
	}

	var leaseOwner sql.NullString
	var leaseExpiresAt sql.NullInt64
	if err := db.Conn().QueryRow(`SELECT lease_owner, lease_expires_at FROM quota_reservations WHERE id = ?`, "res-crash").Scan(&leaseOwner, &leaseExpiresAt); err != nil {
		t.Fatalf("read lease columns: %v", err)
	}
	if leaseOwner.Valid || leaseExpiresAt.Valid {
		t.Fatalf("lease = (owner=%v expires=%v), want both NULL (cleared)", leaseOwner, leaseExpiresAt)
	}

	state, _ := readReservationState(t, db, "res-crash")
	if state != "reconciliation_pending" {
		t.Fatalf("state = %q, want STILL reconciliation_pending", state)
	}
	afterAllocState, _ := readAllocation(t, db, "res-crash", "win-crash")
	if afterAllocState != beforeAllocState {
		t.Fatalf("allocation state = %q, want unchanged %q", afterAllocState, beforeAllocState)
	}
	afterWindow := snapshotWindow(t, db, "win-crash")
	if !afterWindow.equal(beforeWindow) {
		t.Fatalf("window changed: before=%+v after=%+v, want byte-for-byte unchanged", beforeWindow, afterWindow)
	}
	var afterAttempts int64
	if err := db.Conn().QueryRow(`SELECT reconcile_attempts FROM quota_reservations WHERE id = ?`, "res-crash").Scan(&afterAttempts); err != nil {
		t.Fatalf("read reconcile_attempts after: %v", err)
	}
	if afterAttempts != beforeAttempts {
		t.Fatalf("reconcile_attempts = %d, want unchanged %d", afterAttempts, beforeAttempts)
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

func float64PtrRecon(v float64) *float64 { return &v }

// providerSpec is a small test-local constructor for
// quota.ProviderWindowSpec, since SyncQuotaWindows now consumes that
// type directly rather than []providers.QuotaWindow.
func providerSpec(unit quota.Unit, windowType, key string, remaining *float64, confidence float64, observedAt time.Time) quota.ProviderWindowSpec {
	return quota.ProviderWindowSpec{
		Source:     quota.SourceProviderEvidence,
		Unit:       unit,
		WindowType: windowType,
		Key:        key,
		Remaining:  remaining,
		Confidence: confidence,
		Freshness:  quota.FreshnessFresh,
		ObservedAt: observedAt,
	}
}

// TestSyncQuotaWindows_UpsertsAndMarksAbsentStale proves SyncQuotaWindows
// creates a provider_evidence window on first sync, UPDATES the SAME row
// (never a duplicate) on a second sync with different values, and marks
// exactly the window missing from the second fetch as stale while
// leaving the still-present one fresh.
func TestSyncQuotaWindows_UpsertsAndMarksAbsentStale(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-sync-upsert")
	insertAccount(t, db, "acct-sync-upsert", "prov-sync-upsert")

	repo := NewReconciliationRepo(db, fixedQuotaClock(1000), quota.DefaultReconciliationPolicy(), NewQuotaLifecycleRepo(db, fixedQuotaClock(1000), nil), nil)
	observedAt := time.Unix(1000, 0)

	first := []quota.ProviderWindowSpec{
		providerSpec(quota.UnitRequests, "rpm", "provider:rpm", float64PtrRecon(90), 0.9, observedAt),
		providerSpec(quota.UnitTokens, "tpm", "provider:tpm", float64PtrRecon(900), 0.9, observedAt),
	}
	if err := repo.SyncQuotaWindows(ctx, "acct-sync-upsert", first, nil); err != nil {
		t.Fatalf("first SyncQuotaWindows: %v", err)
	}

	var countAfterFirst int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM quota_windows WHERE account_id = ? AND source = 'provider_evidence'`, "acct-sync-upsert").Scan(&countAfterFirst); err != nil {
		t.Fatalf("count after first sync: %v", err)
	}
	if countAfterFirst != 2 {
		t.Fatalf("rows after first sync = %d, want 2", countAfterFirst)
	}

	// Second fetch: rpm's numbers change, tpm is now missing entirely.
	second := []quota.ProviderWindowSpec{
		providerSpec(quota.UnitRequests, "rpm", "provider:rpm", float64PtrRecon(80), 0.95, observedAt),
	}
	if err := repo.SyncQuotaWindows(ctx, "acct-sync-upsert", second, nil); err != nil {
		t.Fatalf("second SyncQuotaWindows: %v", err)
	}

	var countAfterSecond int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM quota_windows WHERE account_id = ? AND source = 'provider_evidence'`, "acct-sync-upsert").Scan(&countAfterSecond); err != nil {
		t.Fatalf("count after second sync: %v", err)
	}
	if countAfterSecond != 2 {
		t.Fatalf("rows after second sync = %d, want still 2 (UPSERT + stale, never a duplicate, never deleted)", countAfterSecond)
	}

	var remaining, confidence float64
	var version int64
	var rpmFreshness string
	if err := db.Conn().QueryRow(
		`SELECT remaining, confidence, version, freshness_state FROM quota_windows WHERE account_id = ? AND window_type = 'rpm'`,
		"acct-sync-upsert",
	).Scan(&remaining, &confidence, &version, &rpmFreshness); err != nil {
		t.Fatalf("read rpm window after second sync: %v", err)
	}
	if remaining != 80 || confidence != 0.95 || rpmFreshness != "fresh" {
		t.Fatalf("rpm window = (remaining=%v confidence=%v freshness=%q), want (80,0.95,fresh) — the SECOND call's values", remaining, confidence, rpmFreshness)
	}
	if version != 2 {
		t.Fatalf("rpm version = %d, want 2 (incremented on update)", version)
	}

	var tpmFreshness string
	var tpmReserved float64
	if err := db.Conn().QueryRow(`SELECT freshness_state, reserved FROM quota_windows WHERE account_id = ? AND window_type = 'tpm'`, "acct-sync-upsert").Scan(&tpmFreshness, &tpmReserved); err != nil {
		t.Fatalf("read tpm window: %v", err)
	}
	if tpmFreshness != "stale" {
		t.Fatalf("tpm freshness = %q, want stale (missing from the second fetch)", tpmFreshness)
	}
	if tpmReserved != 0 {
		t.Fatalf("tpm reserved = %v, want 0 (staleness never touches reserved)", tpmReserved)
	}
}

// TestSyncQuotaWindows_RateLimitWritesCooldownAndNeverExhausts proves a
// non-nil CooldownTrigger writes a cooldown row at the trigger's scope
// with the expected until, and that NO window is marked exhausted or has
// its numbers zeroed — both directions: a nil trigger writes no
// cooldown.
func TestSyncQuotaWindows_RateLimitWritesCooldownAndNeverExhausts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-sync-429")
	insertAccount(t, db, "acct-sync-429", "prov-sync-429")
	seedWindowFull(t, db, "win-sync-429", "acct-sync-429", "provider_evidence", "requests", "rpm", "provider:rpm", float64Ptr(90), nil, 0, 1)

	repo := NewReconciliationRepo(db, fixedQuotaClock(1000), quota.DefaultReconciliationPolicy(), NewQuotaLifecycleRepo(db, fixedQuotaClock(1000), nil), nil)

	trigger := &quota.CooldownTrigger{
		Scope: quota.CooldownScopeAccount, ScopeRef: "acct-sync-429",
		Until: time.Unix(1030, 0), Source: quota.CooldownSourceRetryAfter, ReasonCode: "rate_limited",
	}
	if err := repo.SyncQuotaWindows(ctx, "acct-sync-429", nil, trigger); err != nil {
		t.Fatalf("SyncQuotaWindows with trigger: %v", err)
	}

	var until int64
	var reasonCode, source string
	if err := db.Conn().QueryRow(`SELECT until, reason_code, source FROM cooldowns WHERE scope = 'account' AND account_id = ?`, "acct-sync-429").Scan(&until, &reasonCode, &source); err != nil {
		t.Fatalf("read cooldown: %v", err)
	}
	if until != 1030 || reasonCode != "rate_limited" || source != "retry_after" {
		t.Fatalf("cooldown = (until=%d reason=%q source=%q), want (1030, rate_limited, retry_after)", until, reasonCode, source)
	}

	// The pre-existing window must be untouched: no exhausted marker
	// exists on quota_windows, and reserved/remaining are unchanged.
	var remaining, reserved float64
	if err := db.Conn().QueryRow(`SELECT remaining, reserved FROM quota_windows WHERE id = ?`, "win-sync-429").Scan(&remaining, &reserved); err != nil {
		t.Fatalf("read window: %v", err)
	}
	if remaining != 90 || reserved != 0 {
		t.Fatalf("window = (remaining=%v reserved=%v), want (90,0) — untouched by the rate-limit trigger", remaining, reserved)
	}

	// No trigger -> no cooldown written.
	insertAccount(t, db, "acct-sync-no429", "prov-sync-429")
	if err := repo.SyncQuotaWindows(ctx, "acct-sync-no429", nil, nil); err != nil {
		t.Fatalf("SyncQuotaWindows without trigger: %v", err)
	}
	var count int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM cooldowns WHERE account_id = ?`, "acct-sync-no429").Scan(&count); err != nil {
		t.Fatalf("count cooldowns: %v", err)
	}
	if count != 0 {
		t.Fatalf("cooldowns for acct-sync-no429 = %d, want 0 (nil trigger writes nothing)", count)
	}
}

// TestSyncQuotaWindows_ClearsRebaselineFlag proves a flagged account is
// unflagged after a successful sync — this is what "re-baselined at the
// next quota sync" means operationally.
func TestSyncQuotaWindows_ClearsRebaselineFlag(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-sync-clearflag")
	insertAccount(t, db, "acct-sync-clearflag", "prov-sync-clearflag")

	repo := NewReconciliationRepo(db, fixedQuotaClock(1000), quota.DefaultReconciliationPolicy(), NewQuotaLifecycleRepo(db, fixedQuotaClock(1000), nil), nil)
	if err := repo.FlagRebaseline(ctx, "acct-sync-clearflag", "usage_gap"); err != nil {
		t.Fatalf("FlagRebaseline: %v", err)
	}

	if err := repo.SyncQuotaWindows(ctx, "acct-sync-clearflag", nil, nil); err != nil {
		t.Fatalf("SyncQuotaWindows: %v", err)
	}

	flagged, err := repo.RebaselineFlagged(ctx)
	if err != nil {
		t.Fatalf("RebaselineFlagged: %v", err)
	}
	if len(flagged) != 0 {
		t.Fatalf("RebaselineFlagged() = %v, want empty (cleared by the sync)", flagged)
	}
}

// TestStaleAccounts_ReturnsOnlyStaleProviderWindows proves an account
// owning a 20-minute-old provider_evidence window is returned, one owning
// only a 1-minute-old window is not, and a stale-in-age local_safety
// window never qualifies (local-safety windows are ours and never go
// stale for lack of a provider fetch).
func TestStaleAccounts_ReturnsOnlyStaleProviderWindows(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-stale-accounts")
	insertAccount(t, db, "acct-stale-old", "prov-stale-accounts")
	insertAccount(t, db, "acct-stale-fresh", "prov-stale-accounts")
	insertAccount(t, db, "acct-stale-localsafety", "prov-stale-accounts")

	now := int64(100000)
	// 20 minutes old.
	if _, err := db.Conn().Exec(
		`INSERT INTO quota_windows (id, account_id, source, unit, window_type, window_key, reserved, version, confidence, freshness_state, observed_at, created_at, updated_at)
		 VALUES (?, ?, 'provider_evidence', 'requests', 'rpm', 'provider:rpm', 0, 1, 0.9, 'fresh', ?, ?, ?)`,
		"win-stale-old", "acct-stale-old", now-20*60, now-20*60, now-20*60,
	); err != nil {
		t.Fatalf("seed old provider window: %v", err)
	}
	// 1 minute old.
	if _, err := db.Conn().Exec(
		`INSERT INTO quota_windows (id, account_id, source, unit, window_type, window_key, reserved, version, confidence, freshness_state, observed_at, created_at, updated_at)
		 VALUES (?, ?, 'provider_evidence', 'requests', 'rpm', 'provider:rpm', 0, 1, 0.9, 'fresh', ?, ?, ?)`,
		"win-stale-fresh", "acct-stale-fresh", now-60, now-60, now-60,
	); err != nil {
		t.Fatalf("seed fresh provider window: %v", err)
	}
	// A 20-minute-old LOCAL_SAFETY window must never qualify.
	if _, err := db.Conn().Exec(
		`INSERT INTO quota_windows (id, account_id, source, unit, window_type, window_key, reserved, version, confidence, freshness_state, observed_at, created_at, updated_at)
		 VALUES (?, ?, 'local_safety', 'concurrency', 'concurrency', 'local:concurrency', 0, 1, 1, 'fresh', ?, ?, ?)`,
		"win-stale-localsafety", "acct-stale-localsafety", now-20*60, now-20*60, now-20*60,
	); err != nil {
		t.Fatalf("seed local-safety window: %v", err)
	}

	repo := NewReconciliationRepo(db, fixedQuotaClock(now), quota.DefaultReconciliationPolicy(), NewQuotaLifecycleRepo(db, fixedQuotaClock(now), nil), nil)
	stale, err := repo.StaleAccounts(ctx, time.Unix(now, 0).Add(-quota.DefaultStalenessWindow))
	if err != nil {
		t.Fatalf("StaleAccounts: %v", err)
	}
	if len(stale) != 1 || stale[0] != "acct-stale-old" {
		t.Fatalf("StaleAccounts() = %v, want exactly [acct-stale-old]", stale)
	}
}
