package storage

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

// Janitor recovers reservations stuck past their processing deadline
// (02 §3) in ONE BEGIN IMMEDIATE transaction on a dedicated connection,
// discriminating strictly on dispatched_at (branches A/B) and on
// quota.RetryExhausted (branches C1/C2) — never on state='reserved'
// alone, and never on a wall-clock deadline for the reconciliation_pending
// branches:
//
//  1. Branch A: never dispatched, past its processing deadline ->
//     released, with the SAME window arithmetic QuotaLifecycleRepo.Release
//     applies (headroom is freed, never left leaking).
//  2. Branch B: dispatched, past its processing deadline, still reserved
//     -> reconciliation_pending (the provider call was made but its
//     result never landed, e.g. a crash mid-flight; headroom stays
//     debited until reconciliation resolves it).
//  3. Branch C1: reconciliation_pending, NOT quota.RetryExhausted, and
//     its lease is expired or absent -> the lease is cleared (RECLAIMED)
//     so the next ReconciliationRepo.ClaimPending re-picks it. State,
//     allocations, window arithmetic, and reconcile_attempts are all
//     UNTOUCHED — reclaiming is not an attempt, only a hand-off. A
//     reservation whose lease is still held by a live worker is left
//     alone entirely (neither reclaimed nor terminalized).
//  4. Branch C2: reconciliation_pending AND quota.RetryExhausted ->
//     unknown_consumption (terminal; headroom stays debited — the usage
//     gap is never silently discarded), regardless of lease state.
//
// All branches run on the SAME connection inside the SAME transaction —
// this method never acquires a second connection (SetMaxOpenConns(1)) —
// so one sweep is itself all-or-nothing. Audit rows (one per non-empty
// branch, when an audit sink is configured) are emitted AFTER
// janitorSweep has fully returned — i.e. after its deferred conn.Close()
// has already run — never while the transaction's connection is still
// checked out. Emitting them from INSIDE janitorSweep, before its own
// defer runs, would be exactly the deadlock applyTransition's
// emitAudit-after-commit split guards against: the pool's one connection
// would still be held, and audit's own r.db.Conn() call would block
// forever waiting for a second one that never comes.
func (r *QuotaLifecycleRepo) Janitor(ctx context.Context) (quota.JanitorResult, error) {
	result, usageGapIDs, err := r.janitorSweep(ctx)
	if err != nil {
		return quota.JanitorResult{}, err
	}
	r.emitJanitorAudit(ctx, "released", result.Released)
	r.emitJanitorAudit(ctx, "pended", result.Pended)
	r.emitJanitorAudit(ctx, "reclaimed", result.Reclaimed)
	// Branch C2 is the terminal retry boundary, which 02 §3 requires to
	// record a usage_gap event PER RESERVATION — a summary count alone
	// cannot tell the owner (or the reconciliation diagnostics surface,
	// P3b-CAPI-002) WHICH reservations have an unaccounted-for cost.
	for _, id := range usageGapIDs {
		r.emitUsageGap(ctx, id)
	}
	return result, nil
}

