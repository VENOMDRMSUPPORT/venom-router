package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

// QuotaWindowRepo persists quota.Window rows over the frozen M5
// quota_windows table. Its EnsureLocalSafetyWindows method is the ONE
// place the mandatory local-safety windows (02 §3, quota.WindowSpec /
// quota.LocalSafetyPolicy) are provisioned.
//
// Governor-set spec decision: provisioning is an idempotent ENSURE, not
// a "create on connect" event. 02 §3 states an invariant of state —
// "every connected account owns a local_safety source" — not an event
// tied to a particular enrollment call site. Accounts enrolled before
// this unit predate quota entirely; a create-on-connect rule would leave
// every one of them unbounded, which is exactly the failure §3 exists to
// prevent. An idempotent ensure is strictly stronger: it holds for
// pre-existing accounts, after a restore, and after a crash. The
// connect-time/admission-time CALL SITE (wiring this into enrollment or
// admission) is a later unit's concern — this type only provides the
// idempotent capability.
type QuotaWindowRepo struct {
	db    *DB
	newID func() string
	now   func() time.Time
}

// NewQuotaWindowRepo builds a repository over db's existing connection.
// newID mints fresh quota_windows row ids, defaulting to a crypto/rand
// hex generator when nil. now supplies the repo's clock, defaulting to
// time.Now when nil (mirrors NewDiscoveryRepo's newID-defaulting
// pattern) — tests inject a fixed clock for determinism.
func NewQuotaWindowRepo(db *DB, newID func() string, now func() time.Time) *QuotaWindowRepo {
	if newID == nil {
		newID = randomQuotaID
	}
	if now == nil {
		now = time.Now
	}
	return &QuotaWindowRepo{db: db, newID: newID, now: now}
}

func randomQuotaID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read is documented to never fail on this module's
		// supported platforms; this fallback only avoids a panic in the
		// theoretical case it ever does.
		return fmt.Sprintf("quota-fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// ErrInvalidLocalSafetySpec is returned by EnsureLocalSafetyWindows for
// an empty accountID, an empty spec slice, or any spec whose Source is
// not quota.SourceLocalSafety.
var ErrInvalidLocalSafetySpec = errors.New("storage: invalid local-safety window spec")

// EnsureLocalSafetyWindows idempotently creates any of specs' windows
// that do not already exist for accountID, identified by
// (account_id, source, unit, window_type, window_key) — the same
// identity the quota_windows UNIQUE constraint enforces. An existing
// window is left COMPLETELY untouched (reserved, limit_value, version,
// created_at all survive unchanged); calling this twice with the same
// specs is a no-op the second time.
//
// The whole call is rejected — nothing written — if accountID is empty,
// specs is empty, or ANY spec's Source is not quota.SourceLocalSafety: a
// provider-evidence or owner-override window must never be created
// through this door (02 §3: sources are never conflated). Validation
// happens before any statement runs, and the whole ensure is one
// transaction, so a rejected call is provably all-or-nothing.
func (r *QuotaWindowRepo) EnsureLocalSafetyWindows(ctx context.Context, accountID string, specs []quota.WindowSpec) error {
	if accountID == "" {
		return fmt.Errorf("%w: account id required", ErrInvalidLocalSafetySpec)
	}
	if len(specs) == 0 {
		return fmt.Errorf("%w: at least one window spec required", ErrInvalidLocalSafetySpec)
	}
	for _, spec := range specs {
		if spec.Source != quota.SourceLocalSafety {
			return fmt.Errorf("%w: source %q is not local_safety", ErrInvalidLocalSafetySpec, spec.Source)
		}
	}

	tx, err := r.db.Conn().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: begin ensure local-safety windows tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has succeeded

	epoch := r.now().Unix()
	for _, spec := range specs {
		exists, err := quotaWindowExists(ctx, tx, accountID, spec)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if err := insertQuotaWindowSpec(ctx, tx, r.newID(), accountID, spec, epoch); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: ensure local-safety windows for %q: commit: %w", accountID, err)
	}
	return nil
}

func quotaWindowExists(ctx context.Context, tx *sql.Tx, accountID string, spec quota.WindowSpec) (bool, error) {
	var existingID string
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM quota_windows WHERE account_id = ? AND source = ? AND unit = ? AND window_type = ? AND window_key = ?`,
		accountID, string(spec.Source), string(spec.Unit), spec.WindowType, spec.Key,
	).Scan(&existingID)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, fmt.Errorf("storage: lookup quota window (%q,%q,%q,%q,%q): %w", accountID, spec.Source, spec.Unit, spec.WindowType, spec.Key, err)
}

func insertQuotaWindowSpec(ctx context.Context, tx *sql.Tx, id, accountID string, spec quota.WindowSpec, epoch int64) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO quota_windows
		    (id, account_id, source, unit, window_type, window_key, duration_seconds, reserved, limit_value, version, confidence, freshness_state, observed_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, 1, ?, ?, ?, ?, ?)`,
		id, accountID, string(spec.Source), string(spec.Unit), spec.WindowType, spec.Key,
		intPtrArg(spec.DurationSeconds), spec.LimitValue, spec.Confidence, string(spec.Freshness),
		epoch, epoch, epoch,
	)
	if err != nil {
		return fmt.Errorf("storage: insert quota window (%q,%q): %w", accountID, spec.Key, err)
	}
	return nil
}

