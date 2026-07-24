package storage

import (
	"context"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
)

func TestSeedProviders_IdempotentOneRowPerID(t *testing.T) {
	db := migratedEnrollmentDB(t)
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	entries := providers.BuiltinCatalog()

	if err := SeedProviders(ctx, db, entries, now); err != nil {
		t.Fatalf("first SeedProviders: %v", err)
	}
	assertRowCount(t, db, "providers", len(entries))

	if err := SeedProviders(ctx, db, entries, now.Add(time.Hour)); err != nil {
		t.Fatalf("second SeedProviders: %v", err)
	}
	assertRowCount(t, db, "providers", len(entries))

	rows, err := ListProviders(ctx, db)
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(rows) != len(entries) {
		t.Fatalf("ListProviders returned %d rows, want %d", len(rows), len(entries))
	}
}

func TestSeedProviders_ValuesRoundTripCorrectly(t *testing.T) {
	db := migratedEnrollmentDB(t)
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	if err := SeedProviders(ctx, db, providers.BuiltinCatalog(), now); err != nil {
		t.Fatalf("SeedProviders: %v", err)
	}

	row, ok, err := GetProvider(ctx, db, "clinepass")
	if err != nil || !ok {
		t.Fatalf("GetProvider(clinepass) ok=%v err=%v", ok, err)
	}
	if row.AuthMode != "oauth2" {
		t.Fatalf("clinepass auth_mode = %q, want oauth2", row.AuthMode)
	}
	if row.FundingMode != "fixed" || row.FundingFixed != "paid" || !row.FundingLocked {
		t.Fatalf("clinepass funding = mode=%q fixed=%q locked=%v, want fixed/paid/true", row.FundingMode, row.FundingFixed, row.FundingLocked)
	}

	agnes, ok, err := GetProvider(ctx, db, "agnes-ai")
	if err != nil || !ok {
		t.Fatalf("GetProvider(agnes-ai) ok=%v err=%v", ok, err)
	}
	if agnes.FundingMode != "evidence_required" {
		t.Fatalf("agnes-ai funding_mode = %q, want evidence_required", agnes.FundingMode)
	}
	if agnes.FundingFixed != "" {
		t.Fatalf("agnes-ai funding_fixed = %q, want empty (NULL)", agnes.FundingFixed)
	}
}

func TestSeedProviders_ReSeedUpdatesValuesNotDuplicates(t *testing.T) {
	db := migratedEnrollmentDB(t)
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	entry := providers.CatalogEntry{
		ID:          "opencode-zen",
		DisplayName: "Original Name",
		AuthMode:    providers.CatalogAuthAPIKey,
		Funding:     providers.FundingPolicy{Mode: providers.FundingModeOwnerPolicy},
	}
	if err := SeedProviders(ctx, db, []providers.CatalogEntry{entry}, now); err != nil {
		t.Fatalf("first seed: %v", err)
	}

	entry.DisplayName = "Updated Name"
	if err := SeedProviders(ctx, db, []providers.CatalogEntry{entry}, now.Add(time.Hour)); err != nil {
		t.Fatalf("second seed: %v", err)
	}

	assertRowCount(t, db, "providers", 1)
	row, ok, err := GetProvider(ctx, db, "opencode-zen")
	if err != nil || !ok {
		t.Fatalf("GetProvider ok=%v err=%v", ok, err)
	}
	if row.DisplayName != "Updated Name" {
		t.Fatalf("DisplayName = %q, want %q (re-seed must update, not just insert-once)", row.DisplayName, "Updated Name")
	}
}

func TestGetProvider_UnknownIDNotOK(t *testing.T) {
	db := migratedEnrollmentDB(t)
	_, ok, err := GetProvider(context.Background(), db, "does-not-exist")
	if err != nil {
		t.Fatalf("GetProvider: unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("GetProvider(unknown) ok = true, want false")
	}
}