func (r *QuotaLifecycleRepo) janitorSweep(ctx context.Context) (quota.JanitorResult, []string, error) {
	conn, err := r.db.Conn().Conn(ctx)
	if err != nil {
		return quota.JanitorResult{}, nil, fmt.Errorf("storage: acquire connection for janitor: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return quota.JanitorResult{}, nil, fmt.Errorf("storage: begin immediate janitor tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	now := r.now().Unix()

	released, err := janitorReleaseNeverDispatched(ctx, conn, now, quota.DefaultJanitorBatchSize)
	if err != nil {
		return quota.JanitorResult{}, nil, err
	}
	pended, err := janitorPendDispatched(ctx, conn, now, quota.DefaultJanitorBatchSize)
	if err != nil {
		return quota.JanitorResult{}, nil, err
	}
	reclaimed, unknownIDs, err := janitorProcessReconciliationPending(ctx, conn, now, r.policy, quota.DefaultJanitorBatchSize)
	if err != nil {
		return quota.JanitorResult{}, nil, err
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return quota.JanitorResult{}, nil, fmt.Errorf("storage: commit janitor sweep: %w", err)
	}
	committed = true

	return quota.JanitorResult{Released: released, Pended: pended, Reclaimed: reclaimed, UnknownConsumption: len(unknownIDs)}, unknownIDs, nil
}

func janitorSelectIDs(ctx context.Context, conn *sql.Conn, query string, args ...any) ([]string, error) {
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: janitor select: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("storage: janitor scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: janitor select: %w", err)
	}
	return ids, nil
}

// janitorReleaseNeverDispatched is Branch A: reserved, expired,
// dispatched_at IS NULL -> released. It applies the SAME per-allocation
// window arithmetic releaseAllocations already implements for
// QuotaLifecycleRepo.Release, on the janitor's own connection/
// transaction — calling Release itself here would try to open a SECOND
// connection and deadlock (SetMaxOpenConns(1)).
func janitorReleaseNeverDispatched(ctx context.Context, conn *sql.Conn, now int64, limit int) (int, error) {
	ids, err := janitorSelectIDs(ctx, conn,
		`SELECT id FROM quota_reservations
		  WHERE state = 'reserved' AND expires_at < ? AND dispatched_at IS NULL
		  ORDER BY expires_at LIMIT ?`,
		now, limit,
	)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		allocs, err := loadAllocations(ctx, conn, id)
		if err != nil {
			return 0, err
		}
		if err := releaseAllocations(ctx, conn, id, allocs, string(quota.AllocationReleased), now); err != nil {
			return 0, err
		}
		if _, err := conn.ExecContext(ctx,
			`UPDATE quota_reservations SET state = 'released', settled_at = ? WHERE id = ?`,
			now, id,
		); err != nil {
			return 0, fmt.Errorf("storage: janitor release reservation %q: %w", id, err)
		}
	}
	return len(ids), nil
}

// janitorPendDispatched is Branch B: reserved, expired, dispatched_at IS
// NOT NULL -> reconciliation_pending. No window arithmetic — headroom
// stays fully debited (02 §3) — matching applyTransition's default
// branch for this same edge.
func janitorPendDispatched(ctx context.Context, conn *sql.Conn, now int64, limit int) (int, error) {
	ids, err := janitorSelectIDs(ctx, conn,
		`SELECT id FROM quota_reservations
		  WHERE state = 'reserved' AND expires_at < ? AND dispatched_at IS NOT NULL
		  ORDER BY expires_at LIMIT ?`,
		now, limit,
	)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if _, err := conn.ExecContext(ctx,
			`UPDATE quota_reservation_allocations SET state = ? WHERE reservation_id = ?`,
			string(quota.AllocationReserved), id,
		); err != nil {
			return 0, fmt.Errorf("storage: janitor pend allocations %q: %w", id, err)
		}
		if _, err := conn.ExecContext(ctx,
			`UPDATE quota_reservations SET state = 'reconciliation_pending' WHERE id = ?`,
			id,
		); err != nil {
			return 0, fmt.Errorf("storage: janitor pend reservation %q: %w", id, err)
		}
	}
	return len(ids), nil
}

// pendingLease is one reconciliation_pending row's retry/lease state, as
// needed to decide between Branch C1 (reclaim) and Branch C2 (terminal).
type pendingLease struct {
	id             string
	attempts       int64
	leaseOwner     sql.NullString
	leaseExpiresAt sql.NullInt64
}

