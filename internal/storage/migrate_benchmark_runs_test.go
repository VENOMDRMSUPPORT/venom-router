package storage

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// benchmarkRunsVersion is the goose version of the benchmark_runs migration
// (00017_benchmark_runs.sql).
const benchmarkRunsVersion = 17

// TestMigration00017_UpDown proves 00017 adds benchmark_runs plus its
// idx_benchmark_runs_model index, rolls back to exactly the pre-00017 shape
// (every earlier table survives), and re-applies. Count-agnostic: it rolls
// back every migration at or above benchmarkRunsVersion, so a later
// migration lands without silently breaking this test (mirrors every other
// migrate_*_test in this package).
func TestMigration00017_UpDown(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	if _, err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() (up) error = %v", err)
	}
	assertTableExists(t, db, "benchmark_runs", true)
	assertIndexExists(t, db, "idx_benchmark_runs_model", true)

	for currentVersion(t, db) >= benchmarkRunsVersion {
		if _, err := DownOne(ctx, db); err != nil {
			t.Fatalf("DownOne() error = %v", err)
		}
	}
	assertTableExists(t, db, "benchmark_runs", false)
	assertIndexExists(t, db, "idx_benchmark_runs_model", false)
	// Every earlier table must survive rolling back only 00017.
	assertTableExists(t, db, "models", true)
	assertTableExists(t, db, "accounts", true)
	assertTableExists(t, db, "providers", true)
	assertBaselineTableExists(t, db, true)

	if _, err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() (re-up) error = %v", err)
	}
	assertTableExists(t, db, "benchmark_runs", true)
	assertIndexExists(t, db, "idx_benchmark_runs_model", true)
}

func assertIndexExists(t *testing.T, db *DB, name string, want bool) {
	t.Helper()

	var got string
	err := db.Conn().QueryRow(
		"SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?", name,
	).Scan(&got)

	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("query sqlite_master for index %s: %v", name, err)
	}
	if exists != want {
		t.Fatalf("index %s exists = %v, want %v", name, exists, want)
	}
}
