package storage

import (
	"context"
	"testing"
)

// ownerSettingsVersion is the goose version of the owner-settings migration
// (00005_owner_settings.sql).
const ownerSettingsVersion = 5

// TestMigrateOwnerSettings_UpDownUp proves M5 (owner_settings) applies,
// rolls back to exactly the M4 state (every lower table survives), and
// re-applies. The rollback loop is count-agnostic: it rolls back every
// migration at or above ownerSettingsVersion, so a later M6 lands without
// silently breaking this test (the M2/M3/M4 up/down tests use the same
// robustness shape).
func TestMigrateOwnerSettings_UpDownUp(t *testing.T) {
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
	assertTableExists(t, db, "owner_settings", true)

	// Roll back every migration applied at or above M5, then M5 itself, so
	// this proves M5's own down path no matter how many later migrations
	// land (ownerSettingsVersion is M5's goose version).
	for currentVersion(t, db) >= ownerSettingsVersion {
		if _, err := DownOne(ctx, db); err != nil {
			t.Fatalf("DownOne() error = %v", err)
		}
	}
	assertTableExists(t, db, "owner_settings", false)
	// Every lower table must survive rolling back only M5.
	assertTableExists(t, db, "audit_events", true)
	assertTableExists(t, db, "jobs", true)
	assertTableExists(t, db, "providers", true)
	assertTableExists(t, db, "accounts", true)
	assertTableExists(t, db, "account_credentials", true)
	assertTableExists(t, db, "account_funding_evidence", true)
	assertTableExists(t, db, "oauth_transactions", true)
	assertTableExists(t, db, "owner_auth", true)
	assertTableExists(t, db, "owner_sessions", true)
	assertTableExists(t, db, "auth_events", true)
	assertBaselineTableExists(t, db, true)

	if _, err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() (re-up) error = %v", err)
	}
	assertTableExists(t, db, "owner_settings", true)
}

// TestOwnerSettings_ThemeCheck proves the theme column's CHECK constraint
// is a mutation-provable DB-level invariant: every frozen design-system
// theme value is accepted, and every other value is rejected. A reviewer
// re-proves this by dropping the CHECK and confirming this test goes RED.
func TestOwnerSettings_ThemeCheck(t *testing.T) {
	db := migratedOwnerSettingsDB(t)

	for _, theme := range []string{"venom-dark", "venom-light", "venom-hc"} {
		if err := insertOwnerSettingsRow(db, theme, "comfortable"); err != nil {
			t.Fatalf("insert owner_settings with allowed theme %q: %v, want success", theme, err)
		}
		// Clear the row so the next iteration starts fresh.
		if _, err := db.Conn().Exec(`DELETE FROM owner_settings WHERE id = 1`); err != nil {
			t.Fatalf("clear owner_settings between theme trials: %v", err)
		}
	}

	if err := insertOwnerSettingsRow(db, "light", "comfortable"); err == nil {
		t.Fatalf("insert owner_settings with disallowed theme %q succeeded, want CHECK rejection", "light")
	}
	if err := insertOwnerSettingsRow(db, "dark", "comfortable"); err == nil {
		t.Fatalf("insert owner_settings with disallowed theme %q succeeded, want CHECK rejection", "dark")
	}
}

// TestOwnerSettings_DensityCheck proves the density column's CHECK
// constraint is a mutation-provable DB-level invariant: every frozen
// design-system density value is accepted, and every other value is
// rejected. A reviewer re-proves this by dropping the CHECK and confirming
// this test goes RED.
func TestOwnerSettings_DensityCheck(t *testing.T) {
	db := migratedOwnerSettingsDB(t)

	for _, density := range []string{"comfortable", "compact"} {
		if err := insertOwnerSettingsRow(db, "venom-dark", density); err != nil {
			t.Fatalf("insert owner_settings with allowed density %q: %v, want success", density, err)
		}
		if _, err := db.Conn().Exec(`DELETE FROM owner_settings WHERE id = 1`); err != nil {
			t.Fatalf("clear owner_settings between density trials: %v", err)
		}
	}

	if err := insertOwnerSettingsRow(db, "venom-dark", "cozy"); err == nil {
		t.Fatalf("insert owner_settings with disallowed density %q succeeded, want CHECK rejection", "cozy")
	}
	if err := insertOwnerSettingsRow(db, "venom-dark", "spacious"); err == nil {
		t.Fatalf("insert owner_settings with disallowed density %q succeeded, want CHECK rejection", "spacious")
	}
}

// TestOwnerSettings_SingleRow proves the id = 1 CHECK constraint is a
// mutation-provable DB-level invariant: the table holds exactly one row,
// and inserting a second row with id = 2 is rejected. A reviewer re-proves
// this by dropping the CHECK and confirming this test goes RED. Mirrors
// owner_auth's own single-row CHECK (id = 1) invariant.
func TestOwnerSettings_SingleRow(t *testing.T) {
	db := migratedOwnerSettingsDB(t)

	if err := insertOwnerSettingsRow(db, "venom-dark", "comfortable"); err != nil {
		t.Fatalf("seed first owner_settings row: %v", err)
	}
	// A second row with id = 2 must be rejected by CHECK(id = 1).
	if err := insertOwnerSettingsRowWithID(db, 2, "venom-dark", "comfortable"); err == nil {
		t.Fatalf("insert owner_settings with id = 2 succeeded, want CHECK(id = 1) rejection")
	}

	// Exactly one row exists.
	var n int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM owner_settings`).Scan(&n); err != nil {
		t.Fatalf("count owner_settings: %v", err)
	}
	if n != 1 {
		t.Fatalf("owner_settings row count = %d, want 1", n)
	}
}

// migratedOwnerSettingsDB opens and fully migrates a fresh temp-dir DB,
// returning it (with a t.Cleanup Close). Mirrors migratedAuditJobsDB.
func migratedOwnerSettingsDB(t *testing.T) *DB {
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

func insertOwnerSettingsRow(db *DB, theme, density string) error {
	return insertOwnerSettingsRowWithID(db, 1, theme, density)
}

func insertOwnerSettingsRowWithID(db *DB, id int, theme, density string) error {
	_, err := db.Conn().Exec(
		`INSERT INTO owner_settings (id, theme, density, updated_at) VALUES (?, ?, ?, ?)`,
		id, theme, density, 0,
	)
	return err
}
