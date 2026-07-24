package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

func TestMigrate_UpDownUp(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	total := embeddedMigrationCount(t)

	results, err := Migrate(ctx, db)
	if err != nil {
		t.Fatalf("Migrate() (up) error = %v", err)
	}
	if len(results) != total {
		t.Fatalf("Migrate() (up) applied %d migrations, want %d", len(results), total)
	}
	if got := currentVersion(t, db); got != int64(total) {
		t.Fatalf("version after up = %d, want %d", got, total)
	}
	assertBaselineTableExists(t, db, true)

	downResult, err := DownOne(ctx, db)
	if err != nil {
		t.Fatalf("DownOne() error = %v", err)
	}
	if downResult.Source.Version != int64(total) {
		t.Fatalf("DownOne() rolled back version %d, want %d", downResult.Source.Version, total)
	}
	if got := currentVersion(t, db); got != int64(total-1) {
		t.Fatalf("version after down = %d, want %d", got, total-1)
	}

	results, err = Migrate(ctx, db)
	if err != nil {
		t.Fatalf("Migrate() (re-up) error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Migrate() (re-up) applied %d migrations, want 1", len(results))
	}
	if got := currentVersion(t, db); got != int64(total) {
		t.Fatalf("version after re-up = %d, want %d", got, total)
	}
	assertBaselineTableExists(t, db, true)
}

// embeddedMigrationCount derives the expected number of embedded migrations
// from the embedded filesystem itself, rather than a hard-coded literal, so
// this test keeps working as M2/M3+ migrations land.
func embeddedMigrationCount(t *testing.T) int {
	t.Helper()

	fsys, err := migrationsRootFS()
	if err != nil {
		t.Fatalf("migrationsRootFS: %v", err)
	}
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}

	count := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			count++
		}
	}
	return count
}

// TestMigrate_ChecksumTamperRejected forces the actual failure condition:
// it applies the baseline, then directly corrupts the checksum recorded
// in this runner's own bookkeeping table (venom_migration_checksums) for
// the already-applied version — simulating the embedded migration's
// content having drifted from what was recorded when it was applied.
// Both Verify and a subsequent Migrate must fail closed rather than
// silently proceeding.
func TestMigrate_ChecksumTamperRejected(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	if _, err := Migrate(ctx, db); err != nil {
		t.Fatalf("initial Migrate() error = %v", err)
	}

	if _, err := db.Conn().Exec(
		"UPDATE " + checksumTableName + " SET checksum = 'deadbeef' WHERE version = 1",
	); err != nil {
		t.Fatalf("tamper recorded checksum: %v", err)
	}

	if err := Verify(ctx, db); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("Verify() after tamper, error = %v, want ErrChecksumMismatch", err)
	}

	if _, err := Migrate(ctx, db); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("Migrate() after tamper, error = %v, want ErrChecksumMismatch", err)
	}
}

func TestVerify_IntegrityCheckPassesOnHealthyDB(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	if _, err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	if err := Verify(ctx, db); err != nil {
		t.Fatalf("Verify() on a healthy, migrated DB, error = %v", err)
	}
}

