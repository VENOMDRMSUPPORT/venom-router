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
// processing deadline, as read by PendingReservations or claimed by
// ClaimPending. Attempts and LeaseOwner are only meaningful for a value
// returned by ClaimPending (PendingReservations does not read them).
type PendingReservation struct {
	ReservationID string
	AccountID     string
	RequestID     string
	AttemptID     string
	CreatedAt     int64
	ExpiresAt     int64
	Attempts      int
	LeaseOwner    string
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

// emitUsageGap records one usage_gap event (AuditActionUsageGap) for a
// reservation whose cost is terminally unaccounted for, after its
// transition has already committed. Mirrors QuotaLifecycleRepo.emitUsageGap
// — the worker has its own audit sink, so it cannot reuse that method.
func (r *ReconciliationRepo) emitUsageGap(ctx context.Context, reservationID string) {
	if r.audit == nil {
		return
	}
	_ = r.audit.Append(ctx, AuditEventRow{
		Action:     AuditActionUsageGap,
		EntityType: "quota_reservation",
		EntityID:   reservationID,
		Result:     "success",
		ReasonCode: "retry_budget_exhausted",
		At:         r.now(),
	})
}

// ErrClaimRequiresOwner is returned by ClaimPending for an empty owner.
var ErrClaimRequiresOwner = errors.New("storage: claim pending: owner required")

// ClaimPending atomically leases up to r.policy.BatchSize pending
// reservations for owner, in ONE BEGIN IMMEDIATE transaction, skipping
// any whose backoff (quota.BackoffFor) has not yet elapsed, whose lease
// is still held by someone else, or that are already retry-exhausted
// (those belong to the janitor's Branch C2, never to a live claim). The
// per-candidate backoff duration involves 10^attempts, which SQL cannot
// express, so candidates are selected broadly and filtered/limited here
// in Go, exactly as this batch's spec requires. An empty result is not
// an error — it simply means nothing is currently claimable.
func (r *ReconciliationRepo) ClaimPending(ctx context.Context, owner string, ttl time.Duration) ([]PendingReservation, error) {
	if owner == "" {
		return nil, ErrClaimRequiresOwner
	}

	conn, err := r.db.Conn().Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("storage: acquire connection for claim-pending: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return nil, fmt.Errorf("storage: begin immediate claim-pending tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	now := r.now().Unix()

	rows, err := conn.QueryContext(ctx,
		`SELECT id, account_id, request_id, attempt_id, created_at, expires_at, reconcile_attempts
		   FROM quota_reservations
		  WHERE state = 'reconciliation_pending'
		    AND (lease_owner IS NULL OR lease_expires_at IS NULL OR lease_expires_at < ?)
		    AND reconcile_attempts < ?
		  ORDER BY expires_at`,
		now, r.policy.MaxRetries,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: claim-pending select candidates: %w", err)
	}
	type candidate struct {
		id, accountID, requestID, attemptID string
		createdAt, expiresAt                int64
		attempts                            int
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if scanErr := rows.Scan(&c.id, &c.accountID, &c.requestID, &c.attemptID, &c.createdAt, &c.expiresAt, &c.attempts); scanErr != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("storage: claim-pending scan candidate: %w", scanErr)
		}
		candidates = append(candidates, c)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("storage: claim-pending select candidates: %w", rowsErr)
	}
	_ = rows.Close()

	var claimed []PendingReservation
	for _, c := range candidates {
		if len(claimed) >= r.policy.BatchSize {
			break
		}
		backoff := quota.BackoffFor(r.policy, c.attempts)
		if now < c.expiresAt+int64(backoff.Seconds()) {
			continue // backoff has not elapsed yet
		}

		leaseExpiresAt := now + int64(ttl.Seconds())
		if _, err := conn.ExecContext(ctx,
			`UPDATE quota_reservations SET lease_owner = ?, lease_expires_at = ? WHERE id = ?`,
			owner, leaseExpiresAt, c.id,
		); err != nil {
			return nil, fmt.Errorf("storage: claim-pending lease %q: %w", c.id, err)
		}
		claimed = append(claimed, PendingReservation{
			ReservationID: c.id,
			AccountID:     c.accountID,
			RequestID:     c.requestID,
			AttemptID:     c.attemptID,
			CreatedAt:     c.createdAt,
			ExpiresAt:     c.expiresAt,
			Attempts:      c.attempts,
			LeaseOwner:    owner,
		})
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return nil, fmt.Errorf("storage: commit claim-pending: %w", err)
	}
	committed = true

	return claimed, nil
}

// verifyLeaseAndIncrementAttempt is ReconcileOne's own short transaction:
// re-read the reservation's CURRENT lease_owner inside the transaction
// (never trusting the caller's stale PendingReservation snapshot),
// reject with quota.ErrLeaseNotHeld if owner no longer matches, and
// otherwise increment reconcile_attempts by exactly 1, returning the NEW
// attempt count. This is deliberately its own transaction, separate from
// the Settle/Transition call ReconcileOne makes next: those open their
// OWN dedicated connection, and holding this one open across that call
// would deadlock (SetMaxOpenConns(1)).
func (r *ReconciliationRepo) verifyLeaseAndIncrementAttempt(ctx context.Context, owner, reservationID string) (int, error) {
	conn, err := r.db.Conn().Conn(ctx)
	if err != nil {
		return 0, fmt.Errorf("storage: acquire connection for lease check: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return 0, fmt.Errorf("storage: begin immediate lease-check tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	var currentOwner sql.NullString
	var attempts int64
	err = conn.QueryRowContext(ctx,
		`SELECT lease_owner, reconcile_attempts FROM quota_reservations WHERE id = ?`, reservationID,
	).Scan(&currentOwner, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrReservationNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("storage: lease-check load reservation %q: %w", reservationID, err)
	}
	if !currentOwner.Valid || currentOwner.String != owner {
		return 0, quota.ErrLeaseNotHeld
	}

	newAttempts := attempts + 1
	if _, err := conn.ExecContext(ctx,
		`UPDATE quota_reservations SET reconcile_attempts = ? WHERE id = ?`,
		newAttempts, reservationID,
	); err != nil {
		return 0, fmt.Errorf("storage: increment reconcile_attempts for %q: %w", reservationID, err)
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return 0, fmt.Errorf("storage: commit lease-check for %q: %w", reservationID, err)
	}
	committed = true

	return int(newAttempts), nil
}

// clearLease frees a reservation's lease columns through the pool — a
// single autocommitted statement, safe to call whenever the caller holds
// no open transaction of its own.
func (r *ReconciliationRepo) clearLease(ctx context.Context, reservationID string) error {
	if _, err := r.db.Conn().ExecContext(ctx,
		`UPDATE quota_reservations SET lease_owner = NULL, lease_expires_at = NULL WHERE id = ?`,
		reservationID,
	); err != nil {
		return fmt.Errorf("storage: clear lease for %q: %w", reservationID, err)
	}
	return nil
}

// ReconcileOne resolves a single pending reservation claimed by owner
// (via ClaimPending). It first verifies owner still holds the lease and
// increments reconcile_attempts (its own short transaction) — a worker
// whose lease expired gets quota.ErrLeaseNotHeld and writes nothing else.
// It then decides with quota.RetryExhausted(policy, newAttempts) — THE
// SAME predicate the janitor's Branch C2 uses, so the two can never
// disagree:
//
//   - exhausted -> Transition to unknown_consumption + a usage_gap audit
//     event, per reservation, + a "usage_gap" re-baseline flag.
//   - not exhausted -> SettleEstimate (confidence=low) — with no
//     provider-usage API available at this layer, there is nothing else
//     to settle at — + an "estimate_settled_low_confidence" re-baseline
//     flag.
//
// Either way the lease is cleared once the terminal storage call
// succeeds. Both paths route through QuotaLifecycleRepo's
// already-idempotent SettleEstimate/Transition, so calling ReconcileOne
// twice in a row for the same reservation (same owner, lease not yet
// cleared by anything else) is itself idempotent.
func (r *ReconciliationRepo) ReconcileOne(ctx context.Context, owner string, p PendingReservation) (quota.ReconciliationOutcome, error) {
	newAttempts, err := r.verifyLeaseAndIncrementAttempt(ctx, owner, p.ReservationID)
	if err != nil {
		return quota.ReconciliationOutcome{}, err
	}

	if quota.RetryExhausted(r.policy, newAttempts) {
		if err := r.lifecycle.Transition(ctx, p.ReservationID, quota.ReservationUnknownConsumption); err != nil {
			return quota.ReconciliationOutcome{}, err
		}
		if err := r.clearLease(ctx, p.ReservationID); err != nil {
			return quota.ReconciliationOutcome{}, err
		}
		// 02 §3: reaching the terminal retry boundary "emits a usage_gap
		// audit event"; 05 §4 additionally requires the gap to be visible in
		// diagnostics and re-baselined at the next authoritative quota sync.
		// Emitted here, after Transition has committed, so the event is never
		// recorded for a transition that did not actually happen.
		r.emitUsageGap(ctx, p.ReservationID)
		if err := r.FlagRebaseline(ctx, p.AccountID, "usage_gap"); err != nil {
			return quota.ReconciliationOutcome{}, err
		}
		return quota.ReconciliationOutcome{ReservationID: p.ReservationID, Outcome: quota.ReservationUnknownConsumption}, nil
	}

	// 05 §4's no-provider-API path: settle at the estimate with LOW
	// confidence, never Settle(..., nil) — that method is reserved for
	// confirmed costs (P3b-FIX-CONF).
	if err := r.lifecycle.SettleEstimate(ctx, p.ReservationID); err != nil {
		return quota.ReconciliationOutcome{}, err
	}
	if err := r.clearLease(ctx, p.ReservationID); err != nil {
		return quota.ReconciliationOutcome{}, err
	}
	if err := r.FlagRebaseline(ctx, p.AccountID, "estimate_settled_low_confidence"); err != nil {
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
