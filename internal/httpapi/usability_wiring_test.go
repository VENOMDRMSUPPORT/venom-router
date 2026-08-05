package httpapi

// usability_wiring_test.go pins BuildUsabilityService (task-8, spec
// 2026-08-05): the sweep composition root refactored to also expose a
// fast-lane VerifyAccount, so a caller (DiscoveryHandler) can hand it just
// provider+account ids and get one account's verification run right now,
// without waiting for the next scheduled sweep.

import (
	"context"
	"testing"
	"time"
)

func fixedUsabilityWiringClock() time.Time {
	return time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
}

// TestBuildUsabilityService_ReturnsBothFuncsNonNil proves the composition
// root hands back BOTH the sweep's Tick and the fast-lane VerifyAccount,
// never a nil func a caller could panic on invoking.
func TestBuildUsabilityService_ReturnsBothFuncsNonNil(t *testing.T) {
	db := testControlDB(t)
	kr := testKeyring(t)
	clock := fixedUsabilityWiringClock()

	svc, err := BuildUsabilityService(db, kr, func() time.Time { return clock })
	if err != nil {
		t.Fatalf("BuildUsabilityService: %v", err)
	}
	if svc == nil {
		t.Fatal("BuildUsabilityService returned a nil service")
	}
	if svc.Tick == nil {
		t.Fatal("svc.Tick is nil")
	}
	if svc.VerifyAccount == nil {
		t.Fatal("svc.VerifyAccount is nil")
	}
}

// TestUsabilityService_VerifyAccount_UnknownProviderIsNoop proves the
// fast-lane is a safe no-op for a provider absent from
// usabilityProviderSpecs(): it must return without touching the DB at all
// (no panic, no query, no row written) — the map-miss check happens BEFORE
// any account lookup or credential resolution. Run against a freshly
// migrated, otherwise-empty DB: if VerifyAccount tried to look anything up
// for this provider/account pair it would find nothing and could still
// return cleanly, so the real proof is simply that this never panics and
// the accounts table stays empty.
func TestUsabilityService_VerifyAccount_UnknownProviderIsNoop(t *testing.T) {
	db := testControlDB(t)
	kr := testKeyring(t)
	clock := fixedUsabilityWiringClock()

	svc, err := BuildUsabilityService(db, kr, func() time.Time { return clock })
	if err != nil {
		t.Fatalf("BuildUsabilityService: %v", err)
	}

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("VerifyAccount panicked on an unknown provider: %v", r)
			}
		}()
		svc.VerifyAccount(context.Background(), "no-such-provider", "no-such-account")
	}()

	if n := countRowsQuery(t, db, `SELECT COUNT(*) FROM accounts`); n != 0 {
		t.Fatalf("accounts row count = %d, want 0 (VerifyAccount must not touch the DB for an unknown provider)", n)
	}
}