// TestVerify_IntegrityCheckFailsClosedOnCorruption forces real, on-disk
// SQLite corruption: after migrating, it checkpoints WAL into the main
// file (so the corruption lands on live data, not the not-yet-folded-in
// WAL), closes the DB, then overwrites every byte in page 2 onward
// (leaving page 1 — the schema/master-table page — untouched) with
// garbage.
//
// Two cruder approaches were tried first via a throwaway diagnostic
// script and rejected:
//   - Corrupting a narrow byte range (e.g. 100 bytes at offset 150) left
//     PRAGMA integrity_check reporting "ok" every time — on this small,
//     near-empty database, ranges up to several hundred bytes past the
//     header repeatedly landed in each page's unused free space, which
//     integrity_check's b-tree walk never needs to visit.
//   - Corrupting everything past the 100-byte file header, including
//     page 1 itself, breaks schema reading badly enough that
//     storage.Open's own connection-time pragma application fails during
//     Ping — the corruption is real, but it's caught by Open() with a
//     generic wrapped error, never reaching this package's
//     PRAGMA-integrity_check-based Verify path at all.
//
// Corrupting only page 2 onward keeps the schema page intact (Open/Ping
// succeed) while destroying the actual table b-tree pages, which only a
// full integrity_check walk detects — confirmed empirically before
// writing this test.
func TestVerify_IntegrityCheckFailsClosedOnCorruption(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	ctx := context.Background()
	if _, err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	if _, err := db.Conn().Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatalf("checkpoint before corrupting: %v", err)
	}

	path := db.Path()
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	corruptDataPages(t, path)

	db2, err := Open(dir)
	if err != nil {
		t.Fatalf("re-Open() on corrupted file, error = %v (want this to still succeed — only integrity_check should catch the corruption)", err)
	}
	defer func() { _ = db2.Close() }()

	err = Verify(ctx, db2)
	if !errors.Is(err, ErrIntegrityCheckFailed) {
		t.Fatalf("Verify() on corrupted DB, error = %v, want ErrIntegrityCheckFailed", err)
	}
	t.Logf("integrity_check correctly failed closed: %v", err)
}

// sqlitePageSize is SQLite's default page size (bytes per page) since
// v3.12, which this project relies on implicitly by never overriding
// PRAGMA page_size.
const sqlitePageSize = 4096

func corruptDataPages(t *testing.T, path string) {
	t.Helper()

	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open db file to corrupt: %v", err)
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		t.Fatalf("stat db file: %v", err)
	}
	if info.Size() <= sqlitePageSize {
		t.Fatalf("db file has no page 2 to corrupt: %d bytes", info.Size())
	}

	garbage := bytes.Repeat([]byte{0xFF}, int(info.Size()-sqlitePageSize))
	if _, err := f.WriteAt(garbage, sqlitePageSize); err != nil {
		t.Fatalf("write corruption: %v", err)
	}
}

func TestMigrationBytes_LFOnlyAndChecksumDeterministic(t *testing.T) {
	fsys, err := migrationsRootFS()
	if err != nil {
		t.Fatalf("migrationsRootFS: %v", err)
	}
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}

	checked := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		checked++

		data, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			t.Fatalf("read embedded migration %s: %v", e.Name(), err)
		}
		if bytes.ContainsRune(data, '\r') {
			t.Fatalf("embedded migration %s contains CR bytes; checksums would not be stable across OSes", e.Name())
		}

		sum1 := sha256.Sum256(data)
		sum2 := sha256.Sum256(data)
		if sum1 != sum2 {
			t.Fatalf("checksum for %s is not deterministic across repeated computations of the same bytes", e.Name())
		}

		source := &goose.Source{Path: e.Name(), Version: 1, Type: goose.TypeSQL}
		got, err := checksumSource(source)
		if err != nil {
			t.Fatalf("checksumSource(%s): %v", e.Name(), err)
		}
		want := hex.EncodeToString(sum1[:])
		if got != want {
			t.Fatalf("checksumSource(%s) = %q, want %q (must be a pure function of the embedded bytes)", e.Name(), got, want)
		}
	}
	if checked == 0 {
		t.Fatalf("no embedded .sql migrations found to check")
	}
}

func currentVersion(t *testing.T, db *DB) int64 {
	t.Helper()
	p, err := newProvider(db)
	if err != nil {
		t.Fatalf("newProvider: %v", err)
	}
	// Do not call p.Close(): goose.Provider.Close closes the underlying
	// *sql.DB, which is db's shared handle, not this helper's to close.

	v, err := p.GetDBVersion(context.Background())
	if err != nil {
		t.Fatalf("GetDBVersion: %v", err)
	}
	return v
}

func assertBaselineTableExists(t *testing.T, db *DB, want bool) {
	t.Helper()

	var name string
	err := db.Conn().QueryRow(
		"SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'venom_schema_baseline'",
	).Scan(&name)

	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("query sqlite_master: %v", err)
	}
	if exists != want {
		t.Fatalf("venom_schema_baseline exists = %v, want %v", exists, want)
	}
}
