package storage

import (
	"context"
	"testing"
	"time"
)

// TestSettings_Get_OnFreshDB_ReturnsDefaults proves Get on a migrated-but-
// empty DB resolves to the frozen design-system defaults with a nil error
// (never 500) — a fresh DB has no seed row to reason about.
func TestSettings_Get_OnFreshDB_ReturnsDefaults(t *testing.T) {
	db := migratedOwnerSettingsDB(t)
	repo := NewSettingsRepo(db)

	row, err := repo.Get(context.Background())
	if err != nil {
		t.Fatalf("Get on fresh DB: error = %v, want nil (defaults, never 500)", err)
	}
	if row.Theme != DefaultTheme {
		t.Fatalf("Theme = %q, want default %q", row.Theme, DefaultTheme)
	}
	if row.Density != DefaultDensity {
		t.Fatalf("Density = %q, want default %q", row.Density, DefaultDensity)
	}
	if !row.UpdatedAt.IsZero() {
		t.Fatalf("UpdatedAt = %v, want zero (no row exists yet)", row.UpdatedAt)
	}

	// And no row physically exists.
	var n int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM owner_settings`).Scan(&n); err != nil {
		t.Fatalf("count owner_settings: %v", err)
	}
	if n != 0 {
		t.Fatalf("owner_settings row count = %d, want 0 (Get must not seed)", n)
	}
}

// TestSettings_PutThenGet_RoundTrips proves Put then Get round-trips the
// theme/density/updated_at values.
func TestSettings_PutThenGet_RoundTrips(t *testing.T) {
	db := migratedOwnerSettingsDB(t)
	repo := NewSettingsRepo(db)
	ctx := context.Background()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)

	if err := repo.Put(ctx, "venom-light", "compact", now); err != nil {
		t.Fatalf("Put: error = %v", err)
	}

	row, err := repo.Get(ctx)
	if err != nil {
		t.Fatalf("Get after Put: error = %v", err)
	}
	if row.Theme != "venom-light" {
		t.Fatalf("Theme = %q, want venom-light", row.Theme)
	}
	if row.Density != "compact" {
		t.Fatalf("Density = %q, want compact", row.Density)
	}
	if !row.UpdatedAt.Equal(now) {
		t.Fatalf("UpdatedAt = %v, want %v", row.UpdatedAt, now)
	}
}

// TestSettings_SecondPut_UpdatesSameSingleRow proves a second Put updates
// the SAME single row (still exactly one row) rather than inserting a
// second — the UPSERT + the CHECK(id = 1) backstop.
func TestSettings_SecondPut_UpdatesSameSingleRow(t *testing.T) {
	db := migratedOwnerSettingsDB(t)
	repo := NewSettingsRepo(db)
	ctx := context.Background()
	t0 := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)

	if err := repo.Put(ctx, "venom-dark", "comfortable", t0); err != nil {
		t.Fatalf("first Put: %v", err)
	}
	if err := repo.Put(ctx, "venom-hc", "compact", t1); err != nil {
		t.Fatalf("second Put: %v", err)
	}

	// Still exactly one row.
	var n int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM owner_settings`).Scan(&n); err != nil {
		t.Fatalf("count owner_settings: %v", err)
	}
	if n != 1 {
		t.Fatalf("owner_settings row count after second Put = %d, want 1 (UPSERT, not insert)", n)
	}

	// And it holds the latest values.
	row, err := repo.Get(ctx)
	if err != nil {
		t.Fatalf("Get after second Put: %v", err)
	}
	if row.Theme != "venom-hc" || row.Density != "compact" || !row.UpdatedAt.Equal(t1) {
		t.Fatalf("row after second Put = %+v, want venom-hc/compact/%v", row, t1)
	}
}

// --- P3a-CAPI-003: the enrichment toggle ---

// TestSettings_EnrichmentOffByDefault proves Get on a fresh migrated DB
// resolves EnrichmentEnabled to false (04 §2b: "off by default").
func TestSettings_EnrichmentOffByDefault(t *testing.T) {
	db := migratedOwnerSettingsDB(t)
	repo := NewSettingsRepo(db)

	row, err := repo.Get(context.Background())
	if err != nil {
		t.Fatalf("Get on fresh DB: error = %v", err)
	}
	if row.EnrichmentEnabled {
		t.Fatalf("EnrichmentEnabled = true on a fresh DB, want false (off by default)")
	}
}

