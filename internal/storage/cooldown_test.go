package storage

import (
	"context"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

func TestCooldown_SetGetClear(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-cooldown-sgc")
	insertAccount(t, db, "acct-cooldown-sgc", "prov-cooldown-sgc")

	repo := NewCooldownRepo(db, fixedQuotaClock(1000))
	accountID := "acct-cooldown-sgc"

	if err := repo.SetCooldown(ctx, quota.CooldownScopeAccount, &accountID, nil, nil, "rate_limited", fixedQuotaClock(2000)(), quota.CooldownSourceRetryAfter); err != nil {
		t.Fatalf("SetCooldown: %v", err)
	}

	active, err := repo.GetActiveCooldown(ctx, string(quota.CooldownScopeAccount), accountID)
	if err != nil {
		t.Fatalf("GetActiveCooldown: %v", err)
	}
	if active == nil {
		t.Fatal("GetActiveCooldown = nil, want an active cooldown")
	}
	if active.ReasonCode != "rate_limited" || active.Source != quota.CooldownSourceRetryAfter {
		t.Fatalf("active = %+v, want (reason=rate_limited source=retry_after)", active)
	}
	if active.AccountID == nil || *active.AccountID != accountID {
		t.Fatalf("active.AccountID = %v, want %q", active.AccountID, accountID)
	}

	// Advance the clock past `until` (2000) before clearing.
	laterRepo := NewCooldownRepo(db, fixedQuotaClock(3000))
	n, err := laterRepo.ClearExpiredCooldowns(ctx)
	if err != nil {
		t.Fatalf("ClearExpiredCooldowns: %v", err)
	}
	if n != 1 {
		t.Fatalf("ClearExpiredCooldowns() = %d, want 1", n)
	}

	afterClear, err := repo.GetActiveCooldown(ctx, string(quota.CooldownScopeAccount), accountID)
	if err != nil {
		t.Fatalf("GetActiveCooldown after clear: %v", err)
	}
	if afterClear != nil {
		t.Fatalf("GetActiveCooldown after clear = %+v, want nil", afterClear)
	}
}

// TestCooldown_UpsertSecondValueWins proves calling SetCooldown twice for
// the same identity UPDATEs the existing row (new until/reason/source
// win) rather than creating a duplicate — the UPSERT contract required
// by the partial unique index per scope.
func TestCooldown_UpsertSecondValueWins(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-cooldown-upsert")
	providerID := "prov-cooldown-upsert"

	repo := NewCooldownRepo(db, fixedQuotaClock(1000))
	if err := repo.SetCooldown(ctx, quota.CooldownScopeProvider, nil, nil, &providerID, "first_reason", fixedQuotaClock(2000)(), quota.CooldownSourceDefaultBackoff); err != nil {
		t.Fatalf("first SetCooldown: %v", err)
	}
	if err := repo.SetCooldown(ctx, quota.CooldownScopeProvider, nil, nil, &providerID, "second_reason", fixedQuotaClock(5000)(), quota.CooldownSourceRetryAfter); err != nil {
		t.Fatalf("second SetCooldown: %v", err)
	}

	var count int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM cooldowns WHERE provider_id = ?`, providerID).Scan(&count); err != nil {
		t.Fatalf("count cooldowns: %v", err)
	}
	if count != 1 {
		t.Fatalf("cooldowns rows for provider = %d, want exactly 1 (UPSERT, never a duplicate)", count)
	}

	active, err := repo.GetActiveCooldown(ctx, string(quota.CooldownScopeProvider), providerID)
	if err != nil {
		t.Fatalf("GetActiveCooldown: %v", err)
	}
	if active == nil {
		t.Fatal("GetActiveCooldown = nil, want an active cooldown")
	}
	if active.ReasonCode != "second_reason" || active.Source != quota.CooldownSourceRetryAfter {
		t.Fatalf("active = %+v, want the SECOND call's values (second_reason, retry_after)", active)
	}
	if active.Until.Unix() != 5000 {
		t.Fatalf("active.Until = %v, want 5000 (the second call's value)", active.Until.Unix())
	}
}

func TestCooldown_OfferingScope(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	opID := seedOfferingOperationChain(t, db, "acct-cooldown-offering", "prov-cooldown-offering", "model-cooldown-offering", "model-cooldown-offering-pm")

	repo := NewCooldownRepo(db, fixedQuotaClock(1000))
	if err := repo.SetCooldown(ctx, quota.CooldownScopeOffering, nil, &opID, nil, "quota_exhausted", fixedQuotaClock(2000)(), quota.CooldownSourceRetryAfter); err != nil {
		t.Fatalf("SetCooldown: %v", err)
	}

	active, err := repo.GetActiveCooldown(ctx, string(quota.CooldownScopeOffering), opID)
	if err != nil {
		t.Fatalf("GetActiveCooldown: %v", err)
	}
	if active == nil || active.OfferingOperationID == nil || *active.OfferingOperationID != opID {
		t.Fatalf("active = %+v, want OfferingOperationID = %q", active, opID)
	}
}

// TestCooldownForScope_MatchesExactIdentityIsNullChecks proves
// CooldownForScope filters strictly on the given scope's identity column
// with correct IS NULL semantics for the other two — never returning a
// row from an unrelated scope/identity.
func TestCooldownForScope_MatchesExactIdentityIsNullChecks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-cooldown-scope-a")
	insertProvider(t, db, "prov-cooldown-scope-b")
	providerA := "prov-cooldown-scope-a"
	providerB := "prov-cooldown-scope-b"

	repo := NewCooldownRepo(db, fixedQuotaClock(1000))
	if err := repo.SetCooldown(ctx, quota.CooldownScopeProvider, nil, nil, &providerA, "a", fixedQuotaClock(2000)(), quota.CooldownSourceDefaultBackoff); err != nil {
		t.Fatalf("SetCooldown a: %v", err)
	}
	if err := repo.SetCooldown(ctx, quota.CooldownScopeProvider, nil, nil, &providerB, "b", fixedQuotaClock(2000)(), quota.CooldownSourceDefaultBackoff); err != nil {
		t.Fatalf("SetCooldown b: %v", err)
	}

	rows, err := repo.CooldownForScope(ctx, string(quota.CooldownScopeProvider), nil, nil, &providerA)
	if err != nil {
		t.Fatalf("CooldownForScope: %v", err)
	}
	if len(rows) != 1 || rows[0].ProviderID == nil || *rows[0].ProviderID != providerA {
		t.Fatalf("CooldownForScope(providerA) = %+v, want exactly one row for providerA", rows)
	}
}
