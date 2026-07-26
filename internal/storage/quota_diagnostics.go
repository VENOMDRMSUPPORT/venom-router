package storage

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// ReconciliationAllocation is one reservation-allocation's diagnostic
// projection (05 §4 "Audit & retention: ids/costs/confidence only") — no
// prompt/response content exists in these tables and none may be added
// here.
type ReconciliationAllocation struct {
	WindowID         string
	Unit             string
	EstimatedCost    float64
	ActualCost       *float64
	ActualConfidence *string // nil = unsettled, never defaulted to "high"
	State            string
}

// ReconciliationItem is one reconciliation_pending or unknown_consumption
// reservation's diagnostic projection (P3b-CAPI-002, 09 §2 / 05 §4). Ids,
// costs, states, confidence and counts ONLY.
type ReconciliationItem struct {
	ReservationID     string
	AccountID         string
	RequestID         string
	AttemptID         string
	State             string
	Attempts          int
	Leased            bool
	DispatchedAt      *int64
	ExpiresAt         int64
	RebaselineFlagged bool
	Allocations       []ReconciliationAllocation
}

// reconciliationCursorSeparator mirrors encodeCatalogCursor's separator
// convention (catalog.go) — an opaque base64 cursor over this list's own
// deterministic order key, (expires_at, id).
const reconciliationCursorSeparator = "\x00"

func encodeReconciliationCursor(expiresAt int64, id string) string {
	raw := fmt.Sprintf("%d%s%s", expiresAt, reconciliationCursorSeparator, id)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeReconciliationCursor(cursor string) (expiresAt int64, id string, ok bool) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, "", false
	}
	parts := strings.SplitN(string(raw), reconciliationCursorSeparator, 2)
	if len(parts) != 2 {
		return 0, "", false
	}
	var e int64
	if _, err := fmt.Sscanf(parts[0], "%d", &e); err != nil {
		return 0, "", false
	}
	return e, parts[1], true
}

// ListReconciliationItems returns up to limit reconciliation_pending and
// unknown_consumption reservations (P3b-CAPI-002's diagnostics read
// model), ordered deterministically by (expires_at, id) — settled/
// released/reserved reservations never appear here. nextCursor is "" on
// the last page.
//
// This issues ONE query for the page of reservations, closes that
// cursor, and only THEN issues the bounded per-item allocation reads
// (never a nested query while the reservation cursor is still open — the
// P3a-CAPI-001 deadlock this package has already hit).
func (r *ReconciliationRepo) ListReconciliationItems(ctx context.Context, limit int, cursor string) ([]ReconciliationItem, string, error) {
	if limit <= 0 {
		limit = defaultCatalogListLimit
	}

	var (
		query strings.Builder
		args  []any
	)
	query.WriteString(`SELECT id, account_id, request_id, attempt_id, state, reconcile_attempts,
		       lease_owner, lease_expires_at, dispatched_at, expires_at
		  FROM quota_reservations
		 WHERE state IN ('reconciliation_pending', 'unknown_consumption')`)

	if cursor != "" {
		cursorExpiresAt, cursorID, ok := decodeReconciliationCursor(cursor)
		if ok {
			query.WriteString(" AND (expires_at > ? OR (expires_at = ? AND id > ?))")
			args = append(args, cursorExpiresAt, cursorExpiresAt, cursorID)
		}
	}
	query.WriteString(" ORDER BY expires_at ASC, id ASC LIMIT ?")
	args = append(args, limit+1) // over-fetch by one to detect a next page

	rows, err := r.db.Conn().QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, "", fmt.Errorf("storage: list reconciliation items: %w", err)
	}

	type reservationRow struct {
		id, accountID, requestID, attemptID, state string
		attempts                                   int64
		leaseOwner                                 sql.NullString
		leaseExpiresAt, dispatchedAt               sql.NullInt64
		expiresAt                                  int64
	}
	var all []reservationRow
	for rows.Next() {
		var row reservationRow
		if err := rows.Scan(&row.id, &row.accountID, &row.requestID, &row.attemptID, &row.state,
			&row.attempts, &row.leaseOwner, &row.leaseExpiresAt, &row.dispatchedAt, &row.expiresAt); err != nil {
			_ = rows.Close()
			return nil, "", fmt.Errorf("storage: scan reconciliation item: %w", err)
		}
		all = append(all, row)
	}
	rowsErr := rows.Err()
	if err := rows.Close(); err != nil {
		return nil, "", fmt.Errorf("storage: list reconciliation items: close: %w", err)
	}
	if rowsErr != nil {
		return nil, "", fmt.Errorf("storage: list reconciliation items: %w", rowsErr)
	}

	overFetched := false
	if len(all) > limit {
		overFetched = true
		all = all[:limit]
	}

	now := r.now().Unix()
	out := make([]ReconciliationItem, 0, len(all))
	for _, row := range all {
		item := ReconciliationItem{
			ReservationID: row.id,
			AccountID:     row.accountID,
			RequestID:     row.requestID,
			AttemptID:     row.attemptID,
			State:         row.state,
			Attempts:      int(row.attempts),
			Leased:        row.leaseOwner.Valid && row.leaseExpiresAt.Valid && row.leaseExpiresAt.Int64 >= now,
			ExpiresAt:     row.expiresAt,
		}
		if row.dispatchedAt.Valid {
			v := row.dispatchedAt.Int64
			item.DispatchedAt = &v
		}

		flagged, err := r.accountFlaggedForRebaseline(ctx, row.accountID)
		if err != nil {
			return nil, "", err
		}
		item.RebaselineFlagged = flagged

		allocs, err := r.loadReconciliationAllocations(ctx, row.id)
		if err != nil {
			return nil, "", err
		}
		item.Allocations = allocs

		out = append(out, item)
	}

	nextCursor := ""
	if overFetched {
		last := all[len(all)-1]
		nextCursor = encodeReconciliationCursor(last.expiresAt, last.id)
	}
	return out, nextCursor, nil
}

