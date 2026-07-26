package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

// CooldownRepo persists quota.Cooldown rows over the frozen M5 cooldowns
// table, keyed by the exactly-one-non-nil-identity-per-scope invariant
// its CHECK constraint and three partial unique indexes enforce.
type CooldownRepo struct {
	db  *DB
	now func() time.Time
}

// NewCooldownRepo builds a repository over db's existing connection. now
// defaults to time.Now when nil.
func NewCooldownRepo(db *DB, now func() time.Time) *CooldownRepo {
	if now == nil {
		now = time.Now
	}
	return &CooldownRepo{db: db, now: now}
}

// ErrInvalidCooldownScope is returned by SetCooldown/CooldownForScope for
// an unrecognized scope, or one whose identity arguments don't match the
// M5 CHECK constraint (exactly one of account/offering/provider id
// non-nil, matching scope).
var ErrInvalidCooldownScope = errors.New("storage: invalid cooldown scope")

// columnForScope maps a scope to its one identity column — the same
// mapping the M5 CHECK constraint enforces at the schema level.
func columnForScope(scope quota.CooldownScope) (string, error) {
	switch scope {
	case quota.CooldownScopeAccount:
		return "account_id", nil
	case quota.CooldownScopeOffering:
		return "offering_operation_id", nil
	case quota.CooldownScopeProvider:
		return "provider_id", nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidCooldownScope, scope)
	}
}

// scopeIdentityColumn maps a scope to its one identity column, and
// validates the caller supplied exactly that one id (the others nil) —
// the same shape M5's CHECK constraint enforces at the schema level.
func scopeIdentityColumn(scope quota.CooldownScope, accountID, offeringOperationID, providerID *string) (column, id string, err error) {
	column, err = columnForScope(scope)
	if err != nil {
		return "", "", err
	}
	switch scope {
	case quota.CooldownScopeAccount:
		if accountID == nil || offeringOperationID != nil || providerID != nil {
			return "", "", fmt.Errorf("%w: account scope requires accountID only", ErrInvalidCooldownScope)
		}
		return column, *accountID, nil
	case quota.CooldownScopeOffering:
		if offeringOperationID == nil || accountID != nil || providerID != nil {
			return "", "", fmt.Errorf("%w: offering scope requires offeringOperationID only", ErrInvalidCooldownScope)
		}
		return column, *offeringOperationID, nil
	default: // quota.CooldownScopeProvider
		if providerID == nil || accountID != nil || offeringOperationID != nil {
			return "", "", fmt.Errorf("%w: provider scope requires providerID only", ErrInvalidCooldownScope)
		}
		return column, *providerID, nil
	}
}

