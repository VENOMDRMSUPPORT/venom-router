package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
)

// accountSelectColumns lists every accounts column AccountRepo reads, in
// scanAccount's exact scan order.
const accountSelectColumns = `id, provider_id, external_id, display_name, label, auth_type, connection_state, health_state, reauth_in_progress, identity_email, identity_plan, last_health_check_at, last_health_error, created_at, updated_at`

// AccountRepo reads and updates accounts rows (M2) into domain.Account. Row
// creation happens only through EnrollmentRepo.CreateConnectedAccount's
// atomic transaction (P2b-PROV-003); the lifecycle writes here (P2b-
// CAPI-004) only ever UPDATE an existing row to persist a pure-domain
// transition decision, never create or delete one.
type AccountRepo struct {
	db *DB
}

// NewAccountRepo builds a repository over db's existing connection.
func NewAccountRepo(db *DB) *AccountRepo {
	return &AccountRepo{db: db}
}

// GetByID reads back a single account by id. ok is false if none exists.
func (r *AccountRepo) GetByID(ctx context.Context, id string) (domain.Account, bool, error) {
	row := r.db.Conn().QueryRowContext(ctx, `SELECT `+accountSelectColumns+` FROM accounts WHERE id = ?`, id)
	acc, ok, err := scanAccount(row)
	if err != nil {
		return domain.Account{}, false, fmt.Errorf("storage: get account %q: %w", id, err)
	}
	return acc, ok, nil
}

// GetByProviderExternalID reads back the account uniquely identified by
// (provider_id, external_id) — the M2 UNIQUE(provider_id, external_id)
// constraint's read-side counterpart. ok is false if none exists.
func (r *AccountRepo) GetByProviderExternalID(ctx context.Context, providerID, externalID string) (domain.Account, bool, error) {
	row := r.db.Conn().QueryRowContext(ctx,
		`SELECT `+accountSelectColumns+` FROM accounts WHERE provider_id = ? AND external_id = ?`,
		providerID, externalID,
	)
	acc, ok, err := scanAccount(row)
	if err != nil {
		return domain.Account{}, false, fmt.Errorf("storage: get account by (provider,external_id) (%q,%q): %w", providerID, externalID, err)
	}
	return acc, ok, nil
}

// defaultAccountListLimit bounds a List call that did not supply a sane
// limit.
const defaultAccountListLimit = 50

// List returns up to limit accounts in stable id order, optionally
// resuming after cursor (an account id; "" starts from the beginning) and
// optionally restricted to providerID ("" = all providers). nextCursor is
// the id of the next page's first row, or "" when this is the last page.
// The stable id ordering is what makes the cursor unambiguous and the
// page deterministic, exactly mirroring how the rest of this package keys
// its single-row lookups.
func (r *AccountRepo) List(ctx context.Context, cursor string, limit int, providerID string) ([]domain.Account, string, error) {
	if limit <= 0 {
		limit = defaultAccountListLimit
	}

	var (
		query  strings.Builder
		args   []any
		first  = true
		addSep = func() {
			if first {
				query.WriteString(" WHERE ")
				first = false
			} else {
				query.WriteString(" AND ")
			}
		}
	)
	query.WriteString(`SELECT ` + accountSelectColumns + ` FROM accounts`)
	if providerID != "" {
		addSep()
		query.WriteString("provider_id = ?")
		args = append(args, providerID)
	}
	if cursor != "" {
		addSep()
		query.WriteString("id > ?")
		args = append(args, cursor)
	}
	query.WriteString(" ORDER BY id ASC LIMIT ?")
	args = append(args, limit+1) // over-fetch by one to detect a next page

	rows, err := r.db.Conn().QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, "", fmt.Errorf("storage: list accounts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var (
		out         []domain.Account
		lastID      string
		overFetched bool
	)
	for rows.Next() {
		acc, ok, err := scanRowsAccount(rows)
		if err != nil {
			return nil, "", fmt.Errorf("storage: list accounts: scan: %w", err)
		}
		if !ok {
			continue
		}
		if len(out) == limit {
			// This is the limit+1'th row: it exists only to prove a next
			// page follows. Do not include it in the returned page.
			overFetched = true
			lastID = acc.ID
			break
		}
		out = append(out, acc)
		lastID = acc.ID
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("storage: list accounts: %w", err)
	}

	nextCursor := ""
	if overFetched {
		nextCursor = lastID
	}
	return out, nextCursor, nil
}

