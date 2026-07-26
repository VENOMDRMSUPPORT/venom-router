package storage

import (
	"context"
	"errors"
	"fmt"
)

// ErrInvalidRebaselineFlag is returned by FlagRebaseline for an empty
// account id or reason code.
var ErrInvalidRebaselineFlag = errors.New("storage: invalid rebaseline flag")

// FlagRebaseline idempotently flags accountID for re-baseline at the
// next authoritative quota sync (05 §4: "flag the account for
// re-baseline at the next quota sync"). An existing flag keeps its
// ORIGINAL flagged_at and reason_code — the first signal that triggered
// re-baselining is the one worth keeping, not whichever call happened
// to land last.
func (r *ReconciliationRepo) FlagRebaseline(ctx context.Context, accountID, reasonCode string) error {
	if accountID == "" || reasonCode == "" {
		return fmt.Errorf("%w: account id and reason code required", ErrInvalidRebaselineFlag)
	}
	if _, err := r.db.Conn().ExecContext(ctx,
		`INSERT INTO quota_rebaseline_flags (account_id, reason_code, flagged_at) VALUES (?, ?, ?)
		 ON CONFLICT(account_id) DO NOTHING`,
		accountID, reasonCode, r.now().Unix(),
	); err != nil {
		return fmt.Errorf("storage: flag rebaseline for %q: %w", accountID, err)
	}
	return nil
}

// RebaselineFlagged returns every flagged account id, ordered
// deterministically — the surface P3b-CAPI-002's diagnostics endpoint
// consumes.
func (r *ReconciliationRepo) RebaselineFlagged(ctx context.Context) ([]string, error) {
	rows, err := r.db.Conn().QueryContext(ctx, `SELECT account_id FROM quota_rebaseline_flags ORDER BY account_id`)
	if err != nil {
		return nil, fmt.Errorf("storage: list rebaseline flags: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("storage: scan rebaseline flag: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: list rebaseline flags: %w", err)
	}
	return out, nil
}

// ClearRebaseline removes accountID's re-baseline flag, if any — this is
// what "re-baselined at the next quota sync" means operationally
// (SyncQuotaWindows calls this on a successful authoritative sync).
// Clearing an account with no flag is a harmless no-op.
func (r *ReconciliationRepo) ClearRebaseline(ctx context.Context, accountID string) error {
	if _, err := r.db.Conn().ExecContext(ctx, `DELETE FROM quota_rebaseline_flags WHERE account_id = ?`, accountID); err != nil {
		return fmt.Errorf("storage: clear rebaseline for %q: %w", accountID, err)
	}
	return nil
}
