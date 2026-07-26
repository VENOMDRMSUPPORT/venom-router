package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

// QuotaLifecycleRepo applies the canonical five-state reservation
// transition graph (02 §3, 05 §4) over the frozen M5 quota_reservations
// / quota_reservation_allocations / quota_windows tables.
type QuotaLifecycleRepo struct {
	db     *DB
	now    func() time.Time
	audit  *AuditEventRepo // may be nil: no audit sink
	policy quota.ReconciliationPolicy
}

// NewQuotaLifecycleRepo builds a repository over db's existing
// connection. now defaults to time.Now when nil. audit may be nil (no
// audit emission); when non-nil it is invoked AFTER the transaction has
// resolved — never inside it (the deadlock hazard: SetMaxOpenConns(1)
// means an audit write attempted while this repo's own transaction still
// holds the pool's one connection would block forever) — and its failure
// is swallowed (log-and-continue) so an audit-sink outage can never block
// the primary state transition, mirroring the P2b auditEmitter
// precedent. The repo's reconciliation policy (used only by the
// janitor's Branch C1/C2 split, via WithPolicy) defaults to
// quota.DefaultReconciliationPolicy() until WithPolicy overrides it.
func NewQuotaLifecycleRepo(db *DB, now func() time.Time, audit *AuditEventRepo) *QuotaLifecycleRepo {
	if now == nil {
		now = time.Now
	}
	return &QuotaLifecycleRepo{db: db, now: now, audit: audit, policy: quota.DefaultReconciliationPolicy()}
}

// WithPolicy sets the quota.ReconciliationPolicy this repo's Janitor uses
// to evaluate quota.RetryExhausted (02 §3 janitor branch 3 / 05 §4) and
// returns r for chaining. Chosen as an optional setter rather than a
// required NewQuotaLifecycleRepo parameter: the vast majority of this
// repo's callers (Settle/Release/Transition/MarkDispatched — the whole
// five-state lifecycle) have nothing to do with the retry policy, and
// forcing every one of those existing call sites (28 across this
// package's tests) to thread a policy through would be pure noise for a
// value only the janitor's reconciliation-pending branches consult.
func (r *QuotaLifecycleRepo) WithPolicy(policy quota.ReconciliationPolicy) *QuotaLifecycleRepo {
	r.policy = policy
	return r
}

var (
	// ErrReservationNotFound is returned when the target reservation id
	// does not exist.
	ErrReservationNotFound = errors.New("storage: reservation not found")
	// ErrInvalidSettlement is returned by Settle for an actuals map
	// keyed by a window id that is not one of the reservation's own
	// allocations, or carrying a negative/non-finite cost.
	ErrInvalidSettlement = errors.New("storage: invalid settlement")
	// ErrCannotMarkDispatched is returned by MarkDispatched when the
	// reservation is not (still) in the reserved state.
	ErrCannotMarkDispatched = errors.New("storage: cannot mark dispatched: reservation is not reserved")
)

