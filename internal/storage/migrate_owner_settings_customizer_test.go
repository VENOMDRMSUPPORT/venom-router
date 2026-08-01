package storage

import (
	"context"
	"testing"
)

// ownerSettingsCustomizerVersion is the goose version of the
// owner-settings customizer migration (00013_owner_settings_customizer.sql).
const ownerSettingsCustomizerVersion = 13

// customizerColumns enumerates the three columns 00013 adds to
// owner_settings, in migration order.
var customizerColumns = []string{"accent", "radius_px", "spacing_scale"}

// TestOwnerSettingsCustomizer_UpAddsColumnsWithDefaults proves 00013 adds
// owner_settings.{accent,radius_px,spacing_scale} with schema-level
// defaults of 'mono'/6/1.0: a freshly-inserted row that never mentions the
// columns still reads them back at those defaults — the default rule lives
// in the schema, not only in a Go-layer default (mirrors 00007's own
// default-off proof).
func TestOwnerSettingsCustomizer_UpAddsColumnsWithDefaults(t *testing.T) {
	db := migratedOwnerSettingsDB(t)

	if _, err := db.Conn().Exec(
		`INSERT INTO owner_settings (id, theme, density, updated_at) VALUES (1, 'venom-dark', 'comfortable', 0)`,
	); err != nil {
		t.Fatalf("insert owner_settings row without customizer columns: %v", err)
	}

	var (
		accent       string
		radiusPx     int
		spacingScale float64
	)
	if err := db.Conn().QueryRow(
		`SELECT accent, radius_px, spacing_scale FROM owner_settings WHERE id = 1`,
	).Scan(&accent, &radiusPx, &spacingScale); err != nil {
		t.Fatalf("query customizer columns: %v", err)
	}
	if accent != "mono" {
		t.Fatalf("accent default = %q, want mono", accent)
	}
	if radiusPx != 6 {
		t.Fatalf("radius_px default = %d, want 6", radiusPx)
	}
	if spacingScale != 1.0 {
		t.Fatalf("spacing_scale default = %v, want 1.0", spacingScale)
	}
}

// TestOwnerSettingsCustomizer_DownRemovesColumns proves 00013's down path
// drops the three columns while leaving owner_settings (and every lower
// table) intact. The rollback loop is count-agnostic: it rolls back every
// migration at or above ownerSettingsCustomizerVersion, so a later
// migration lands without silently breaking this test (mirrors 00007's
// robustness shape).
func TestOwnerSettingsCustomizer_DownRemovesColumns(t *testing.T) {
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
	for _, col := range customizerColumns {
		assertColumnExists(t, db, "owner_settings", col, true)
	}

	for currentVersion(t, db) >= ownerSettingsCustomizerVersion {
		if _, err := DownOne(ctx, db); err != nil {
			t.Fatalf("DownOne() error = %v", err)
		}
	}
	assertTableExists(t, db, "owner_settings", true)
	for _, col := range customizerColumns {
		assertColumnExists(t, db, "owner_settings", col, false)
	}
	// Every lower table must survive rolling back only 00013.
	assertTableExists(t, db, "venom_api_keys", true)
	assertTableExists(t, db, "usage_records", true)
	assertTableExists(t, db, "route_decisions", true)
	assertTableExists(t, db, "audit_events", true)
	assertTableExists(t, db, "jobs", true)
	assertTableExists(t, db, "providers", true)
	assertTableExists(t, db, "accounts", true)
	assertBaselineTableExists(t, db, true)

	if _, err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() (re-up) error = %v", err)
	}
	for _, col := range customizerColumns {
		assertColumnExists(t, db, "owner_settings", col, true)
	}
}

