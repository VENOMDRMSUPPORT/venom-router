package storage

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// enrichmentSettingVersion is the goose version of the enrichment-setting
// migration (00007_enrichment_setting.sql).
const enrichmentSettingVersion = 7

// TestEnrichmentSetting_UpAddsColumnDefaultOff proves 00007 adds
// owner_settings.enrichment_enabled with a schema-level default of 0 (off):
// a freshly-inserted row that never mentions the column still reads back
// enrichment_enabled = 0 — the off-by-default rule lives in the schema, not
// only in a Go-layer default.
func TestEnrichmentSetting_UpAddsColumnDefaultOff(t *testing.T) {
	db := migratedEnrichmentSettingDB(t)

	if _, err := db.Conn().Exec(
		`INSERT INTO owner_settings (id, theme, density, updated_at) VALUES (1, 'venom-dark', 'comfortable', 0)`,
	); err != nil {
		t.Fatalf("insert owner_settings row without enrichment_enabled: %v", err)
	}

	var enrichmentEnabled int
	if err := db.Conn().QueryRow(`SELECT enrichment_enabled FROM owner_settings WHERE id = 1`).Scan(&enrichmentEnabled); err != nil {
		t.Fatalf("query enrichment_enabled: %v", err)
	}
	if enrichmentEnabled != 0 {
		t.Fatalf("enrichment_enabled default = %d, want 0 (off by default)", enrichmentEnabled)
	}
}

// TestEnrichmentSetting_DownRemovesColumn proves 00007's down path drops
// enrichment_enabled while leaving owner_settings (and every lower table)
// intact. The rollback loop is count-agnostic: it rolls back every
// migration at or above enrichmentSettingVersion, so a later migration lands
// without silently breaking this test (mirrors the M4/M5 up/down tests'
// robustness shape).
func TestEnrichmentSetting_DownRemovesColumn(t *testing.T) {
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
	assertColumnExists(t, db, "owner_settings", "enrichment_enabled", true)

	for currentVersion(t, db) >= enrichmentSettingVersion {
		if _, err := DownOne(ctx, db); err != nil {
			t.Fatalf("DownOne() error = %v", err)
		}
	}
	assertTableExists(t, db, "owner_settings", true)
	assertColumnExists(t, db, "owner_settings", "enrichment_enabled", false)
	// Every lower table must survive rolling back only 00007.
	assertTableExists(t, db, "models", true)
	assertTableExists(t, db, "discovery_runs", true)
	assertTableExists(t, db, "audit_events", true)
	assertTableExists(t, db, "jobs", true)
	assertTableExists(t, db, "providers", true)
	assertTableExists(t, db, "accounts", true)
	assertBaselineTableExists(t, db, true)

	if _, err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() (re-up) error = %v", err)
	}
	assertColumnExists(t, db, "owner_settings", "enrichment_enabled", true)
}

func migratedEnrichmentSettingDB(t *testing.T) *DB {
	t.Helper()

	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := Migrate(context.Background(), db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return db
}

// assertColumnExists reports (via t.Fatalf on mismatch) whether table has a
// column named column, using SQLite's pragma_table_info introspection —
// no third-party dependency needed for this shape-only check.
func assertColumnExists(t *testing.T, db *DB, table, column string, want bool) {
	t.Helper()

	var name string
	err := db.Conn().QueryRow(
		`SELECT name FROM pragma_table_info(?) WHERE name = ?`, table, column,
	).Scan(&name)

	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("query pragma_table_info(%s) for column %s: %v", table, column, err)
	}
	if exists != want {
		t.Fatalf("%s.%s exists = %v, want %v", table, column, exists, want)
	}
}
