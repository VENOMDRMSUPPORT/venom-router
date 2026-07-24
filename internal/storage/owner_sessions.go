package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// timestampLayout matches the baseline/M1 migrations' own
// strftime('%Y-%m-%dT%H:%M:%fZ','now') ISO-8601-with-milliseconds
// format, so rows written by this package sort and parse identically to
// rows the DB itself default-stamps.
const timestampLayout = "2006-01-02T15:04:05.000Z"

func formatTimestamp(t time.Time) string {
	return t.UTC().Format(timestampLayout)
}

func parseTimestamp(s string) (time.Time, error) {
	t, err := time.Parse(timestampLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("storage: parse timestamp %q: %w", s, err)
	}
	return t, nil
}

// OwnerSessionRow is one owner_sessions row (M1, 00002_owner_auth.sql).
// TokenHash is the ONLY thing ever persisted for the session identifier
// — the raw opaque handle is minted by internal/secrets, handed to the
// caller as the cookie value, and never stored.
type OwnerSessionRow struct {
	TokenHash          []byte
	CreatedAt          time.Time
	LastSeenAt         time.Time
	IdleExpiresAt      time.Time
	AbsoluteExpiresAt  time.Time
	RevokedAt          *time.Time // nil = not revoked
	ReverifyFreshUntil *time.Time // nil = never re-verified (or freshness already consumed by a later stamp's absence)
}

// OwnerSessionRepo persists owner_sessions rows over the M1 schema.
type OwnerSessionRepo struct {
	db *DB
}

// NewOwnerSessionRepo builds a repository over db's existing connection.
func NewOwnerSessionRepo(db *DB) *OwnerSessionRepo {
	return &OwnerSessionRepo{db: db}
}

// Create inserts one owner_sessions row. revoked_at and
// reverify_fresh_until are left NULL (reverify_fresh_until is SEC-005's
// concern; revoked_at is only ever set later, by Revoke).
func (r *OwnerSessionRepo) Create(ctx context.Context, row OwnerSessionRow) error {
	_, err := r.db.Conn().ExecContext(ctx,
		`INSERT INTO owner_sessions (token_hash, created_at, last_seen_at, idle_expires_at, absolute_expires_at)
		 VALUES (?, ?, ?, ?, ?)`,
		row.TokenHash,
		formatTimestamp(row.CreatedAt),
		formatTimestamp(row.LastSeenAt),
		formatTimestamp(row.IdleExpiresAt),
		formatTimestamp(row.AbsoluteExpiresAt),
	)
	if err != nil {
		return fmt.Errorf("storage: create owner_sessions row: %w", err)
	}
	return nil
}

// GetByTokenHash looks a session up by its verifier hash (never the raw
// handle — callers hash an incoming cookie value via
// secrets.HashSessionHandle before calling this). ok is false if no row
// matches. This does not check expiry or revocation itself — enforcing
// idle/absolute expiry is SEC-003's job; the caller here (SEC-002's
// GET /auth/session) only needs to see RevokedAt.
func (r *OwnerSessionRepo) GetByTokenHash(ctx context.Context, tokenHash []byte) (OwnerSessionRow, bool, error) {
	var (
		createdAt, lastSeenAt, idleExpiresAt, absoluteExpiresAt string
		revokedAt, reverifyFreshUntil                           sql.NullString
	)

	err := r.db.Conn().QueryRowContext(ctx,
		`SELECT created_at, last_seen_at, idle_expires_at, absolute_expires_at, revoked_at, reverify_fresh_until
		 FROM owner_sessions WHERE token_hash = ?`,
		tokenHash,
	).Scan(&createdAt, &lastSeenAt, &idleExpiresAt, &absoluteExpiresAt, &revokedAt, &reverifyFreshUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return OwnerSessionRow{}, false, nil
	}
	if err != nil {
		return OwnerSessionRow{}, false, fmt.Errorf("storage: get owner_sessions by token_hash: %w", err)
	}

	row := OwnerSessionRow{TokenHash: tokenHash}
	if row.CreatedAt, err = parseTimestamp(createdAt); err != nil {
		return OwnerSessionRow{}, false, err
	}
	if row.LastSeenAt, err = parseTimestamp(lastSeenAt); err != nil {
		return OwnerSessionRow{}, false, err
	}
	if row.IdleExpiresAt, err = parseTimestamp(idleExpiresAt); err != nil {
		return OwnerSessionRow{}, false, err
	}
	if row.AbsoluteExpiresAt, err = parseTimestamp(absoluteExpiresAt); err != nil {
		return OwnerSessionRow{}, false, err
	}
	if revokedAt.Valid {
		t, parseErr := parseTimestamp(revokedAt.String)
		if parseErr != nil {
			return OwnerSessionRow{}, false, parseErr
		}
		row.RevokedAt = &t
	}
	if reverifyFreshUntil.Valid {
		t, parseErr := parseTimestamp(reverifyFreshUntil.String)
		if parseErr != nil {
			return OwnerSessionRow{}, false, parseErr
		}
		row.ReverifyFreshUntil = &t
	}

	return row, true, nil
}