// janitorProcessReconciliationPending implements Branches C1 and C2
// together, in ONE pass over every reconciliation_pending reservation,
// because the two share the same selection base and are mutually
// exclusive by construction:
//
//   - quota.RetryExhausted(policy, attempts) decides C2 (terminal)
//     FIRST, regardless of lease state — an exhausted reservation is
//     terminalized whether or not a worker currently holds its lease.
//   - Otherwise, a lease that is absent or expired (lease_owner IS NULL,
//     lease_expires_at IS NULL, or lease_expires_at < now) is Branch C1:
//     cleared so the next ClaimPending re-picks it. Nothing else about
//     the row changes.
//   - Otherwise (not exhausted AND actively leased by a live worker) the
//     row is left completely alone — a worker is presumably still
//     working it.
//
// Returns the reclaimed count and the terminalized reservation ids (for
// the caller's per-reservation usage_gap audit, after commit).
func janitorProcessReconciliationPending(ctx context.Context, conn *sql.Conn, now int64, policy quota.ReconciliationPolicy, limit int) (reclaimed int, terminalIDs []string, err error) {
	rows, err := conn.QueryContext(ctx,
		`SELECT id, reconcile_attempts, lease_owner, lease_expires_at
		   FROM quota_reservations
		  WHERE state = 'reconciliation_pending'
		  ORDER BY expires_at LIMIT ?`,
		limit,
	)
	if err != nil {
		return 0, nil, fmt.Errorf("storage: janitor select reconciliation_pending: %w", err)
	}
	var pending []pendingLease
	for rows.Next() {
		var p pendingLease
		if scanErr := rows.Scan(&p.id, &p.attempts, &p.leaseOwner, &p.leaseExpiresAt); scanErr != nil {
			_ = rows.Close()
			return 0, nil, fmt.Errorf("storage: janitor scan reconciliation_pending: %w", scanErr)
		}
		pending = append(pending, p)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		_ = rows.Close()
		return 0, nil, fmt.Errorf("storage: janitor select reconciliation_pending: %w", rowsErr)
	}
	_ = rows.Close()

	for _, p := range pending {
		if quota.RetryExhausted(policy, int(p.attempts)) {
			if _, err := conn.ExecContext(ctx,
				`UPDATE quota_reservation_allocations SET state = ? WHERE reservation_id = ?`,
				string(quota.AllocationUnknownConsumption), p.id,
			); err != nil {
				return 0, nil, fmt.Errorf("storage: janitor terminalize allocations %q: %w", p.id, err)
			}
			if _, err := conn.ExecContext(ctx,
				`UPDATE quota_reservations
				    SET state = 'unknown_consumption', settled_at = ?, lease_owner = NULL, lease_expires_at = NULL
				  WHERE id = ?`,
				now, p.id,
			); err != nil {
				return 0, nil, fmt.Errorf("storage: janitor terminalize reservation %q: %w", p.id, err)
			}
			terminalIDs = append(terminalIDs, p.id)
			continue
		}

		leaseFree := !p.leaseOwner.Valid || !p.leaseExpiresAt.Valid || p.leaseExpiresAt.Int64 < now
		if !leaseFree {
			continue // actively leased by a live worker; leave entirely alone
		}
		if _, err := conn.ExecContext(ctx,
			`UPDATE quota_reservations SET lease_owner = NULL, lease_expires_at = NULL WHERE id = ?`,
			p.id,
		); err != nil {
			return 0, nil, fmt.Errorf("storage: janitor reclaim %q: %w", p.id, err)
		}
		reclaimed++
	}
	return reclaimed, terminalIDs, nil
}

// AuditActionUsageGap is the action recorded when a reservation reaches
// the terminal unknown_consumption boundary with its cost unaccounted
// for. 02 §3 requires this event by name ("emits a usage_gap audit
// event"), and 05 §4 requires the gap to be surfaceable in diagnostics
// and re-baselined at the next authoritative quota sync — both of which
// need the affected reservation identified, so the row always carries its
// EntityID. Ids and codes only, never content.
const AuditActionUsageGap = "quota_usage_gap"

// emitUsageGap records one usage_gap event for a single reservation,
// AFTER its transaction has committed (the SetMaxOpenConns(1) hazard). A
// nil sink or an append failure are tolerated: audit is best-effort and
// must never block or undo the state transition it describes.
func (r *QuotaLifecycleRepo) emitUsageGap(ctx context.Context, reservationID string) {
	if r.audit == nil {
		return
	}
	_ = r.audit.Append(ctx, AuditEventRow{
		Action:     AuditActionUsageGap,
		EntityType: "quota_reservation",
		EntityID:   reservationID,
		Result:     "success",
		ReasonCode: "reconciliation_pending->unknown_consumption",
		At:         r.now(),
	})
}

// emitJanitorAudit appends one summary audit row for a branch AFTER the
// janitor's transaction has already committed — never while it is open
// (the same deadlock hazard applyTransition's emitAudit guards against).
// A zero count or a nil sink emit nothing.
func (r *QuotaLifecycleRepo) emitJanitorAudit(ctx context.Context, branch string, count int) {
	if r.audit == nil || count == 0 {
		return
	}
	_ = r.audit.Append(ctx, AuditEventRow{
		Action:     "quota_janitor_" + branch,
		EntityType: "quota_reservation",
		Result:     "success",
		ReasonCode: fmt.Sprintf("count=%d", count),
		At:         r.now(),
	})
}
