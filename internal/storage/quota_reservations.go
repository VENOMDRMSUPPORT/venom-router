package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

// QuotaReservationRepo implements the atomic all-or-nothing reservation
// contract (02 §3, 05 §2 Step 8.3) over the frozen M5 quota_reservations
// / quota_reservation_allocations tables. It never takes a provider
// adapter, HTTP client, or any provider seam as a parameter — the
// signature itself is the guard that no provider call can ever be made
// while its transaction is open (05 §2 Step 4).
type QuotaReservationRepo struct {
	db  *DB
	now func() time.Time
}

// NewQuotaReservationRepo builds a repository over db's existing
// connection. now supplies the repo's clock, defaulting to time.Now when
// nil (mirrors NewQuotaWindowRepo's pattern).
func NewQuotaReservationRepo(db *DB, now func() time.Time) *QuotaReservationRepo {
	if now == nil {
		now = time.Now
	}
	return &QuotaReservationRepo{db: db, now: now}
}

// ReserveParams is one attempt's reservation request.
type ReserveParams struct {
	AccountID   string
	RequestID   string
	AttemptID   string
	Allocations []quota.Allocation // from quota.Estimate
}

// ReserveResult is what Reserve produced.
type ReserveResult struct {
	ReservationID string
	Debits        []quota.WindowDebit
	// Idempotent is true when a reservation for (RequestID, AttemptID)
	// already existed and NOTHING was debited by this call.
	Idempotent bool
}

var (
	// ErrReservationRejected is returned when any applicable window's
	// conditional UPDATE affects zero rows (insufficient headroom or a
	// stale version). Nothing is left debited anywhere.
	ErrReservationRejected = errors.New("storage: reservation rejected: insufficient headroom or stale window version")
	// ErrInvalidReserveParams is returned for a structurally invalid
	// ReserveParams. Nothing is written.
	ErrInvalidReserveParams = errors.New("storage: invalid reserve params")
)