func (r *ReconciliationRepo) accountFlaggedForRebaseline(ctx context.Context, accountID string) (bool, error) {
	var id string
	err := r.db.Conn().QueryRowContext(ctx, `SELECT account_id FROM quota_rebaseline_flags WHERE account_id = ?`, accountID).Scan(&id)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, fmt.Errorf("storage: check rebaseline flag for %q: %w", accountID, err)
}

// ReservationStateAndAccount reads a reservation's current state and
// account id — the minimal lookup P3b-CAPI-002's manual-recovery action
// needs to validate a request and audit it, without loading the full
// diagnostic projection ListReconciliationItems builds.
func (r *ReconciliationRepo) ReservationStateAndAccount(ctx context.Context, reservationID string) (state, accountID string, ok bool, err error) {
	err = r.db.Conn().QueryRowContext(ctx,
		`SELECT state, account_id FROM quota_reservations WHERE id = ?`, reservationID,
	).Scan(&state, &accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("storage: reservation state/account for %q: %w", reservationID, err)
	}
	return state, accountID, true, nil
}

// ResetForResync clears reservationID's lease AND resets
// reconcile_attempts to 0 (P3b-CAPI-002's "resync" manual recovery
// action — the owner explicitly granting a fresh retry budget). State,
// allocations, and every window are left completely untouched; the next
// ClaimPending re-picks the reservation as if it had never been
// attempted.
func (r *ReconciliationRepo) ResetForResync(ctx context.Context, reservationID string) error {
	if _, err := r.db.Conn().ExecContext(ctx,
		`UPDATE quota_reservations SET lease_owner = NULL, lease_expires_at = NULL, reconcile_attempts = 0 WHERE id = ?`,
		reservationID,
	); err != nil {
		return fmt.Errorf("storage: reset for resync %q: %w", reservationID, err)
	}
	return nil
}

func (r *ReconciliationRepo) loadReconciliationAllocations(ctx context.Context, reservationID string) ([]ReconciliationAllocation, error) {
	rows, err := r.db.Conn().QueryContext(ctx,
		`SELECT window_id, unit, estimated_cost, actual_cost, actual_confidence, state
		   FROM quota_reservation_allocations WHERE reservation_id = ? ORDER BY window_id`,
		reservationID,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: load reconciliation allocations for %q: %w", reservationID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []ReconciliationAllocation
	for rows.Next() {
		var (
			a                ReconciliationAllocation
			actualCost       sql.NullFloat64
			actualConfidence sql.NullString
		)
		if err := rows.Scan(&a.WindowID, &a.Unit, &a.EstimatedCost, &actualCost, &actualConfidence, &a.State); err != nil {
			return nil, fmt.Errorf("storage: scan reconciliation allocation for %q: %w", reservationID, err)
		}
		if actualCost.Valid {
			v := actualCost.Float64
			a.ActualCost = &v
		}
		if actualConfidence.Valid {
			v := actualConfidence.String
			a.ActualConfidence = &v
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: load reconciliation allocations for %q: %w", reservationID, err)
	}
	return out, nil
}
