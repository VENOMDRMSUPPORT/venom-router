package storage

import (
	"context"
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

// OwnerSessionRow is one row to insert into owner_sessions (M1,
// 00002_owner_auth.sql). TokenHash is the ONLY thing ever persisted for
// the session identifier — the raw opaque handle is minted by
// internal/secrets, handed to the caller as the cookie value, and never
// stored.
type OwnerSessionRow struct {
	TokenHash         []byte
	CreatedAt         time.Time
	LastSeenAt        time.Time
	IdleExpiresAt     time.Time
	AbsoluteExpiresAt time.Time
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
// reverify_fresh_until are left NULL (this unit only creates the first
// session; expiry enforcement and re-verification are later units).
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