// ListByAccount returns accountID's quota windows ordered deterministically
// by (source, unit, window_type, window_key). NULL numeric columns map
// to nil pointers (unknown, never 0-as-unknown).
func (r *QuotaWindowRepo) ListByAccount(ctx context.Context, accountID string) ([]quota.Window, error) {
	rows, err := r.db.Conn().QueryContext(ctx,
		`SELECT id, account_id, source, unit, window_type, window_key, duration_seconds,
		        used, remaining, total, reserved, limit_value, reset_at, version, confidence,
		        freshness_state, observed_at
		 FROM quota_windows
		 WHERE account_id = ?
		 ORDER BY source, unit, window_type, window_key`,
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

// ListByAccounts loads every quota window for a whole page of accounts in
// ONE query, keyed by account id. The list path MUST use this instead of
// calling ListByAccount per row: a per-row query issued while the accounts
// cursor is still open deadlocks under SetMaxOpenConns(1).
//
// An empty accountIDs returns an empty (non-nil) map and no error — no
// query is issued. Each account's windows are ordered deterministically by
// (source, unit, window_type, window_key), the same canonical order
// ListByAccount uses; an account with no windows is simply absent from the
// returned map (callers must not treat a missing key as an error).
func (r *QuotaWindowRepo) ListByAccounts(ctx context.Context, accountIDs []string) (map[string][]quota.Window, error) {
	out := make(map[string][]quota.Window)
	if len(accountIDs) == 0 {
		return out, nil
	}

	placeholders := make([]string, len(accountIDs))
	args := make([]any, len(accountIDs))
	for i, id := range accountIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	query := fmt.Sprintf(
		`SELECT id, account_id, source, unit, window_type, window_key, duration_seconds,
		        used, remaining, total, reserved, limit_value, reset_at, version, confidence,
		        freshness_state, observed_at
		 FROM quota_windows
		 WHERE account_id IN (%s)
		 ORDER BY account_id, source, unit, window_type, window_key`,
		strings.Join(placeholders, ","),
	)

	rows, err := r.db.Conn().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: list quota windows for %d accounts: %w", len(accountIDs), err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		w, err := scanQuotaWindow(rows)
		if err != nil {
			return nil, fmt.Errorf("storage: scan quota window: %w", err)
		}
		out[w.AccountID] = append(out[w.AccountID], w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: list quota windows for %d accounts: %w", len(accountIDs), err)
	}
	return out, nil
}

func scanQuotaWindow(rows *sql.Rows) (quota.Window, error) {
	var (
		w                                  quota.Window
		source, unit, freshness            string
		durationSeconds                    sql.NullInt64
		used, remaining, total, limitValue sql.NullFloat64
		resetAt                            sql.NullInt64
		observedAtUnix                     int64
	)
	if err := rows.Scan(
		&w.ID, &w.AccountID, &source, &unit, &w.WindowType, &w.Key, &durationSeconds,
		&used, &remaining, &total, &w.Reserved, &limitValue, &resetAt, &w.Version, &w.Confidence,
		&freshness, &observedAtUnix,
	); err != nil {
		return quota.Window{}, err
	}

	w.Source = quota.Source(source)
	w.Unit = quota.Unit(unit)
	w.Freshness = quota.Freshness(freshness)
	w.ObservedAt = time.Unix(observedAtUnix, 0).UTC()
	if durationSeconds.Valid {
		d := int(durationSeconds.Int64)
		w.DurationSeconds = &d
	}
	if used.Valid {
		v := used.Float64
		w.Used = &v
	}
	if remaining.Valid {
		v := remaining.Float64
		w.Remaining = &v
	}
	if total.Valid {
		v := total.Float64
		w.Total = &v
	}
	if limitValue.Valid {
		v := limitValue.Float64
		w.LimitValue = &v
	}
	if resetAt.Valid {
		v := resetAt.Int64
		w.ResetAt = &v
	}
	return w, nil
}