// Renew advances a still-active session's last_seen_at/idle_expires_at
// (09 §5.3's renewal: "each authenticated request within the idle
// window advances last_seen_at and idle_expires_at = now + idle_ttl,
// but never past absolute_expires_at"). The caller (httpapi's session
// validator) computes idleExpires already clamped to the session's
// absolute_expires_at — this method persists whatever it is given
// verbatim, so the clamping invariant lives in exactly one place. The
// WHERE clause only ever matches a still-active row, mirroring Revoke's
// idempotency: renewing an already-revoked or absent session affects
// zero rows and is not an error.
func (r *OwnerSessionRepo) Renew(ctx context.Context, tokenHash []byte, lastSeen, idleExpires time.Time) error {
	_, err := r.db.Conn().ExecContext(ctx,
		`UPDATE owner_sessions SET last_seen_at = ?, idle_expires_at = ? WHERE token_hash = ? AND revoked_at IS NULL`,
		formatTimestamp(lastSeen), formatTimestamp(idleExpires), tokenHash,
	)
	if err != nil {
		return fmt.Errorf("storage: renew owner_sessions row: %w", err)
	}
	return nil
}

// RevokeAll revokes every still-active session at once (09 §5.3:
// "changing the owner password revokes all existing sessions") —
// the password-change endpoint itself is a later unit; this is the
// storage primitive it will call.
func (r *OwnerSessionRepo) RevokeAll(ctx context.Context, at time.Time) error {
	_, err := r.db.Conn().ExecContext(ctx,
		`UPDATE owner_sessions SET revoked_at = ? WHERE revoked_at IS NULL`,
		formatTimestamp(at),
	)
	if err != nil {
		return fmt.Errorf("storage: revoke all owner_sessions rows: %w", err)
	}
	return nil
}

// StampReverify sets reverify_fresh_until for the still-active session
// identified by tokenHash (09 §5.5: "on success stamp the session's
// reverify_fresh_until = now + 5 minutes"). The caller computes until
// (exactly now+5m) — this method persists it verbatim, mirroring
// Renew's division of responsibility. The WHERE clause only ever
// matches a still-active row: stamping a revoked or absent session is a
// no-op, not an error.
func (r *OwnerSessionRepo) StampReverify(ctx context.Context, tokenHash []byte, until time.Time) error {
	_, err := r.db.Conn().ExecContext(ctx,
		`UPDATE owner_sessions SET reverify_fresh_until = ? WHERE token_hash = ? AND revoked_at IS NULL`,
		formatTimestamp(until), tokenHash,
	)
	if err != nil {
		return fmt.Errorf("storage: stamp reverify_fresh_until: %w", err)
	}
	return nil
}

// Revoke sets revoked_at = now for the session identified by tokenHash.
// It is idempotent by construction (the WHERE clause only ever matches
// and updates a still-active row): revoking an already-revoked row, or a
// tokenHash matching no row at all, affects zero rows and is not an
// error — logout does not need to check existence first, and a
// revoked/absent session is never resurrected by a repeated call.
func (r *OwnerSessionRepo) Revoke(ctx context.Context, tokenHash []byte) error {
	_, err := r.db.Conn().ExecContext(ctx,
		`UPDATE owner_sessions SET revoked_at = ? WHERE token_hash = ? AND revoked_at IS NULL`,
		formatTimestamp(time.Now()), tokenHash,
	)
	if err != nil {
		return fmt.Errorf("storage: revoke owner_sessions row: %w", err)
	}
	return nil
}
