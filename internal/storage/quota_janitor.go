package storage

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

// Janitor recovers reservations stuck past their processing deadline
// (02 §3) in ONE BEGIN IMMEDIATE transaction on a dedicated connection,
// discriminating strictly on dispatched_at — never on state='reserved'
// alone:
//
//  1. Branch A: never dispatched, past its processing deadline ->
//     released, with the SAME window arithmetic QuotaLifecycleRepo.Release
//     applies (headroom is freed, never left leaking).
//  2. Branch B: dispatched, past its processing deadline, still reserved
//     -> reconciliation_pending (the provider call was made but its
//     result never landed, e.g. a crash mid-flight; headroom stays
//     debited until reconciliation resolves it).
//  3. Branch C: reconciliation_pending past the retry deadline
//     (now - quota.DefaultRetryDeadline) -> unknown_consumption
//     (terminal; headroom stays debited — the usage gap is never
//     silently discarded).
//
// All three branches run on the SAME connection inside the SAME
// transaction — this method never acquires a second connection
// (SetMaxOpenConns(1); see quota.ErrJanitorWouldDeadlock) — so one sweep
// is itself all-or-nothing. Audit rows (one per non-empty branch, when
// an audit sink is configured) are emitted AFTER janitorSweep has fully
// returned — i.e. after its deferred conn.Close() has already run —
// never while the transaction's connection is still checked out. Emitting
// them from INSIDE janitorSweep, before its own defer runs, would be
// exactly the deadlock applyTransition's emitAudit-after-commit split
// guards against: the pool's one connection would still be held, and
// audit's own r.db.Conn() call would block forever waiting for a second
// one that never comes.
func (r *QuotaLifecycleRepo) Janitor(ctx context.Context) (quota.JanitorResult, error) {
	result, err := r.janitorSweep(ctx)
	if err != nil {
		return quota.JanitorResult{}, err
	}
	r.emitJanitorAudit(ctx, "released", result.Released)
	r.emitJanitorAudit(ctx, "pended", result.Pended)
	r.emitJanitorAudit(ctx, "unknown_consumption", result.UnknownConsumption)
	return result, nil
}

func (r *QuotaLifecycleRepo) janitorSweep(ctx context.Context) (quota.JanitorResult, error) {
	conn, err := r.db.Conn().Conn(ctx)
	if err != nil {
		return quota.JanitorResult{}, fmt.Errorf("storage: acquire connection for janitor: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return quota.JanitorResult{}, fmt.Errorf("storage: begin immediate janitor tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	now := r.now().Unix()
	retryDeadline := now - int64(quota.DefaultRetryDeadline.Seconds())

	released, err := janitorReleaseNeverDispatched(ctx, conn, now, quota.DefaultJanitorBatchSize)
	if err != nil {
		return quota.JanitorResult{}, err
	}
	pended, err := janitorPendDispatched(ctx, conn, now, quota.DefaultJanitorBatchSize)
	if err != nil {
		return quota.JanitorResult{}, err
	}
	unknown, err := janitorUnknownConsumption(ctx, conn, retryDeadline, now, quota.DefaultJanitorBatchSize)
	if err != nil {
		return quota.JanitorResult{}, err
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return quota.JanitorResult{}, fmt.Errorf("storage: commit janitor sweep: %w", err)
	}
	committed = true

	return quota.JanitorResult{Released: released, Pended: pended, UnknownConsumption: unknown}, nil
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

// janitorUnknownConsumption is Branch C: reconciliation_pending past the
// retry deadline -> unknown_consumption. Terminal; no window arithmetic
// — headroom stays debited, the usage gap is never silently discarded.
func janitorUnknownConsumption(ctx context.Context, conn *sql.Conn, retryDeadline, now int64, limit int) (int, error) {
	ids, err := janitorSelectIDs(ctx, conn,
		`SELECT id FROM quota_reservations
		  WHERE state = 'reconciliation_pending' AND expires_at < ?
		  ORDER BY expires_at LIMIT ?`,
		retryDeadline, limit,
	)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if _, err := conn.ExecContext(ctx,
			`UPDATE quota_reservation_allocations SET state = ? WHERE reservation_id = ?`,
			string(quota.AllocationUnknownConsumption), id,
		); err != nil {
			return 0, fmt.Errorf("storage: janitor unknown-consumption allocations %q: %w", id, err)
		}
		if _, err := conn.ExecContext(ctx,
			`UPDATE quota_reservations SET state = 'unknown_consumption', settled_at = ? WHERE id = ?`,
			now, id,
		); err != nil {
			return 0, fmt.Errorf("storage: janitor unknown-consumption reservation %q: %w", id, err)
		}
	}
	return len(ids), nil
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
