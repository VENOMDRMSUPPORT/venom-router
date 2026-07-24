package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// OwnerAuthRow is the single-row owner_auth persistence shape (M1,
// 00002_owner_auth.sql): the Argon2id hash + salt + KDF params.
// internal/secrets computes these values; this package only stores and
// retrieves the bytes, never derives or verifies them itself.
type OwnerAuthRow struct {
	PasswordHash []byte
	Salt         []byte
	KDFTime      uint32
	KDFMemKiB    uint32
	KDFThreads   uint8
	KDFKeyLen    uint32
}

// ErrOwnerAuthAlreadySet is returned by Create when the single owner_auth
// row already exists — the precondition for first-run setup (09 §5.1's
// setup_already_complete).
var ErrOwnerAuthAlreadySet = errors.New("storage: owner_auth row already exists")

// OwnerAuthRepo persists the single owner_auth row over the M1 schema.
type OwnerAuthRepo struct {
	db *DB
}

// NewOwnerAuthRepo builds a repository over db's existing connection —
// it opens no connection of its own.
func NewOwnerAuthRepo(db *DB) *OwnerAuthRepo {
	return &OwnerAuthRepo{db: db}
}

// Exists reports whether the single owner_auth row has been created yet
// (09 §5.1's setup precondition / GET /auth/status).
func (r *OwnerAuthRepo) Exists(ctx context.Context) (bool, error) {
	var id int
	err := r.db.Conn().QueryRowContext(ctx, "SELECT id FROM owner_auth WHERE id = 1").Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("storage: check owner_auth existence: %w", err)
	}
	return true, nil
}

// Create writes the single owner_auth row. It fails with
// ErrOwnerAuthAlreadySet if a row already exists. The M1 schema's
// CHECK(id=1) plus this INSERT (never an upsert) is the structural
// backstop; the pre-check gives callers a typed error instead of a raw
// driver "UNIQUE constraint failed", and the post-insert-failure re-check
// maps a concurrent racing Create to the same typed error rather than an
// opaque one.
func (r *OwnerAuthRepo) Create(ctx context.Context, row OwnerAuthRow) error {
	exists, err := r.Exists(ctx)
	if err != nil {
		return err
	}
	if exists {
		return ErrOwnerAuthAlreadySet
	}

	_, insertErr := r.db.Conn().ExecContext(ctx,
		`INSERT INTO owner_auth (id, password_hash, salt, kdf_time, kdf_mem_kib, kdf_threads, kdf_key_len)
		 VALUES (1, ?, ?, ?, ?, ?, ?)`,
		row.PasswordHash, row.Salt, row.KDFTime, row.KDFMemKiB, row.KDFThreads, row.KDFKeyLen,
	)
	if insertErr != nil {
		if stillExists, checkErr := r.Exists(ctx); checkErr == nil && stillExists {
			return ErrOwnerAuthAlreadySet
		}
		return fmt.Errorf("storage: create owner_auth row: %w", insertErr)
	}
	return nil
}

// Clear deletes the single owner_auth row if one exists (09 §5.7's
// local-owner reset: "clears the owner_auth row"). It is idempotent —
// deleting when no row exists is a no-op, not an error — so a caller
// never needs to check existence first.
func (r *OwnerAuthRepo) Clear(ctx context.Context) error {
	if _, err := r.db.Conn().ExecContext(ctx, `DELETE FROM owner_auth WHERE id = 1`); err != nil {
		return fmt.Errorf("storage: clear owner_auth row: %w", err)
	}
	return nil
}

// Get reads back the single owner_auth row. ok is false if no row exists
// yet.
func (r *OwnerAuthRepo) Get(ctx context.Context) (row OwnerAuthRow, ok bool, err error) {
	var kdfThreads int
	dbErr := r.db.Conn().QueryRowContext(ctx,
		`SELECT password_hash, salt, kdf_time, kdf_mem_kib, kdf_threads, kdf_key_len
		 FROM owner_auth WHERE id = 1`,
	).Scan(&row.PasswordHash, &row.Salt, &row.KDFTime, &row.KDFMemKiB, &kdfThreads, &row.KDFKeyLen)
	if errors.Is(dbErr, sql.ErrNoRows) {
		return OwnerAuthRow{}, false, nil
	}
	if dbErr != nil {
		return OwnerAuthRow{}, false, fmt.Errorf("storage: read owner_auth row: %w", dbErr)
	}
	row.KDFThreads = uint8(kdfThreads)
	return row, true, nil
}
