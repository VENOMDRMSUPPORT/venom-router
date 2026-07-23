package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
)

// checksumTableName is this runner's own bookkeeping table, recording the
// content checksum of every migration at the moment it was applied. This
// is separate from goose's own version-tracking table (goose only tracks
// version numbers, not content — it has no built-in checksum guard), so
// this package adds one on top.
const checksumTableName = "venom_migration_checksums"

// ErrIntegrityCheckFailed is returned by Verify/Migrate when
// PRAGMA integrity_check does not report "ok".
var ErrIntegrityCheckFailed = errors.New("storage: database integrity check failed")

// ErrChecksumMismatch is returned by Verify/Migrate when an
// already-applied migration's current embedded content does not match
// the checksum recorded at the time it was applied (or has no recorded
// checksum at all despite being applied) — the migration was tampered
// with, or the bookkeeping row was tampered with, after being applied.
var ErrChecksumMismatch = errors.New("storage: applied migration checksum mismatch")

// newProvider builds a goose Provider against db's existing *sql.DB and
// the embedded migrations. It does not open a new connection, driver, or
// pragma set — it operates entirely on the handle P0-DB-001 already
// configured via storage.Open.
func newProvider(db *DB) (*goose.Provider, error) {
	fsys, err := migrationsRootFS()
	if err != nil {
		return nil, fmt.Errorf("storage: resolve embedded migrations: %w", err)
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, db.Conn(), fsys)
	if err != nil {
		return nil, fmt.Errorf("storage: init migration provider: %w", err)
	}
	return p, nil
}

// Verify runs this unit's two fail-closed safety checks — PRAGMA
// integrity_check, then the checksum guard over every already-applied
// migration — without applying any pending migration. It is safe to call
// on its own, e.g. as a pre-flight check independent of Migrate.
func Verify(ctx context.Context, db *DB) error {
	if err := checkIntegrity(ctx, db); err != nil {
		return err
	}

	p, err := newProvider(db)
	if err != nil {
		return err
	}
	// newProvider's *goose.Provider wraps db's own *sql.DB. Provider.Close
	// closes that underlying *sql.DB — it must NOT be called here, since
	// this function does not own that handle's lifecycle (storage.DB.Close
	// does). The Provider itself needs no other cleanup.

	return verifyChecksums(ctx, db, p)
}

// Migrate runs Verify, then applies any pending migrations (goose Up),
// recording each newly-applied migration's checksum. This is the
// forward-only production posture: it never rolls back on its own — see
// DownOne for the dev/test-only rollback path.
func Migrate(ctx context.Context, db *DB) ([]*goose.MigrationResult, error) {
	if err := checkIntegrity(ctx, db); err != nil {
		return nil, err
	}

	p, err := newProvider(db)
	if err != nil {
		return nil, err
	}
	// See the comment in Verify: Provider.Close would close db's shared
	// *sql.DB, which this function does not own — do not call it.

	if err := verifyChecksums(ctx, db, p); err != nil {
		return nil, err
	}

	if err := ensureChecksumTable(ctx, db); err != nil {
		return nil, err
	}

	results, err := p.Up(ctx)
	if err != nil {
		return nil, fmt.Errorf("storage: apply migrations: %w", err)
	}

	if err := recordChecksums(ctx, db, results); err != nil {
		return nil, err
	}

	return results, nil
}

// DownOne rolls back exactly one migration (goose Down). This is the
// dev/test-only rollback path referenced by 08 §4 ("tested down path in
// dev"); production startup calls Migrate, never this.
func DownOne(ctx context.Context, db *DB) (*goose.MigrationResult, error) {
	p, err := newProvider(db)
	if err != nil {
		return nil, err
	}
	// See the comment in Verify: Provider.Close would close db's shared
	// *sql.DB, which this function does not own — do not call it.

	result, err := p.Down(ctx)
	if err != nil {
		return nil, fmt.Errorf("storage: roll back migration: %w", err)
	}

	if err := ensureChecksumTable(ctx, db); err != nil {
		return nil, err
	}
	if err := deleteChecksum(ctx, db, result.Source.Version); err != nil {
		return nil, err
	}

	return result, nil
}

func checkIntegrity(ctx context.Context, db *DB) error {
	var result string
	if err := db.Conn().QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return fmt.Errorf("storage: run integrity check: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("%w: %s", ErrIntegrityCheckFailed, result)
	}
	return nil
}

// verifyChecksums compares every already-applied migration's current
// embedded content against the checksum recorded when it was applied. If
// the bookkeeping table does not exist yet, nothing has ever been
// applied through this runner, so there is nothing to verify.
func verifyChecksums(ctx context.Context, db *DB, p *goose.Provider) error {
	exists, err := checksumTableExists(ctx, db)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}

	statuses, err := p.Status(ctx)
	if err != nil {
		return fmt.Errorf("storage: read migration status: %w", err)
	}

	for _, s := range statuses {
		if s.State != goose.StateApplied {
			continue
		}

		recorded, ok, err := lookupChecksum(ctx, db, s.Source.Version)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: version %d is applied but has no recorded checksum",
				ErrChecksumMismatch, s.Source.Version)
		}

		current, err := checksumSource(s.Source)
		if err != nil {
			return err
		}
		if current != recorded {
			return fmt.Errorf("%w: version %d (%s): recorded %s, current %s",
				ErrChecksumMismatch, s.Source.Version, s.Source.Path, recorded, current)
		}
	}
	return nil
}

func checksumSource(s *goose.Source) (string, error) {
	fsys, err := migrationsRootFS()
	if err != nil {
		return "", fmt.Errorf("storage: resolve embedded migrations: %w", err)
	}
	data, err := fs.ReadFile(fsys, s.Path)
	if err != nil {
		return "", fmt.Errorf("storage: read migration %s: %w", s.Path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func checksumTableExists(ctx context.Context, db *DB) (bool, error) {
	var name string
	err := db.Conn().QueryRowContext(ctx,
		"SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?",
		checksumTableName,
	).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("storage: check checksum table: %w", err)
	}
	return true, nil
}

func ensureChecksumTable(ctx context.Context, db *DB) error {
	_, err := db.Conn().ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS `+checksumTableName+` (
			version  INTEGER PRIMARY KEY,
			checksum TEXT NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("storage: create checksum table: %w", err)
	}
	return nil
}

func lookupChecksum(ctx context.Context, db *DB, version int64) (checksum string, ok bool, err error) {
	err = db.Conn().QueryRowContext(ctx,
		"SELECT checksum FROM "+checksumTableName+" WHERE version = ?",
		version,
	).Scan(&checksum)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("storage: lookup checksum for version %d: %w", version, err)
	}
	return checksum, true, nil
}

func recordChecksums(ctx context.Context, db *DB, results []*goose.MigrationResult) error {
	for _, r := range results {
		sum, err := checksumSource(r.Source)
		if err != nil {
			return err
		}
		if _, err := db.Conn().ExecContext(ctx,
			"INSERT INTO "+checksumTableName+" (version, checksum) VALUES (?, ?) "+
				"ON CONFLICT(version) DO UPDATE SET checksum = excluded.checksum",
			r.Source.Version, sum,
		); err != nil {
			return fmt.Errorf("storage: record checksum for version %d: %w", r.Source.Version, err)
		}
	}
	return nil
}

func deleteChecksum(ctx context.Context, db *DB, version int64) error {
	if _, err := db.Conn().ExecContext(ctx,
		"DELETE FROM "+checksumTableName+" WHERE version = ?",
		version,
	); err != nil {
		return fmt.Errorf("storage: delete checksum for version %d: %w", version, err)
	}
	return nil
}
