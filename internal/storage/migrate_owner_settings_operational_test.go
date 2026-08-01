package storage

import (
	"context"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

// ownerSettingsOperationalVersion is the goose version of
// 00014_owner_settings_operational.sql.
const ownerSettingsOperationalVersion = 14

// operationalColumns enumerates the four columns 00014 adds to
// owner_settings, in migration order.
var operationalColumns = []string{
	"quota_staleness_seconds",
	"probe_max_in_flight_per_provider",
	"probe_expensive_enabled",
	"probe_per_account_window_seconds",
}

// TestOwnerSettingsOperational_UpAddsColumnsWithFrozenDefaults proves 00014's
// schema-level defaults equal today's hardcoded Go constants EXACTLY, so an
// existing install upgrades to its current behaviour rather than to a new one.
// The expected values are read from the Go constants themselves, not
// re-typed, so a drift on either side fails here.
func TestOwnerSettingsOperational_UpAddsColumnsWithFrozenDefaults(t *testing.T) {
	db := migratedOwnerSettingsDB(t)

	if _, err := db.Conn().Exec(
		`INSERT INTO owner_settings (id, theme, density, updated_at) VALUES (1, 'venom-dark', 'comfortable', 0)`,
	); err != nil {
		t.Fatalf("insert owner_settings row without operational columns: %v", err)
	}

	var (
		staleness   int
		maxInFlight int
		expensive   int
		accountWndw int
	)
	if err := db.Conn().QueryRow(
		`SELECT quota_staleness_seconds, probe_max_in_flight_per_provider,
			probe_expensive_enabled, probe_per_account_window_seconds
		 FROM owner_settings WHERE id = 1`,
	).Scan(&staleness, &maxInFlight, &expensive, &accountWndw); err != nil {
		t.Fatalf("query operational columns: %v", err)
	}

	if want := int(quota.DefaultStalenessWindow / time.Second); staleness != want {
		t.Fatalf("quota_staleness_seconds default = %d, want %d (quota.DefaultStalenessWindow)", staleness, want)
	}
	if staleness != DefaultQuotaStalenessSeconds {
		t.Fatalf("quota_staleness_seconds default = %d, want the Go constant %d", staleness, DefaultQuotaStalenessSeconds)
	}
	if maxInFlight != DefaultProbeMaxInFlightPerProvider {
		t.Fatalf("probe_max_in_flight_per_provider default = %d, want %d", maxInFlight, DefaultProbeMaxInFlightPerProvider)
	}
	if expensive != 0 {
		t.Fatalf("probe_expensive_enabled default = %d, want 0 (04 §2: expensive probes are opt-in)", expensive)
	}
	if DefaultProbeExpensiveEnabled {
		t.Fatalf("DefaultProbeExpensiveEnabled = true, want false")
	}
	if accountWndw != DefaultProbePerAccountWindowSeconds {
		t.Fatalf("probe_per_account_window_seconds default = %d, want %d", accountWndw, DefaultProbePerAccountWindowSeconds)
	}
}

// TestOwnerSettingsOperational_Checks proves each new column's CHECK is a real
// DB-level invariant. Widening any of them (e.g. `>= 0` instead of `>= 1`)
// makes this test RED.
func TestOwnerSettingsOperational_Checks(t *testing.T) {
	tests := []struct {
		name    string
		column  string
		allowed []int
		refused []int
	}{
		{
			name: "quota_staleness_seconds must be positive", column: "quota_staleness_seconds",
			allowed: []int{1, 900, 86400}, refused: []int{0, -1, -900},
		},
		{
			// >= 1, not >= 0: a cap of 0 would refuse every probe forever.
			name: "probe_max_in_flight_per_provider must be at least 1", column: "probe_max_in_flight_per_provider",
			allowed: []int{1, 2, 64}, refused: []int{0, -1},
		},
		{
			name: "probe_expensive_enabled is boolean", column: "probe_expensive_enabled",
			allowed: []int{0, 1}, refused: []int{-1, 2, 7},
		},
		{
			name: "probe_per_account_window_seconds must be positive", column: "probe_per_account_window_seconds",
			allowed: []int{1, 86400}, refused: []int{0, -1},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := migratedOwnerSettingsDB(t)
			for _, v := range tc.allowed {
				if err := insertOwnerSettingsOperationalRow(db, tc.column, v); err != nil {
					t.Fatalf("%s = %d: %v, want accepted", tc.column, v, err)
				}
				if _, err := db.Conn().Exec(`DELETE FROM owner_settings WHERE id = 1`); err != nil {
					t.Fatalf("clear owner_settings: %v", err)
				}
			}
			for _, v := range tc.refused {
				if err := insertOwnerSettingsOperationalRow(db, tc.column, v); err == nil {
					t.Fatalf("%s = %d was accepted, want CHECK rejection", tc.column, v)
				}
				if _, err := db.Conn().Exec(`DELETE FROM owner_settings WHERE id = 1`); err != nil {
					t.Fatalf("clear owner_settings: %v", err)
				}
			}
		})
	}
}

// TestOwnerSettingsOperational_DownRemovesColumns proves the down path drops
// exactly these four columns and leaves everything below intact. The rollback
// loop is count-agnostic so a future 00015 does not silently break it.
func TestOwnerSettingsOperational_DownRemovesColumns(t *testing.T) {
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
	for _, col := range operationalColumns {
		assertColumnExists(t, db, "owner_settings", col, true)
	}

	for currentVersion(t, db) >= ownerSettingsOperationalVersion {
		if _, err := DownOne(ctx, db); err != nil {
			t.Fatalf("DownOne() error = %v", err)
		}
	}
	assertTableExists(t, db, "owner_settings", true)
	for _, col := range operationalColumns {
		assertColumnExists(t, db, "owner_settings", col, false)
	}
	// The customizer columns from 00013 must survive rolling back only 00014.
	for _, col := range customizerColumns {
		assertColumnExists(t, db, "owner_settings", col, true)
	}
	assertTableExists(t, db, "route_decisions", true)
	assertTableExists(t, db, "venom_api_keys", true)
	assertBaselineTableExists(t, db, true)

	if _, err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() (re-up) error = %v", err)
	}
	for _, col := range operationalColumns {
		assertColumnExists(t, db, "owner_settings", col, true)
	}
}

// insertOwnerSettingsOperationalRow inserts the single owner_settings row
// with one operational column set explicitly, for the CHECK proofs above.
// The column name is interpolated from this file's own closed list, never
// from external input.
func insertOwnerSettingsOperationalRow(db *DB, column string, value int) error {
	_, err := db.Conn().Exec(
		`INSERT INTO owner_settings (id, theme, density, updated_at, `+column+`)
		 VALUES (1, 'venom-dark', 'comfortable', 0, ?)`,
		value,
	)
	return err
}
