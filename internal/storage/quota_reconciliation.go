package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

// ReconciliationRepo resolves reconciliation_pending reservations (02 §3
// / 05 §4): a small-batch, idempotent worker with NO provider-usage API
// available at this layer, so it always settles at the estimate on its
// first opportunity, unless the reservation has already exceeded its
// policy's retry-exhaustion boundary — in which case it gives up and
// transitions to unknown_consumption instead.
type ReconciliationRepo struct {
	db        *DB
	now       func() time.Time
	policy    quota.ReconciliationPolicy
	lifecycle *QuotaLifecycleRepo
	audit     *AuditEventRepo // may be nil: no audit sink
}

// NewReconciliationRepo builds a repository over db's existing
// connection. now defaults to time.Now when nil.
func NewReconciliationRepo(db *DB, now func() time.Time, policy quota.ReconciliationPolicy, lifecycle *QuotaLifecycleRepo, audit *AuditEventRepo) *ReconciliationRepo {
	if now == nil {
		now = time.Now
	}
	return &ReconciliationRepo{db: db, now: now, policy: policy, lifecycle: lifecycle, audit: audit}
}

// PendingReservation is one reconciliation_pending reservation past its
// processing deadline, as read by PendingReservations.
type PendingReservation struct {
	ReservationID string
	AccountID     string
	RequestID     string
	AttemptID     string
	CreatedAt     int64
	ExpiresAt     int64
}

// AllocationInfo is one reservation-allocation pairing: its window id and
// the estimated cost debited against it.
type AllocationInfo struct {
	WindowID  string
	Estimated float64
}

// LoadAllocations reads back p's own allocations through the pool
// (db.Conn()) — safe to call whenever the caller holds no open
// transaction of its own, exactly like every other pool-scoped read in
// this package.
func (p PendingReservation) LoadAllocations(ctx context.Context, db *DB) ([]AllocationInfo, error) {
	rows, err := db.Conn().QueryContext(ctx,
		`SELECT window_id, estimated_cost FROM quota_reservation_allocations WHERE reservation_id = ?`,
		p.ReservationID,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: load allocations for %q: %w", p.ReservationID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []AllocationInfo
	for rows.Next() {
		var a AllocationInfo
		if err := rows.Scan(&a.WindowID, &a.Estimated); err != nil {
			return nil, fmt.Errorf("storage: scan allocation for %q: %w", p.ReservationID, err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: load allocations for %q: %w", p.ReservationID, err)
	}
	return out, nil
}

// PendingReservations returns up to r.policy.BatchSize
// reconciliation_pending reservations past their processing deadline,
// earliest expires_at first — the small-batch contract that keeps any
// one sweep short.
func (r *ReconciliationRepo) PendingReservations(ctx context.Context) ([]PendingReservation, error) {
	rows, err := r.db.Conn().QueryContext(ctx,
		`SELECT id, account_id, request_id, attempt_id, created_at, expires_at
		   FROM quota_reservations
		  WHERE state = 'reconciliation_pending' AND expires_at < ?
		  ORDER BY expires_at LIMIT ?`,
		r.now().Unix(), r.policy.BatchSize,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: list pending reservations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []PendingReservation
	for rows.Next() {
		var p PendingReservation
		if err := rows.Scan(&p.ReservationID, &p.AccountID, &p.RequestID, &p.AttemptID, &p.CreatedAt, &p.ExpiresAt); err != nil {
			return nil, fmt.Errorf("storage: scan pending reservation: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: list pending reservations: %w", err)
	}
	return out, nil
}

// retryExhausted reports whether a reservation stuck in
// reconciliation_pending since expiresAt has outlived this policy's
// cumulative retry budget (MaxRetries attempts at BaseBackoff apart,
// capped at MaxBackoff) as of now. There is no persisted retry counter
// on the frozen M5 schema, so elapsed wall-clock time against this
// budget is the retry-exhaustion signal, rather than a counted attempt
// number.
func retryExhausted(policy quota.ReconciliationPolicy, now, expiresAt int64) bool {
	budget := policy.BaseBackoff * time.Duration(policy.MaxRetries)
	if policy.MaxBackoff > 0 && budget > policy.MaxBackoff {
		budget = policy.MaxBackoff
	}
	elapsed := time.Duration(now-expiresAt) * time.Second
	return elapsed >= budget
}

// ReconcileOne resolves a single pending reservation. With no
// provider-usage API available at this layer, it always settles at the
// estimate (actuals=nil) the first time it is called within the retry
// budget; once the budget is exhausted it instead transitions to
// unknown_consumption. Both paths route through QuotaLifecycleRepo's
// already-idempotent Settle/Transition, so calling ReconcileOne twice
// for the same reservation is itself idempotent — the second call finds
// the reservation already in its target state and is a no-op.
func (r *ReconciliationRepo) ReconcileOne(ctx context.Context, p PendingReservation) (quota.ReconciliationOutcome, error) {
	now := r.now().Unix()

	if retryExhausted(r.policy, now, p.ExpiresAt) {
		if err := r.lifecycle.Transition(ctx, p.ReservationID, quota.ReservationUnknownConsumption); err != nil {
			return quota.ReconciliationOutcome{}, err
		}
		return quota.ReconciliationOutcome{ReservationID: p.ReservationID, Outcome: quota.ReservationUnknownConsumption}, nil
	}

	if err := r.lifecycle.Settle(ctx, p.ReservationID, nil); err != nil {
		return quota.ReconciliationOutcome{}, err
	}
	return quota.ReconciliationOutcome{ReservationID: p.ReservationID, Outcome: quota.ReservationSettled}, nil
}