// SetCooldown UPSERTs a cooldown for the given scope+identity: an
// existing row for that identity has its reason_code/until/source/
// updated_at overwritten in place; otherwise a new row is inserted. The
// check-then-act happens inside one transaction so the whole call is
// atomic.
func (r *CooldownRepo) SetCooldown(ctx context.Context, scope quota.CooldownScope, accountID, offeringOperationID, providerID *string, reasonCode string, until time.Time, source quota.CooldownSource) error {
	column, idValue, err := scopeIdentityColumn(scope, accountID, offeringOperationID, providerID)
	if err != nil {
		return err
	}

	tx, err := r.db.Conn().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: begin set-cooldown tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has succeeded

	epoch := r.now().Unix()

	var existingID string
	err = tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT id FROM cooldowns WHERE scope = ? AND %s = ?`, column), string(scope), idValue).Scan(&existingID)
	switch {
	case err == nil:
		if _, err := tx.ExecContext(ctx,
			`UPDATE cooldowns SET reason_code = ?, until = ?, source = ?, updated_at = ? WHERE id = ?`,
			reasonCode, until.Unix(), string(source), epoch, existingID,
		); err != nil {
			return fmt.Errorf("storage: update cooldown %q: %w", existingID, err)
		}
	case errors.Is(err, sql.ErrNoRows):
		id := randomQuotaID()
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO cooldowns (id, scope, account_id, offering_operation_id, provider_id, reason_code, until, source, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, string(scope), nullableStringArg(accountID), nullableStringArg(offeringOperationID), nullableStringArg(providerID),
			reasonCode, until.Unix(), string(source), epoch, epoch,
		); err != nil {
			return fmt.Errorf("storage: insert cooldown (%s,%s): %w", scope, idValue, err)
		}
	default:
		return fmt.Errorf("storage: lookup cooldown (%s,%s): %w", scope, idValue, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: commit set-cooldown (%s,%s): %w", scope, idValue, err)
	}
	return nil
}

// GetActiveCooldown returns the most-recently-expiring active (not yet
// past Until, as of r.now()) cooldown for the given scope among any of
// ids — nil when none is active. Passing several ids lets a caller check
// a whole candidate set (e.g. several provider ids) in one call.
func (r *CooldownRepo) GetActiveCooldown(ctx context.Context, scope string, ids ...string) (*quota.Cooldown, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	parsedScope, err := quota.ParseCooldownScope(scope)
	if err != nil {
		return nil, err
	}
	column, err := columnForScope(parsedScope)
	if err != nil {
		return nil, err
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids)+2)
	args = append(args, scope)
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, r.now().Unix())

	query := fmt.Sprintf(
		`SELECT id, scope, account_id, offering_operation_id, provider_id, reason_code, until, source
		   FROM cooldowns
		  WHERE scope = ? AND %s IN (%s) AND until > ?
		  ORDER BY until DESC LIMIT 1`,
		column, placeholders,
	)
	row := r.db.Conn().QueryRowContext(ctx, query, args...)
	c, err := scanCooldown(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("storage: get active cooldown (%s): %w", scope, err)
	}
	return &c, nil
}

// ClearExpiredCooldowns deletes every cooldown whose Until has passed as
// of r.now(), returning the number of rows removed.
func (r *CooldownRepo) ClearExpiredCooldowns(ctx context.Context) (int, error) {
	res, err := r.db.Conn().ExecContext(ctx, `DELETE FROM cooldowns WHERE until < ?`, r.now().Unix())
	if err != nil {
		return 0, fmt.Errorf("storage: clear expired cooldowns: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("storage: clear expired cooldowns: rows affected: %w", err)
	}
	return int(n), nil
}

// CooldownForScope returns every cooldown matching scope, with correct
// IS NULL semantics on the two identity columns scope does not use — a
// nil pointer means "this column must be NULL", a non-nil pointer means
// "this column must equal *pointer", mirroring the M5 CHECK constraint
// exactly.
func (r *CooldownRepo) CooldownForScope(ctx context.Context, scope string, accountID, offeringOperationID, providerID *string) ([]quota.Cooldown, error) {
	if _, err := quota.ParseCooldownScope(scope); err != nil {
		return nil, err
	}

	clause, args := identityClause("account_id", accountID)
	clause2, args2 := identityClause("offering_operation_id", offeringOperationID)
	clause3, args3 := identityClause("provider_id", providerID)

	query := fmt.Sprintf(
		`SELECT id, scope, account_id, offering_operation_id, provider_id, reason_code, until, source
		   FROM cooldowns
		  WHERE scope = ? AND %s AND %s AND %s
		  ORDER BY id`,
		clause, clause2, clause3,
	)
	args = append(append(append([]any{scope}, args...), args2...), args3...)

	rows, err := r.db.Conn().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: list cooldowns for scope %q: %w", scope, err)
	}
	defer func() { _ = rows.Close() }()

	var out []quota.Cooldown
	for rows.Next() {
		c, err := scanCooldownRows(rows)
		if err != nil {
			return nil, fmt.Errorf("storage: scan cooldown: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: list cooldowns for scope %q: %w", scope, err)
	}
	return out, nil
}

// identityClause returns "<column> = ?" with the value arg when id is
// non-nil, or "<column> IS NULL" with no arg when id is nil.
func identityClause(column string, id *string) (string, []any) {
	if id == nil {
		return column + " IS NULL", nil
	}
	return column + " = ?", []any{*id}
}

func nullableStringArg(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

type cooldownScanner interface {
	Scan(dest ...any) error
}

func scanCooldown(row *sql.Row) (quota.Cooldown, error) {
	return scanCooldownRow(row)
}

func scanCooldownRows(rows *sql.Rows) (quota.Cooldown, error) {
	return scanCooldownRow(rows)
}

func scanCooldownRow(scanner cooldownScanner) (quota.Cooldown, error) {
	var (
		c                                          quota.Cooldown
		scope, reasonCode, source                  string
		accountID, offeringOperationID, providerID sql.NullString
		until                                      int64
	)
	if err := scanner.Scan(&c.ID, &scope, &accountID, &offeringOperationID, &providerID, &reasonCode, &until, &source); err != nil {
		return quota.Cooldown{}, err
	}
	c.Scope = quota.CooldownScope(scope)
	c.ReasonCode = reasonCode
	c.Source = quota.CooldownSource(source)
	c.Until = time.Unix(until, 0).UTC()
	if accountID.Valid {
		v := accountID.String
		c.AccountID = &v
	}
	if offeringOperationID.Valid {
		v := offeringOperationID.String
		c.OfferingOperationID = &v
	}
	if providerID.Valid {
		v := providerID.String
		c.ProviderID = &v
	}
	return c, nil
}

// writeCooldownOnTx UPSERTs one cooldown from a quota.CooldownTrigger
// through an ALREADY-OPEN transaction (tx) — never through
// CooldownRepo's own pool-based SetCooldown, which would try to open a
// SECOND connection and deadlock while the caller's transaction
// (SyncQuotaWindows) still holds the pool's one connection
// (SetMaxOpenConns(1)). Same UPSERT shape as SetCooldown: an existing
// row for the trigger's (scope, scope ref) is updated in place; a
// missing one is inserted new.
func writeCooldownOnTx(ctx context.Context, tx *sql.Tx, epoch int64, trigger quota.CooldownTrigger) error {
	column, err := columnForScope(trigger.Scope)
	if err != nil {
		return err
	}

	var existingID string
	err = tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT id FROM cooldowns WHERE scope = ? AND %s = ?`, column), string(trigger.Scope), trigger.ScopeRef).Scan(&existingID)
	switch {
	case err == nil:
		if _, err := tx.ExecContext(ctx,
			`UPDATE cooldowns SET reason_code = ?, until = ?, source = ?, updated_at = ? WHERE id = ?`,
			trigger.ReasonCode, trigger.Until.Unix(), string(trigger.Source), epoch, existingID,
		); err != nil {
			return fmt.Errorf("storage: update cooldown %q: %w", existingID, err)
		}
		return nil
	case errors.Is(err, sql.ErrNoRows):
		id := randomQuotaID()
		var accountArg, offeringArg, providerArg any
		switch trigger.Scope {
		case quota.CooldownScopeAccount:
			accountArg = trigger.ScopeRef
		case quota.CooldownScopeOffering:
			offeringArg = trigger.ScopeRef
		case quota.CooldownScopeProvider:
			providerArg = trigger.ScopeRef
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO cooldowns (id, scope, account_id, offering_operation_id, provider_id, reason_code, until, source, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, string(trigger.Scope), accountArg, offeringArg, providerArg, trigger.ReasonCode, trigger.Until.Unix(), string(trigger.Source), epoch, epoch,
		); err != nil {
			return fmt.Errorf("storage: insert cooldown (%s,%s): %w", trigger.Scope, trigger.ScopeRef, err)
		}
		return nil
	default:
		return fmt.Errorf("storage: lookup cooldown (%s,%s): %w", trigger.Scope, trigger.ScopeRef, err)
	}
}