// TestOwnerSettingsCustomizer_AccentCheck proves the accent column's CHECK
// constraint is a mutation-provable DB-level invariant: every frozen
// design-system accent value is accepted, and every other value is
// rejected. A reviewer re-proves this by dropping the CHECK and confirming
// this test goes RED (mirrors 00005's theme/density CHECK proofs).
func TestOwnerSettingsCustomizer_AccentCheck(t *testing.T) {
	db := migratedOwnerSettingsDB(t)

	for _, accent := range []string{"mono", "blue", "violet", "amber", "emerald", "rose"} {
		if err := insertOwnerSettingsCustomizerRow(db, accent, 6, 1.0); err != nil {
			t.Fatalf("insert owner_settings with allowed accent %q: %v, want success", accent, err)
		}
		if _, err := db.Conn().Exec(`DELETE FROM owner_settings WHERE id = 1`); err != nil {
			t.Fatalf("clear owner_settings between accent trials: %v", err)
		}
	}

	for _, accent := range []string{"", "black", "MONO", "teal"} {
		if err := insertOwnerSettingsCustomizerRow(db, accent, 6, 1.0); err == nil {
			t.Fatalf("insert owner_settings with disallowed accent %q succeeded, want CHECK rejection", accent)
		}
	}
}

// TestOwnerSettingsCustomizer_RadiusCheck proves the radius_px column's
// CHECK constraint (integer 0..16) is a mutation-provable DB-level
// invariant.
func TestOwnerSettingsCustomizer_RadiusCheck(t *testing.T) {
	db := migratedOwnerSettingsDB(t)

	for _, radius := range []int{0, 6, 16} {
		if err := insertOwnerSettingsCustomizerRow(db, "mono", radius, 1.0); err != nil {
			t.Fatalf("insert owner_settings with allowed radius_px %d: %v, want success", radius, err)
		}
		if _, err := db.Conn().Exec(`DELETE FROM owner_settings WHERE id = 1`); err != nil {
			t.Fatalf("clear owner_settings between radius trials: %v", err)
		}
	}

	for _, radius := range []int{-1, 17, 100} {
		if err := insertOwnerSettingsCustomizerRow(db, "mono", radius, 1.0); err == nil {
			t.Fatalf("insert owner_settings with disallowed radius_px %d succeeded, want CHECK rejection", radius)
		}
	}
}

// TestOwnerSettingsCustomizer_SpacingCheck proves the spacing_scale
// column's CHECK constraint (0.75..1.25) is a mutation-provable DB-level
// invariant.
func TestOwnerSettingsCustomizer_SpacingCheck(t *testing.T) {
	db := migratedOwnerSettingsDB(t)

	for _, scale := range []float64{0.75, 1.0, 1.25} {
		if err := insertOwnerSettingsCustomizerRow(db, "mono", 6, scale); err != nil {
			t.Fatalf("insert owner_settings with allowed spacing_scale %v: %v, want success", scale, err)
		}
		if _, err := db.Conn().Exec(`DELETE FROM owner_settings WHERE id = 1`); err != nil {
			t.Fatalf("clear owner_settings between spacing trials: %v", err)
		}
	}

	for _, scale := range []float64{0.5, 0.74, 1.26, 2.0} {
		if err := insertOwnerSettingsCustomizerRow(db, "mono", 6, scale); err == nil {
			t.Fatalf("insert owner_settings with disallowed spacing_scale %v succeeded, want CHECK rejection", scale)
		}
	}
}

// insertOwnerSettingsCustomizerRow inserts the single owner_settings row
// with explicit customizer values (theme/density held at the frozen
// defaults), for the CHECK-constraint proofs above.
func insertOwnerSettingsCustomizerRow(db *DB, accent string, radiusPx int, spacingScale float64) error {
	_, err := db.Conn().Exec(
		`INSERT INTO owner_settings (id, theme, density, accent, radius_px, spacing_scale, updated_at)
		 VALUES (1, 'venom-dark', 'comfortable', ?, ?, ?, 0)`,
		accent, radiusPx, spacingScale,
	)
	return err
}
