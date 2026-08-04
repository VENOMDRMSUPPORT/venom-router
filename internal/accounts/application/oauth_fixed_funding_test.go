package application_test

import (
	"context"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
)

// TestOAuthService_FixedFundingStampsCatalogValueAndLock pins the fixed-mode
// funding stamp: a provider whose catalog declares fixed/paid/locked (e.g.
// clinepass) must enroll with funding=paid, source=provider_policy, and
// locked=1 — NOT the unknown/unlocked row the pre-fix code stamped, which
// misclassified every fixed-funding OAuth provider and left the lock off.
func TestOAuthService_FixedFundingStampsCatalogValueAndLock(t *testing.T) {
	db := migratedDB(t)
	seedProvider(t, db, "fake-oauth")

	adapter := newFakeOAuthAdapter()
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	svc, _ := newOAuthTestService(t, db, func() time.Time { return now })

	begin, err := svc.Begin(context.Background(), application.BeginOAuthParams{
		ProviderID: "fake-oauth", Adapter: adapter, RedirectURI: "http://127.0.0.1:8081/callback",
	})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	_ = begin

	_, account, err := svc.Complete(context.Background(), application.CompleteOAuthParams{
		ProviderID: "fake-oauth", Adapter: adapter, RawState: adapter.lastState, Code: "fake-auth-code",
		RedirectURI:   "http://127.0.0.1:8081/callback",
		FundingMode:   domain.FundingModeFixed,
		FundingFixed:  domain.FundingPaid,
		FundingLocked: true,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	var funding, source string
	var locked int
	if err := db.Conn().QueryRow(
		`SELECT funding, source, locked FROM account_funding_evidence WHERE account_id = ?`, account.ID,
	).Scan(&funding, &source, &locked); err != nil {
		t.Fatalf("query funding evidence: %v", err)
	}
	if funding != string(domain.FundingPaid) {
		t.Fatalf("funding = %q, want paid (the catalog's fixed value, never unknown)", funding)
	}
	if source != string(domain.FundingSourceProviderPolicy) {
		t.Fatalf("source = %q, want provider_policy", source)
	}
	if locked != 1 {
		t.Fatalf("locked = %d, want 1 (the catalog's lock must survive enrollment)", locked)
	}
}