// MarkDispatched stamps dispatched_at before the provider call — the
// janitor's branch discriminator (02 §3). Idempotent: a second call
// leaves the original timestamp intact. Legal only while the reservation
// is `reserved`.
func (r *QuotaLifecycleRepo) MarkDispatched(ctx context.Context, reservationID string) error {
	conn, err := r.db.Conn().Conn(ctx)
	if err != nil {
		return fmt.Errorf("storage: acquire connection for mark-dispatched: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("storage: begin immediate mark-dispatched tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	var state string
	var dispatchedAt sql.NullInt64
	err = conn.QueryRowContext(ctx, `SELECT state, dispatched_at FROM quota_reservations WHERE id = ?`, reservationID).Scan(&state, &dispatchedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrReservationNotFound
	}
	if err != nil {
		return fmt.Errorf("storage: load reservation %q: %w", reservationID, err)
	}

	if dispatchedAt.Valid {
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			return fmt.Errorf("storage: commit mark-dispatched no-op %q: %w", reservationID, err)
		}
		committed = true
		return nil
	}
	if state != string(quota.ReservationReserved) {
		return fmt.Errorf("%w: reservation %q is %s", ErrCannotMarkDispatched, reservationID, state)
	}

	if _, err := conn.ExecContext(ctx, `UPDATE quota_reservations SET dispatched_at = ? WHERE id = ?`, r.now().Unix(), reservationID); err != nil {
		return fmt.Errorf("storage: stamp dispatched_at for %q: %w", reservationID, err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("storage: commit mark-dispatched %q: %w", reservationID, err)
	}
	committed = true
	return nil
}

// Settle converts each allocation's hold into consumption. actuals maps
// window_id to the actual cost; a window absent from actuals (or when
// actuals is nil) settles at its estimate.
func (r *QuotaLifecycleRepo) Settle(ctx context.Context, reservationID string, actuals map[string]float64) error {
	outcome, err := r.applyTransition(ctx, reservationID, quota.ReservationSettled, actuals)
	r.auditOutcome(ctx, reservationID, outcome)
	return err
}

// Release frees every allocation without consuming. Legal ONLY from
// `reserved` (never dispatched, or the provider proved no consumption)
// or from `reconciliation_pending` with provider evidence — the caller
// owns that judgement; this method enforces only the state graph.
func (r *QuotaLifecycleRepo) Release(ctx context.Context, reservationID string) error {
	outcome, err := r.applyTransition(ctx, reservationID, quota.ReservationReleased, nil)
	r.auditOutcome(ctx, reservationID, outcome)
	return err
}

// Transition applies any other legal edge (notably
// reserved -> reconciliation_pending and
// reconciliation_pending -> unknown_consumption).
func (r *QuotaLifecycleRepo) Transition(ctx context.Context, reservationID string, to quota.ReservationState) error {
	outcome, err := r.applyTransition(ctx, reservationID, to, nil)
	r.auditOutcome(ctx, reservationID, outcome)
	return err
}

// transitionOutcome carries just enough information for the audit call
// that the PUBLIC wrapper (Settle/Release/Transition) issues once
// applyTransition has fully returned. It is never audited from inside
// applyTransition itself: that function's own dedicated connection is
// only released back to the pool by its deferred conn.Close() as it
// returns, and with SetMaxOpenConns(1) an audit INSERT attempted while
// that connection is still checked out would block forever waiting for a
// second connection the pool will never hand out — the exact deadlock
// hazard this batch's constraints call out by name. auditNone means
// "nothing to audit" (the idempotent no-op path, or an error that never
// reached a decided outcome).
type transitionOutcome struct {
	shouldAudit bool
	result      string
	reasonCode  string
}

// auditOutcome is called by the public wrappers AFTER applyTransition's
// own connection has already been closed.
func (r *QuotaLifecycleRepo) auditOutcome(ctx context.Context, reservationID string, outcome transitionOutcome) {
	if !outcome.shouldAudit {
		return
	}
	r.emitAudit(ctx, reservationID, outcome.result, outcome.reasonCode)
}

// allocationRow is one reservation_allocations row's window identity and
// estimated cost, as needed for the per-window arithmetic below.
type allocationRow struct {
	windowID  string
	estimated float64
}

// applyTransition is the ONE place every reservation state change
// happens, in a single BEGIN IMMEDIATE transaction on a dedicated
// connection (same technique and rationale as QuotaReservationRepo.Reserve):
//
//  1. Load the reservation; missing -> ErrReservationNotFound.
//  2. Idempotency: already in the target state -> commit-and-return-nil,
//     touching nothing else. This check runs BEFORE the legality check,
//     so e.g. settle on an already-settled reservation succeeds rather
//     than being rejected as an illegal settled->settled self-edge.
//  3. Otherwise validate quota.CanTransition; illegal -> write nothing,
//     commit (nothing to roll back), and (after commit) audit the
//     rejection.
//  4. Legal: move every allocation to quota.AllocationStateFor(to) and
//     apply the window arithmetic for that target state (see the switch
//     below), then update the reservation's own state (and settled_at
//     for a terminal target), commit, and (after commit) audit success.
func (r *QuotaLifecycleRepo) applyTransition(ctx context.Context, reservationID string, to quota.ReservationState, actuals map[string]float64) (transitionOutcome, error) {
	conn, err := r.db.Conn().Conn(ctx)
	if err != nil {
		return transitionOutcome{}, fmt.Errorf("storage: acquire connection for transition: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return transitionOutcome{}, fmt.Errorf("storage: begin immediate transition tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	var currentStr string
	err = conn.QueryRowContext(ctx, `SELECT state FROM quota_reservations WHERE id = ?`, reservationID).Scan(&currentStr)
	if errors.Is(err, sql.ErrNoRows) {
		return transitionOutcome{}, ErrReservationNotFound
	}
	if err != nil {
		return transitionOutcome{}, fmt.Errorf("storage: load reservation %q: %w", reservationID, err)
	}
	current, err := quota.ParseReservationState(currentStr)
	if err != nil {
		return transitionOutcome{}, fmt.Errorf("storage: reservation %q: %w", reservationID, err)
	}

	if current == to {
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			return transitionOutcome{}, fmt.Errorf("storage: commit no-op transition %q: %w", reservationID, err)
		}
		committed = true
		return transitionOutcome{}, nil
	}

	if legalErr := quota.CanTransition(current, to); legalErr != nil {
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			return transitionOutcome{}, fmt.Errorf("storage: commit rejected-transition %q: %w", reservationID, err)
		}
		committed = true
		outcome := transitionOutcome{shouldAudit: true, result: "rejected", reasonCode: fmt.Sprintf("%s->%s", current, to)}
		return outcome, legalErr
	}

	allocState, err := quota.AllocationStateFor(to)
	if err != nil {
		return transitionOutcome{}, err
	}

	allocs, err := loadAllocations(ctx, conn, reservationID)
	if err != nil {
		return transitionOutcome{}, err
	}

	if actuals != nil {
		if err := validateActuals(allocs, actuals, reservationID); err != nil {
			return transitionOutcome{}, err
		}
	}

	epoch := r.now().Unix()
	switch to {
	case quota.ReservationSettled:
		if err := settleAllocations(ctx, conn, reservationID, allocs, actuals, string(allocState), epoch); err != nil {
			return transitionOutcome{}, err
		}
	case quota.ReservationReleased:
		if err := releaseAllocations(ctx, conn, reservationID, allocs, string(allocState), epoch); err != nil {
			return transitionOutcome{}, err
		}
	default:
		// reserved -> reconciliation_pending, reconciliation_pending ->
		// unknown_consumption: NO window arithmetic at all — headroom
		// stays debited (02 §3). Every allocation still moves to
		// allocState in ONE statement — never freed silently.
		if _, err := conn.ExecContext(ctx,
			`UPDATE quota_reservation_allocations SET state = ? WHERE reservation_id = ?`,
			string(allocState), reservationID,
		); err != nil {
			return transitionOutcome{}, fmt.Errorf("storage: update allocations for %q: %w", reservationID, err)
		}
	}

	var settledAtArg any
	if quota.IsTerminalReservationState(to) {
		settledAtArg = epoch
	}
	if _, err := conn.ExecContext(ctx,
		`UPDATE quota_reservations SET state = ?, settled_at = ? WHERE id = ?`,
		string(to), settledAtArg, reservationID,
	); err != nil {
		return transitionOutcome{}, fmt.Errorf("storage: update reservation %q: %w", reservationID, err)
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return transitionOutcome{}, fmt.Errorf("storage: commit transition %q: %w", reservationID, err)
	}
	committed = true

	outcome := transitionOutcome{shouldAudit: true, result: "success", reasonCode: fmt.Sprintf("%s->%s", current, to)}
	return outcome, nil
}

func loadAllocations(ctx context.Context, conn *sql.Conn, reservationID string) ([]allocationRow, error) {
	rows, err := conn.QueryContext(ctx, `SELECT window_id, estimated_cost FROM quota_reservation_allocations WHERE reservation_id = ?`, reservationID)
	if err != nil {
		return nil, fmt.Errorf("storage: load allocations for %q: %w", reservationID, err)
	}
	defer func() { _ = rows.Close() }()

	var allocs []allocationRow
	for rows.Next() {
		var a allocationRow
		if err := rows.Scan(&a.windowID, &a.estimated); err != nil {
			return nil, fmt.Errorf("storage: scan allocation for %q: %w", reservationID, err)
		}
		allocs = append(allocs, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: load allocations for %q: %w", reservationID, err)
	}
	return allocs, nil
}

// validateActuals rejects an actuals map keyed by a window id that is
// not one of this reservation's own allocations (a typo must not
// silently settle at the estimate instead of failing loudly), and any
// negative or non-finite cost.
func validateActuals(allocs []allocationRow, actuals map[string]float64, reservationID string) error {
	validIDs := make(map[string]bool, len(allocs))
	for _, a := range allocs {
		validIDs[a.windowID] = true
	}
	for windowID, cost := range actuals {
		if !validIDs[windowID] {
			return fmt.Errorf("%w: window %q is not part of reservation %q", ErrInvalidSettlement, windowID, reservationID)
		}
		if math.IsNaN(cost) || math.IsInf(cost, 0) || cost < 0 {
			return fmt.Errorf("%w: actual cost for window %q must be non-negative and finite, got %v", ErrInvalidSettlement, windowID, cost)
		}
	}
	return nil
}

// settleAllocations converts each allocation's hold into consumption:
// reserved drops by the ESTIMATE (never the actual — the estimate is
// what was debited), actual_cost is persisted, and used/remaining are
// adjusted by the ACTUAL only where already known (nullable numerics
// mean unknown — 02 §3 — so a NULL column is never seeded from a locally
// derived delta, which would present local arithmetic as provider
// evidence).
func settleAllocations(ctx context.Context, conn *sql.Conn, reservationID string, allocs []allocationRow, actuals map[string]float64, allocState string, epoch int64) error {
	for _, a := range allocs {
		actual := a.estimated
		if actuals != nil {
			if v, ok := actuals[a.windowID]; ok {
				actual = v
			}
		}
		if _, err := conn.ExecContext(ctx,
			`UPDATE quota_windows
			 SET reserved = MAX(reserved - ?, 0),
			     used = CASE WHEN used IS NOT NULL THEN used + ? ELSE used END,
			     remaining = CASE WHEN remaining IS NOT NULL THEN remaining - ? ELSE remaining END,
			     version = version + 1,
			     updated_at = ?
			 WHERE id = ?`,
			a.estimated, actual, actual, epoch, a.windowID,
		); err != nil {
			return fmt.Errorf("storage: settle window %q for %q: %w", a.windowID, reservationID, err)
		}
		if _, err := conn.ExecContext(ctx,
			`UPDATE quota_reservation_allocations SET state = ?, actual_cost = ? WHERE reservation_id = ? AND window_id = ?`,
			allocState, actual, reservationID, a.windowID,
		); err != nil {
			return fmt.Errorf("storage: update allocation %q for %q: %w", a.windowID, reservationID, err)
		}
	}
	return nil
}

// releaseAllocations frees every allocation's hold without consuming:
// reserved drops by the estimate, used/remaining are left untouched, and
// actual_cost stays NULL.
func releaseAllocations(ctx context.Context, conn *sql.Conn, reservationID string, allocs []allocationRow, allocState string, epoch int64) error {
	for _, a := range allocs {
		if _, err := conn.ExecContext(ctx,
			`UPDATE quota_windows SET reserved = MAX(reserved - ?, 0), version = version + 1, updated_at = ? WHERE id = ?`,
			a.estimated, epoch, a.windowID,
		); err != nil {
			return fmt.Errorf("storage: release window %q for %q: %w", a.windowID, reservationID, err)
		}
		if _, err := conn.ExecContext(ctx,
			`UPDATE quota_reservation_allocations SET state = ? WHERE reservation_id = ? AND window_id = ?`,
			allocState, reservationID, a.windowID,
		); err != nil {
			return fmt.Errorf("storage: update allocation %q for %q: %w", a.windowID, reservationID, err)
		}
	}
	return nil
}

// emitAudit appends an audit row AFTER the caller's transaction has
// already committed — never while it is open (the deadlock hazard). A
// nil audit sink or an append failure are both silently tolerated: audit
// is best-effort and must never block the primary state transition it
// describes.
func (r *QuotaLifecycleRepo) emitAudit(ctx context.Context, reservationID, result, reasonCode string) {
	if r.audit == nil {
		return
	}
	_ = r.audit.Append(ctx, AuditEventRow{
		Action:     "quota_reservation_transition",
		EntityType: "quota_reservation",
		EntityID:   reservationID,
		Result:     result,
		ReasonCode: reasonCode,
		At:         r.now(),
	})
}
