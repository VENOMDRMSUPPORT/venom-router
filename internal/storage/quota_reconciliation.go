package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
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

// RateLimitSignal is a caller-detected 429/rate-limit condition to
// forward to SyncQuotaWindows' onRateLimit callback. SyncQuotaWindows
// never calls a QuotaAdapter itself and so cannot detect a rate limit on
// its own — the caller (whoever DID call QuotaAdapter.FetchQuota and saw
// its error) supplies this instead.
type RateLimitSignal struct {
	Scope               string
	AccountID           *string
	OfferingOperationID *string
	ProviderID          *string
	RetryAfter          *int // seconds; nil = provider gave no Retry-After
}

// SyncQuotaWindows UPSERTs accountID's provider-evidence quota windows
// from providerWindows (03 §1 / 02 §3): a window matching an existing
// (account_id, unit, window_type, window_key) is updated in place
// (used/remaining/total/reset_at/confidence refreshed, version
// incremented, freshness_state stamped 'fresh'); a window with no match
// is inserted new. Any EXISTING provider_evidence window for accountID
// that providerWindows does NOT mention this round is marked
// freshness_state='stale' — never deleted, never left silently fresh —
// so a dropped or narrowed fetch result surfaces as staleness rather
// than as a false "still current" signal.
//
// SyncQuotaWindows never calls a QuotaAdapter — it only maps an
// already-fetched result. When rateLimit is non-nil and onRateLimit is
// non-nil, onRateLimit is invoked exactly once with rateLimit's fields;
// a nil rateLimit never invokes it. The window sync and the rate-limit
// callback are independent — a caller may supply either, both, or
// neither in a single call.
func (r *ReconciliationRepo) SyncQuotaWindows(ctx context.Context, accountID string, providerWindows []providers.QuotaWindow, rateLimit *RateLimitSignal, onRateLimit func(scope string, accountID, offeringOperationID, providerID *string, retryAfter *int)) error {
	if rateLimit != nil && onRateLimit != nil {
		onRateLimit(rateLimit.Scope, rateLimit.AccountID, rateLimit.OfferingOperationID, rateLimit.ProviderID, rateLimit.RetryAfter)
	}

	tx, err := r.db.Conn().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: begin sync-quota-windows tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has succeeded

	epoch := r.now().Unix()
	seen := make(map[[3]string]bool, len(providerWindows))

	for _, w := range providerWindows {
		unit, err := quota.ParseUnit(w.Unit)
		if err != nil {
			return fmt.Errorf("storage: sync quota window: %w", err)
		}
		key, err := quota.NormalizeWindowKey(quota.WindowKeyInput{ProviderKey: w.WindowKey, DurationSeconds: w.DurationSeconds, Unit: unit})
		if err != nil {
			return fmt.Errorf("storage: sync quota window: %w", err)
		}
		seen[[3]string{string(unit), w.WindowType, key}] = true

		existingID, found, err := lookupSyncedWindow(ctx, tx, accountID, unit, w.WindowType, key)
		if err != nil {
			return err
		}
		if found {
			if _, err := tx.ExecContext(ctx,
				`UPDATE quota_windows
				    SET used = ?, remaining = ?, total = ?, reset_at = ?, confidence = ?,
				        freshness_state = 'fresh', version = version + 1, observed_at = ?, updated_at = ?
				  WHERE id = ?`,
				w.Used, w.Remaining, w.Total, w.ResetAt, w.Confidence, epoch, epoch, existingID,
			); err != nil {
				return fmt.Errorf("storage: update synced window %q: %w", existingID, err)
			}
			continue
		}

		id := randomQuotaID()
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO quota_windows
			    (id, account_id, source, unit, window_type, window_key, duration_seconds,
			     used, remaining, total, reserved, limit_value, reset_at, version, confidence,
			     freshness_state, observed_at, created_at, updated_at)
			 VALUES (?, ?, 'provider_evidence', ?, ?, ?, ?, ?, ?, ?, 0, NULL, ?, 1, ?, 'fresh', ?, ?, ?)`,
			id, accountID, string(unit), w.WindowType, key, intPtrArg(w.DurationSeconds),
			w.Used, w.Remaining, w.Total, w.ResetAt, w.Confidence, epoch, epoch, epoch,
		); err != nil {
			return fmt.Errorf("storage: insert synced window (%q,%q): %w", accountID, key, err)
		}
	}

	if err := staleUnseenProviderWindows(ctx, tx, accountID, seen, epoch); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: commit sync-quota-windows for %q: %w", accountID, err)
	}
	return nil
}

func lookupSyncedWindow(ctx context.Context, tx *sql.Tx, accountID string, unit quota.Unit, windowType, key string) (id string, found bool, err error) {
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM quota_windows WHERE account_id = ? AND source = 'provider_evidence' AND unit = ? AND window_type = ? AND window_key = ?`,
		accountID, string(unit), windowType, key,
	).Scan(&id)
	if err == nil {
		return id, true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return "", false, fmt.Errorf("storage: lookup synced window (%q,%q,%q,%q): %w", accountID, unit, windowType, key, err)
}

// staleUnseenProviderWindows marks every provider_evidence window for
// accountID whose (unit, window_type, window_key) is NOT in seen as
// freshness_state='stale' — the window's own data (used/remaining/etc.)
// is left untouched; only its freshness verdict changes.
func staleUnseenProviderWindows(ctx context.Context, tx *sql.Tx, accountID string, seen map[[3]string]bool, epoch int64) error {
	rows, err := tx.QueryContext(ctx,
		`SELECT id, unit, window_type, window_key FROM quota_windows WHERE account_id = ? AND source = 'provider_evidence'`,
		accountID,
	)
	if err != nil {
		return fmt.Errorf("storage: list provider-evidence windows for %q: %w", accountID, err)
	}
	var staleIDs []string
	for rows.Next() {
		var id, unit, windowType, key string
		if err := rows.Scan(&id, &unit, &windowType, &key); err != nil {
			_ = rows.Close()
			return fmt.Errorf("storage: scan provider-evidence window for %q: %w", accountID, err)
		}
		if !seen[[3]string{unit, windowType, key}] {
			staleIDs = append(staleIDs, id)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("storage: list provider-evidence windows for %q: %w", accountID, err)
	}
	_ = rows.Close()

	for _, id := range staleIDs {
		if _, err := tx.ExecContext(ctx,
			`UPDATE quota_windows SET freshness_state = 'stale', updated_at = ? WHERE id = ?`,
			epoch, id,
		); err != nil {
			return fmt.Errorf("storage: mark window %q stale: %w", id, err)
		}
	}
	return nil
}