// Reserve executes 02 §3's atomic reservation contract:
//
//  1. Validate params — a typed error, nothing written.
//  2. Derive the deterministic reservation id.
//  3. Open a BEGIN IMMEDIATE transaction on one dedicated connection (see
//     the comment above the BEGIN IMMEDIATE call for why).
//  4. Idempotency check: an existing (request_id, attempt_id) row short-
//     circuits to Idempotent=true regardless of its current state —
//     nothing is debited and no terminal reservation is ever resurrected.
//  5. Load the account's windows and compute quota.ApplicableDebits.
//  6. Apply the canonical conditional UPDATE to every debit in
//     deterministic order. Any 0-row result rolls back the WHOLE
//     transaction (no partial debit) and returns ErrReservationRejected.
//  7. On full success, insert the reservation (state=reserved,
//     dispatched_at=NULL, expires_at=now+quota.DefaultProcessingDeadline)
//     and one allocation row per debit, then commit.
func (r *QuotaReservationRepo) Reserve(ctx context.Context, p ReserveParams) (ReserveResult, error) {
	if p.AccountID == "" || p.RequestID == "" || p.AttemptID == "" {
		return ReserveResult{}, fmt.Errorf("%w: account id, request id, and attempt id must all be non-empty", ErrInvalidReserveParams)
	}
	if len(p.Allocations) == 0 {
		return ReserveResult{}, fmt.Errorf("%w: at least one allocation required", ErrInvalidReserveParams)
	}

	reservationID, err := quota.ReservationID(p.RequestID, p.AttemptID)
	if err != nil {
		return ReserveResult{}, fmt.Errorf("%w: %v", ErrInvalidReserveParams, err)
	}

	conn, err := r.db.Conn().Conn(ctx)
	if err != nil {
		return ReserveResult{}, fmt.Errorf("storage: acquire connection for reserve: %w", err)
	}
	defer func() { _ = conn.Close() }()

	// database/sql's Tx always issues a plain (DEFERRED) BEGIN — there is
	// no per-call option to request IMMEDIATE — and the DSN-wide
	// "_txlock=immediate" alternative is forbidden here (it would
	// silently make EVERY transaction in the project immediate, not just
	// this one). So this transaction is driven by hand with raw
	// BEGIN IMMEDIATE / COMMIT / ROLLBACK statements issued on one
	// borrowed *sql.Conn, and every subsequent statement runs on that
	// same conn — the one way to get an immediate write lock through
	// database/sql with this driver without touching Open's pragmas.
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return ReserveResult{}, fmt.Errorf("storage: begin immediate reserve tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			// A background context: ctx may already be past its deadline
			// by the time this runs, but the rollback must still be
			// attempted so the connection isn't returned to the pool
			// mid-transaction.
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	var existingState string
	err = conn.QueryRowContext(ctx,
		`SELECT state FROM quota_reservations WHERE request_id = ? AND attempt_id = ?`,
		p.RequestID, p.AttemptID,
	).Scan(&existingState)
	if err == nil {
		if _, cerr := conn.ExecContext(ctx, "COMMIT"); cerr != nil {
			return ReserveResult{}, fmt.Errorf("storage: commit idempotent reserve %q: %w", reservationID, cerr)
		}
		committed = true
		return ReserveResult{ReservationID: reservationID, Idempotent: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ReserveResult{}, fmt.Errorf("storage: check existing reservation %q: %w", reservationID, err)
	}

	windows, err := listWindowsOnConn(ctx, conn, p.AccountID)
	if err != nil {
		return ReserveResult{}, err
	}

	debits, err := quota.ApplicableDebits(windows, p.Allocations)
	if err != nil {
		return ReserveResult{}, err
	}

	epoch := r.now().Unix()
	for _, d := range debits {
		affected, err := reserveOnWindow(ctx, conn, d.WindowID, d.ExpectedVersion, d.Cost, epoch)
		if err != nil {
			return ReserveResult{}, fmt.Errorf("storage: reserve on window %q: %w", d.WindowID, err)
		}
		if !affected {
			return ReserveResult{}, ErrReservationRejected
		}
	}

	expiresAt := epoch + int64(quota.DefaultProcessingDeadline.Seconds())
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO quota_reservations (id, account_id, request_id, attempt_id, state, dispatched_at, expires_at, created_at)
		 VALUES (?, ?, ?, ?, 'reserved', NULL, ?, ?)`,
		reservationID, p.AccountID, p.RequestID, p.AttemptID, expiresAt, epoch,
	); err != nil {
		return ReserveResult{}, fmt.Errorf("storage: insert reservation %q: %w", reservationID, err)
	}

	for _, d := range debits {
		if _, err := conn.ExecContext(ctx,
			`INSERT INTO quota_reservation_allocations (reservation_id, window_id, unit, estimated_cost, estimate_source, actual_cost, state)
			 VALUES (?, ?, ?, ?, ?, NULL, 'reserved')`,
			reservationID, d.WindowID, string(d.Unit), d.Cost, string(d.EstimateSource),
		); err != nil {
			return ReserveResult{}, fmt.Errorf("storage: insert allocation (%q,%q): %w", reservationID, d.WindowID, err)
		}
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return ReserveResult{}, fmt.Errorf("storage: commit reserve %q: %w", reservationID, err)
	}
	committed = true

	return ReserveResult{ReservationID: reservationID, Debits: debits}, nil
}

// reserveOnWindow issues the ONE canonical conditional UPDATE (02 §3) for
// a single window, inside the caller's already-open BEGIN IMMEDIATE
// transaction on conn. It reports whether exactly one row was affected;
// the caller owns rolling back the whole transaction on a false result —
// this function never retries.
func reserveOnWindow(ctx context.Context, conn *sql.Conn, windowID string, expectedVersion int64, cost float64, updatedAt int64) (bool, error) {
	result, err := conn.ExecContext(ctx,
		`UPDATE quota_windows
		 SET reserved = reserved + ?, version = version + 1, updated_at = ?
		 WHERE id = ? AND version = ?
		   AND COALESCE(remaining, limit_value) - reserved >= ?`,
		cost, updatedAt, windowID, expectedVersion, cost,
	)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

// listWindowsOnConn reads accountID's windows through conn — the SAME
// connection the caller's transaction is already running on. This must
// NEVER be replaced with QuotaWindowRepo.ListByAccount: that method
// issues its query against r.db.Conn() directly, and with
// SetMaxOpenConns(1) the pool has no second connection to hand out while
// this transaction holds the only one — doing so would deadlock (this
// project has already been bitten once by exactly this shape of bug in
// P3a-CAPI-001).
func listWindowsOnConn(ctx context.Context, conn *sql.Conn, accountID string) ([]quota.Window, error) {
	rows, err := conn.QueryContext(ctx,
		`SELECT id, account_id, source, unit, window_type, window_key, duration_seconds,
		        used, remaining, total, reserved, limit_value, reset_at, version, confidence,
		        freshness_state, observed_at
		 FROM quota_windows
		 WHERE account_id = ?
		 ORDER BY id`,
		accountID,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: list quota windows for %q: %w", accountID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []quota.Window
	for rows.Next() {
		w, err := scanQuotaWindow(rows)
		if err != nil {
			return nil, fmt.Errorf("storage: scan quota window: %w", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: list quota windows for %q: %w", accountID, err)
	}
	return out, nil
}
