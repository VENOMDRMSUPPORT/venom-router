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