// TestSettings_PutEnrichmentPersistsAndPreservesThemeDensity proves
// PutEnrichment persists the toggle without disturbing theme/density, and
// that a later Put(theme, density) does not reset enrichment back to off.
func TestSettings_PutEnrichmentPersistsAndPreservesThemeDensity(t *testing.T) {
	db := migratedOwnerSettingsDB(t)
	repo := NewSettingsRepo(db)
	ctx := context.Background()
	t0 := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	t2 := t1.Add(time.Hour)

	if err := repo.Put(ctx, "venom-light", "compact", t0); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := repo.PutEnrichment(ctx, true, t1); err != nil {
		t.Fatalf("PutEnrichment(true): %v", err)
	}

	row, err := repo.Get(ctx)
	if err != nil {
		t.Fatalf("Get after PutEnrichment: %v", err)
	}
	if !row.EnrichmentEnabled {
		t.Fatalf("EnrichmentEnabled = false after PutEnrichment(true), want true")
	}
	if row.Theme != "venom-light" || row.Density != "compact" {
		t.Fatalf("theme/density after PutEnrichment = %s/%s, want venom-light/compact (unchanged)", row.Theme, row.Density)
	}

	// A later theme/density Put must NOT reset enrichment back to off.
	if err := repo.Put(ctx, "venom-hc", "comfortable", t2); err != nil {
		t.Fatalf("second Put: %v", err)
	}
	row, err = repo.Get(ctx)
	if err != nil {
		t.Fatalf("Get after second Put: %v", err)
	}
	if !row.EnrichmentEnabled {
		t.Fatalf("EnrichmentEnabled = false after a theme/density Put, want true (still enabled)")
	}
	if row.Theme != "venom-hc" || row.Density != "comfortable" {
		t.Fatalf("theme/density after second Put = %s/%s, want venom-hc/comfortable", row.Theme, row.Density)
	}
}

// TestSettings_PutEnrichmentOnFreshDB_SeedsFrozenDefaults proves
// PutEnrichment on a completely fresh DB (no row yet) inserts the frozen
// design-system defaults alongside the enrichment value, rather than
// leaving theme/density at a zero value.
func TestSettings_PutEnrichmentOnFreshDB_SeedsFrozenDefaults(t *testing.T) {
	db := migratedOwnerSettingsDB(t)
	repo := NewSettingsRepo(db)
	ctx := context.Background()

	if err := repo.PutEnrichment(ctx, true, time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("PutEnrichment on fresh DB: %v", err)
	}

	row, err := repo.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row.Theme != DefaultTheme || row.Density != DefaultDensity {
		t.Fatalf("theme/density after fresh PutEnrichment = %s/%s, want frozen defaults %s/%s", row.Theme, row.Density, DefaultTheme, DefaultDensity)
	}
	if !row.EnrichmentEnabled {
		t.Fatalf("EnrichmentEnabled = false, want true")
	}
}

// TestSettings_Put_InvalidValue_RejectedByDBCheck proves the owner_settings
// CHECK constraint is the defense-in-depth backstop: an invalid value that
// somehow bypasses the httpapi handler's validation is rejected at the DB
// layer. (The handler is the primary validation; this is the backstop.)
func TestSettings_Put_InvalidValue_RejectedByDBCheck(t *testing.T) {
	db := migratedOwnerSettingsDB(t)
	repo := NewSettingsRepo(db)

	if err := repo.Put(context.Background(), "not-a-real-theme", "comfortable", time.Now()); err == nil {
		t.Fatalf("Put with invalid theme succeeded, want CHECK rejection (DB backstop)")
	}
	if err := repo.Put(context.Background(), "venom-dark", "not-a-real-density", time.Now()); err == nil {
		t.Fatalf("Put with invalid density succeeded, want CHECK rejection (DB backstop)")
	}
}