// UpdateConnectionState durably sets accountID's connection_state and
// stamps updated_at = now, persisting a domain.TransitionConnection
// decision verbatim (this method does not re-validate the transition). ok
// is false if no account row matches accountID.
func (r *AccountRepo) UpdateConnectionState(ctx context.Context, accountID string, next domain.Account, now time.Time) (domain.Account, bool, error) {
	return r.updateAccountState(ctx, accountID,
		`connection_state = ?, updated_at = ?`,
		[]any{string(next.ConnectionState), now.Unix()},
		next,
		"update connection_state",
	)
}

// UpdateHealthState durably sets accountID's health_state and stamps
// updated_at = now, mirroring UpdateConnectionState's persist-a-domain-
// decision contract. ok is false if no account row matches accountID.
func (r *AccountRepo) UpdateHealthState(ctx context.Context, accountID string, next domain.Account, now time.Time) (domain.Account, bool, error) {
	return r.updateAccountState(ctx, accountID,
		`health_state = ?, updated_at = ?`,
		[]any{string(next.HealthState), now.Unix()},
		next,
		"update health_state",
	)
}

// UpdateHealthObservation durably sets accountID's health_state AND the
// live-probe evidence columns: last_health_check_at = checkedAt and
// last_health_error = lastHealthError ("" writes NULL — a healthy probe
// CLEARS the previous error rather than leaving it to read as current).
// Used only by the live-probe path; the body-target/no-probe path keeps
// using UpdateHealthState, which never touches the evidence columns.
func (r *AccountRepo) UpdateHealthObservation(ctx context.Context, accountID string, next domain.Account, checkedAt time.Time, lastHealthError string, now time.Time) (domain.Account, bool, error) {
	var errVal any
	if lastHealthError != "" {
		errVal = lastHealthError
	}
	return r.updateAccountState(ctx, accountID,
		`health_state = ?, last_health_check_at = ?, last_health_error = ?, updated_at = ?`,
		[]any{string(next.HealthState), checkedAt.Unix(), errVal, now.Unix()},
		next,
		"update health observation",
	)
}

