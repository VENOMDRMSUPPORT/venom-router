package storage

import (
	"context"
	"testing"
)

func TestOwnerSettingsTwoThemes_UpDownUpMigratesHighContrastToDark(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	provider, err := newProvider(db)
	if err != nil {
		t.Fatalf("newProvider() error = %v", err)
	}
	if _, err := provider.UpTo(ctx, 14); err != nil {
		t.Fatalf("migrate to v14: %v", err)
	}
	if _, err := db.Conn().Exec(`
		INSERT INTO owner_settings (
			id, theme, density, updated_at, enrichment_enabled, accent, radius_px,
			spacing_scale, quota_staleness_seconds, probe_max_in_flight_per_provider,
			probe_expensive_enabled, probe_per_account_window_seconds
		) VALUES (1, 'venom-hc', 'compact', 123, 1, 'rose', 12, 1.2, 1200, 3, 1, 43200)
	`); err != nil {
		t.Fatalf("seed v14 high-contrast settings: %v", err)
	}

	// UpTo(15)/DownTo(14) pin the exact versions this test reasons about (the
	// two-themes migration is 15). Relative Up/Down would assume 15 is HEAD and
	// break the moment any migration is added above it.
	if _, err := provider.UpTo(ctx, 15); err != nil {
		t.Fatalf("migrate to v15: %v", err)
	}
	assertTwoThemeSettingsRow(t, db)
	if _, err := db.Conn().Exec(`DELETE FROM owner_settings WHERE id = 1`); err != nil {
		t.Fatalf("clear migrated row before constraint proof: %v", err)
	}
	if err := insertOwnerSettingsRow(db, "venom-hc", "comfortable"); err == nil {
		t.Fatal("v15 accepted retired venom-hc theme")
	}
	if err := insertOwnerSettingsRow(db, "venom-dark", "compact"); err != nil {
		t.Fatalf("restore dark row after constraint proof: %v", err)
	}

	if _, err := provider.DownTo(ctx, 14); err != nil {
		t.Fatalf("roll back v15: %v", err)
	}
	var downTheme string
	if err := db.Conn().QueryRow(`SELECT theme FROM owner_settings WHERE id = 1`).Scan(&downTheme); err != nil || downTheme != "venom-dark" {
		t.Fatalf("dark theme did not survive down migration: theme=%q err=%v", downTheme, err)
	}
	if _, err := db.Conn().Exec(`DELETE FROM owner_settings WHERE id = 1`); err != nil {
		t.Fatalf("clear down-migrated row: %v", err)
	}
	if err := insertOwnerSettingsRow(db, "venom-hc", "comfortable"); err != nil {
		t.Fatalf("v14 down schema did not restore venom-hc allowance: %v", err)
	}

	if _, err := provider.UpTo(ctx, 15); err != nil {
		t.Fatalf("re-apply v15: %v", err)
	}
	var theme string
	if err := db.Conn().QueryRow(`SELECT theme FROM owner_settings WHERE id = 1`).Scan(&theme); err != nil {
		t.Fatalf("read re-migrated theme: %v", err)
	}
	if theme != "venom-dark" {
		t.Fatalf("theme after re-up = %q, want venom-dark", theme)
	}
}

func assertTwoThemeSettingsRow(t *testing.T, db *DB) {
	t.Helper()
	var theme, density, accent string
	var updatedAt, enrichment, radius, staleness, maxInFlight, expensive, accountWindow int
	var spacing float64
	if err := db.Conn().QueryRow(`
		SELECT theme, density, updated_at, enrichment_enabled, accent, radius_px,
		       spacing_scale, quota_staleness_seconds, probe_max_in_flight_per_provider,
		       probe_expensive_enabled, probe_per_account_window_seconds
		FROM owner_settings WHERE id = 1
	`).Scan(&theme, &density, &updatedAt, &enrichment, &accent, &radius, &spacing,
		&staleness, &maxInFlight, &expensive, &accountWindow); err != nil {
		t.Fatalf("read migrated settings: %v", err)
	}
	if theme != "venom-dark" || density != "compact" || updatedAt != 123 || enrichment != 1 ||
		accent != "rose" || radius != 12 || spacing != 1.2 || staleness != 1200 ||
		maxInFlight != 3 || expensive != 1 || accountWindow != 43200 {
		t.Fatalf("migrated settings changed: theme=%s density=%s updated=%d enrichment=%d accent=%s radius=%d spacing=%v staleness=%d max=%d expensive=%d window=%d",
			theme, density, updatedAt, enrichment, accent, radius, spacing, staleness, maxInFlight, expensive, accountWindow)
	}
}
