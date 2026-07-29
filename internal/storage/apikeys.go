package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// APIKeyRepo is the typed repository over the M7 venom_api_keys table
// (00012_api_keys_usage.sql). It is the single SQL surface for Venom API
// keys: creation (hash + non-secret prefix only), verification lookup,
// listing, revocation, and the last-used touch.
//
// SECRET HYGIENE: this repository accepts and returns the verifier HASH
// only. A raw vk_live_* key never enters any method here — the httpapi
// layer hashes the raw value with hex(sha256(...)) before it ever reaches
// storage, and there is no column that could hold the raw value even if a
// caller mistakenly passed one.
type APIKeyRepo struct {
	db *DB
}

// NewAPIKeyRepo builds a repository over db's existing connection.
func NewAPIKeyRepo(db *DB) *APIKeyRepo {
	return &APIKeyRepo{db: db}
}

// APIKey is one venom_api_keys row. KeyHash is the 64-char hex verifier;
// there is no raw-key field by construction. RPMLimit/LastUsedAt/RevokedAt
// are nil when the corresponding column is NULL (unknown / not-yet /
// not-revoked) — never a sentinel zero.
type APIKey struct {
	ID         string
	Label      string
	KeyHash    string
	KeyPrefix  string
	RPMLimit   *int
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

// Revoked reports whether this key has been revoked as of its stored
// revoked_at (any non-NULL revoked_at means revoked — revocation is
// permanent, never un-set).
func (k APIKey) Revoked() bool { return k.RevokedAt != nil }

// CreateAPIKeyParams is one Create request. ID is minted by the caller
// (mirroring this package's other id-minting conventions). KeyHash must be
// the 64-char hex verifier; KeyPrefix is the non-secret display fragment.
type CreateAPIKeyParams struct {
	ID        string
	Label     string
	KeyHash   string
	KeyPrefix string
	RPMLimit  *int
	CreatedAt time.Time
}

// ErrInvalidAPIKeyParams is returned by Create for a structurally invalid
// request (missing id/label/hash/prefix). The DB CHECK constraints remain
// the authoritative guard for label-non-empty, hash-length, rpm>0, and
// hash-uniqueness — this pre-check only catches obviously empty inputs
// before a round-trip.
var ErrInvalidAPIKeyParams = errors.New("storage: invalid api key params")

// Create inserts one venom_api_keys row. A duplicate key_hash violates the
// UNIQUE constraint and surfaces as an error (never a silent overwrite); a
// rpm_limit <= 0, empty label, or non-64-char hash is rejected by the
// table's CHECK constraints.
func (r *APIKeyRepo) Create(ctx context.Context, p CreateAPIKeyParams) error {
	if p.ID == "" || p.Label == "" || p.KeyHash == "" || p.KeyPrefix == "" {
		return fmt.Errorf("%w: id, label, key_hash, and key_prefix are all required", ErrInvalidAPIKeyParams)
	}
	var rpm sql.NullInt64
	if p.RPMLimit != nil {
		rpm = sql.NullInt64{Int64: int64(*p.RPMLimit), Valid: true}
	}
	if _, err := r.db.Conn().ExecContext(ctx,
		`INSERT INTO venom_api_keys (id, label, key_hash, key_prefix, rpm_limit, created_at, last_used_at, revoked_at)
		 VALUES (?, ?, ?, ?, ?, ?, NULL, NULL)`,
		p.ID, p.Label, p.KeyHash, p.KeyPrefix, rpm, p.CreatedAt.Unix(),
	); err != nil {
		return fmt.Errorf("storage: create api key %q: %w", p.ID, err)
	}
	return nil
}

// LookupByHash returns the key whose key_hash EQUALS hash (an equality
// lookup on the UNIQUE-indexed column — never a scan, never a prefix/LIKE
// match, so a partial or unrelated value can never authenticate). ok is
// false when no row has exactly that hash.
func (r *APIKeyRepo) LookupByHash(ctx context.Context, hash string) (APIKey, bool, error) {
	row := r.db.Conn().QueryRowContext(ctx,
		`SELECT id, label, key_hash, key_prefix, rpm_limit, created_at, last_used_at, revoked_at
		 FROM venom_api_keys WHERE key_hash = ?`,
		hash,
	)
	key, err := scanAPIKey(row)
	if errors.Is(err, sql.ErrNoRows) {
		return APIKey{}, false, nil
	}
	if err != nil {
		return APIKey{}, false, fmt.Errorf("storage: lookup api key by hash: %w", err)
	}
	return key, true, nil
}

// List returns every key in a deterministic order (created_at ASC, then id
// ASC as a stable tie-break), never returning the raw key (there is none)
// and carrying the full hash for the caller to project away.
func (r *APIKeyRepo) List(ctx context.Context) ([]APIKey, error) {
	rows, err := r.db.Conn().QueryContext(ctx,
		`SELECT id, label, key_hash, key_prefix, rpm_limit, created_at, last_used_at, revoked_at
		 FROM venom_api_keys ORDER BY created_at ASC, id ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: list api keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []APIKey
	for rows.Next() {
		key, err := scanAPIKey(rows)
		if err != nil {
			return nil, fmt.Errorf("storage: list api keys: scan: %w", err)
		}
		out = append(out, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: list api keys: %w", err)
	}
	return out, nil
}

// Revoke stamps revoked_at = now for id, idempotently: the conditional
// UPDATE only matches a row whose revoked_at IS NULL, so revoking an
// already-revoked key affects zero rows (a no-op, never an error) and the
// first revocation instant is never overwritten.
func (r *APIKeyRepo) Revoke(ctx context.Context, id string, now time.Time) error {
	if _, err := r.db.Conn().ExecContext(ctx,
		`UPDATE venom_api_keys SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`,
		now.Unix(), id,
	); err != nil {
		return fmt.Errorf("storage: revoke api key %q: %w", id, err)
	}
	return nil
}

// TouchLastUsed sets last_used_at = now for id. It is a fire-and-forget
// bookkeeping update: it never gates authentication and is safe to call on
// every successful request.
func (r *APIKeyRepo) TouchLastUsed(ctx context.Context, id string, now time.Time) error {
	if _, err := r.db.Conn().ExecContext(ctx,
		`UPDATE venom_api_keys SET last_used_at = ? WHERE id = ?`,
		now.Unix(), id,
	); err != nil {
		return fmt.Errorf("storage: touch api key %q: %w", id, err)
	}
	return nil
}

// scanAPIKey scans one venom_api_keys row, mapping NULL rpm_limit /
// last_used_at / revoked_at to nil pointers (unknown / not-yet /
// not-revoked) rather than a zero value.
func scanAPIKey(s rowScanner) (APIKey, error) {
	var (
		key        APIKey
		rpm        sql.NullInt64
		createdAt  int64
		lastUsedAt sql.NullInt64
		revokedAt  sql.NullInt64
	)
	if err := s.Scan(&key.ID, &key.Label, &key.KeyHash, &key.KeyPrefix, &rpm, &createdAt, &lastUsedAt, &revokedAt); err != nil {
		return APIKey{}, err
	}
	if rpm.Valid {
		v := int(rpm.Int64)
		key.RPMLimit = &v
	}
	key.CreatedAt = time.Unix(createdAt, 0).UTC()
	if lastUsedAt.Valid {
		t := time.Unix(lastUsedAt.Int64, 0).UTC()
		key.LastUsedAt = &t
	}
	if revokedAt.Valid {
		t := time.Unix(revokedAt.Int64, 0).UTC()
		key.RevokedAt = &t
	}
	return key, nil
}