// updateAccountState is the shared body of UpdateConnectionState and
// UpdateHealthState: run the guarded UPDATE, confirm exactly one row was
// affected, then read the updated row back (so the caller gets the
// stamped updated_at). The UPDATE's SET clause is setClause; its
// placeholder args (after accountID) are setArgs; next is the domain
// decision being persisted (its connection/health fields back the
// clause), label names the operation for error wrapping.
func (r *AccountRepo) updateAccountState(ctx context.Context, accountID, setClause string, setArgs []any, next domain.Account, label string) (domain.Account, bool, error) {
	args := append([]any{}, setArgs...)
	args = append(args, accountID)
	res, err := r.db.Conn().ExecContext(ctx,
		`UPDATE accounts SET `+setClause+` WHERE id = ?`,
		args...,
	)
	if err != nil {
		return domain.Account{}, false, fmt.Errorf("storage: %s for account %q: %w", label, accountID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return domain.Account{}, false, fmt.Errorf("storage: %s for account %q: rows affected: %w", label, accountID, err)
	}
	if affected == 0 {
		return domain.Account{}, false, nil
	}
	updated, ok, err := r.GetByID(ctx, accountID)
	if err != nil {
		return domain.Account{}, false, fmt.Errorf("storage: %s for account %q: re-read: %w", label, accountID, err)
	}
	if !ok {
		return domain.Account{}, false, nil
	}
	return updated, true, nil
}

// SoftDisconnect performs 02 §3's soft-disconnect in ONE transaction: it
// sets connection_state = next.ConnectionState (already validated by
// domain.TransitionConnection to be 'disconnected'), RETIRES every still-
// usable (active or staged) credential of the account, and clears
// reauth_in_progress — all in the same tx. It NEVER hard-deletes the
// account row or its history; a soft-disconnected account is retained and
// restorable only via re-enrollment. ok is false if no account row matches
// accountID (in which case the whole transaction is a no-op).
func (r *AccountRepo) SoftDisconnect(ctx context.Context, accountID string, next domain.Account, now time.Time) (domain.Account, bool, error) {
	tx, err := r.db.Conn().BeginTx(ctx, nil)
	if err != nil {
		return domain.Account{}, false, fmt.Errorf("storage: begin soft-disconnect tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has succeeded

	epoch := now.Unix()

	res, err := tx.ExecContext(ctx,
		`UPDATE accounts SET connection_state = ?, reauth_in_progress = 0, updated_at = ? WHERE id = ?`,
		string(next.ConnectionState), epoch, accountID,
	)
	if err != nil {
		return domain.Account{}, false, fmt.Errorf("storage: soft-disconnect: update account: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return domain.Account{}, false, fmt.Errorf("storage: soft-disconnect: rows affected: %w", err)
	}
	if affected == 0 {
		// No matching account: roll back (a no-op deferred rollback) and
		// report not-found rather than leaving a half-applied tx.
		return domain.Account{}, false, nil
	}

	// Retire every still-usable credential of the account in the same tx.
	// A 'retired' credential keeps its row (history is retained) but is no
	// longer usable: the partial-unique indexes only constrain active and
	// staged rows, so retiring frees any (account_id, kind) slot a re-
	// enrollment would later need.
	if _, err := tx.ExecContext(ctx,
		`UPDATE account_credentials SET state = 'retired', retired_at = ?, updated_at = ?
		 WHERE account_id = ? AND state IN ('active', 'staged')`,
		epoch, epoch, accountID,
	); err != nil {
		return domain.Account{}, false, fmt.Errorf("storage: soft-disconnect: retire credentials: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return domain.Account{}, false, fmt.Errorf("storage: soft-disconnect: commit: %w", err)
	}

	updated, ok, err := r.GetByID(ctx, accountID)
	if err != nil {
		return domain.Account{}, false, fmt.Errorf("storage: soft-disconnect: re-read: %w", err)
	}
	if !ok {
		return domain.Account{}, false, nil
	}
	return updated, true, nil
}

// Delete removes an account and every trace that should not outlive it,
// returning its provider to a pristine "available/awaiting-connection" state on
// every surface. Deleting the accounts row fires the ON DELETE CASCADE FKs that
// clean credentials, funding evidence, offerings, offering_operations,
// certifications, quota windows/reservations/allocations, cooldowns, rebaseline
// flags, discovery runs, and probe runs (+ costs). The FK-less orphan state that
// no cascade reaches — account-scoped circuit breakers and route attempts — is
// cleaned explicitly. Append-only history with no account FK (usage_records,
// audit_events) is deliberately retained as compliance/billing record.
//
// defer_foreign_keys is enabled for the transaction so the single NO ACTION edge
// inside the cascade (quota_reservation_allocations.window_id -> quota_windows)
// is checked at COMMIT — by which point both sides are gone and the graph is
// consistent — rather than transiently failing mid-cascade.
//
// deleted is false (with a nil error) when no account had that id.
func (r *AccountRepo) Delete(ctx context.Context, accountID string) (bool, error) {
	tx, err := r.db.Conn().BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("storage: begin delete-account tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has succeeded

	if _, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
		return false, fmt.Errorf("storage: delete-account: defer fks: %w", err)
	}

	// FK-less orphans: no cascade reaches these, so clean them explicitly.
	if _, err := tx.ExecContext(ctx, `DELETE FROM route_attempts WHERE account_id = ?`, accountID); err != nil {
		return false, fmt.Errorf("storage: delete-account: route_attempts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM circuit_breakers WHERE scope = 'account' AND scope_id = ?`, accountID); err != nil {
		return false, fmt.Errorf("storage: delete-account: circuit_breakers: %w", err)
	}

	res, err := tx.ExecContext(ctx, `DELETE FROM accounts WHERE id = ?`, accountID)
	if err != nil {
		return false, fmt.Errorf("storage: delete-account: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("storage: delete-account: rows affected: %w", err)
	}
	if affected == 0 {
		return false, nil // deferred rollback; nothing existed
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("storage: delete-account: commit: %w", err)
	}
	return true, nil
}

// scanner is the shared shape of *sql.Row's and *sql.Rows' Scan method,
// so scanAccount (single row) and scanRowsAccount (one row of a result
// set) share one column-binding implementation.
type scanner interface {
	Scan(dest ...any) error
}

func scanAccount(row *sql.Row) (domain.Account, bool, error) {
	return scanOneAccount(row)
}

// scanRowsAccount binds one already-Next'd row of a multi-row result set
// into a domain.Account. ok is false only on sql.ErrNoRows, which *sql.Rows
// callers never see in practice (they iterate via Next); it is preserved
// here for symmetry with scanOneAccount.
func scanRowsAccount(rows *sql.Rows) (domain.Account, bool, error) {
	return scanOneAccount(rows)
}

func scanOneAccount(s scanner) (domain.Account, bool, error) {
	var (
		a                                                              domain.Account
		connectionState, healthState                                   string
		reauthInProgress                                               int
		displayName, label, identityEmail, identityPlan, lastHealthError sql.NullString
		lastHealthCheckAt                                              sql.NullInt64
		createdAt, updatedAt                                           int64
	)
	err := s.Scan(
		&a.ID, &a.ProviderID, &a.ExternalID, &displayName, &label, &a.AuthType,
		&connectionState, &healthState, &reauthInProgress,
		&identityEmail, &identityPlan, &lastHealthCheckAt, &lastHealthError,
		&createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Account{}, false, nil
	}
	if err != nil {
		return domain.Account{}, false, err
	}

	a.DisplayName = displayName.String
	a.Label = label.String
	a.IdentityEmail = identityEmail.String
	a.IdentityPlan = identityPlan.String
	a.LastHealthError = lastHealthError.String
	a.ConnectionState = domain.ConnectionState(connectionState)
	a.HealthState = domain.HealthState(healthState)
	a.ReauthInProgress = reauthInProgress != 0
	if lastHealthCheckAt.Valid {
		t := time.Unix(lastHealthCheckAt.Int64, 0).UTC()
		a.LastHealthCheckAt = &t
	}
	a.CreatedAt = time.Unix(createdAt, 0).UTC()
	a.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return a, true, nil
}

// UpdateLabel sets (or clears, when label is "") accountID's label column and
// stamps updated_at = now. ok is false if no account row matches accountID.
func (r *AccountRepo) UpdateLabel(ctx context.Context, accountID, label string, now time.Time) (bool, error) {
	var labelVal any
	if label != "" {
		labelVal = label
	}
	res, err := r.db.Conn().ExecContext(ctx,
		`UPDATE accounts SET label = ?, updated_at = ? WHERE id = ?`,
		labelVal, now.Unix(), accountID,
	)
	if err != nil {
		return false, fmt.Errorf("storage: update label for account %q: %w", accountID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("storage: update label for account %q: rows affected: %w", accountID, err)
	}
	return affected > 0, nil
}
