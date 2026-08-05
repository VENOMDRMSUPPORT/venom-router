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

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
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

// readCertificationState reads the {status, capability_truth} of one
// offering-operation's certification row, for asserting it was left
// untouched.
func readCertificationState(t *testing.T, db *storage.DB, offeringOperationID string) (status, truth string) {
	t.Helper()
	if err := db.Conn().QueryRow(
		`SELECT status, capability_truth FROM certifications WHERE offering_operation_id = ?`,
		offeringOperationID,
	).Scan(&status, &truth); err != nil {
		t.Fatalf("read certification state for %q: %v", offeringOperationID, err)
	}
	return status, truth
}

// TestUsabilityService_VerifyAccount_ProviderMismatchIsNoop proves the
// fast-lane guard added in fix round 1: an (providerID, accountID) pair
// where accountID belongs to a DIFFERENT provider than providerID must be a
// no-op, exactly like an unknown provider. Without this guard, VerifyAccount
// would resolve accountID's OWN active credential and hand it to the WRONG
// provider's verifier (wrong probe function, wrong baseURL) — leasing one
// provider's decrypted credential and dispatching it toward another
// provider's endpoint entirely.
//
// The seeded account belongs to clinepass (a real usabilityProviderSpecs()
// entry) but VerifyAccount is called with providerID "opencode-zen" (also a
// real entry, just not THIS account's provider) — so the map-miss guard
// alone would NOT catch this; only the account.ProviderID == providerID
// check does.
//
// The account carries a declared (non-chat) "vision" capability stranded in
// `probing`: certifying it from its declaration needs no credential lease
// and no network call (certifyDeclaredCapabilities just writes a row), which
// makes its certification state the cleanest fully-deterministic, network-
// free signal available through BuildUsabilityService's public API for
// "did verifyAccount run for this account at all" — if the guard were
// missing, this row would flip to certified/supported even though the call
// used the wrong provider id. (The chat-probe path this guard also protects
// would additionally require leasing the real credential and dispatching an
// HTTP probe to the wrong provider's real base URL to observe directly,
// which is deliberately not exercised here — see the task-8 report's
// mutation-fix-round-1 section for why that isn't practical to assert
// without a live network dependency.)
func TestUsabilityService_VerifyAccount_ProviderMismatchIsNoop(t *testing.T) {
	db := testControlDB(t)
	kr := testKeyring(t)
	clock := fixedUsabilityWiringClock()

	const wrongProviderID = string(providers.ClinePassID)
	const accountID = "acct-provider-mismatch"

	if _, err := db.Conn().Exec(
		`INSERT INTO providers (id, display_name, auth_mode, funding_mode, funding_locked, created_at, updated_at) VALUES (?, ?, 'api_key', 'owner_policy', 0, 0, 0)`,
		wrongProviderID, wrongProviderID,
	); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if _, err := db.Conn().Exec(
		`INSERT INTO accounts (id, provider_id, external_id, auth_type, connection_state, health_state, created_at, updated_at)
		 VALUES (?, ?, ?, 'api_key', 'connected', 'healthy', 0, 0)`,
		accountID, wrongProviderID, accountID,
	); err != nil {
		t.Fatalf("seed account: %v", err)
	}

	credRepo := storage.NewAccountCredentialRepo(db)
	credSvc := application.NewCredentialService(credRepo, kr, func() time.Time { return clock })
	if _, err := credSvc.Store(context.Background(), application.StoreCredentialParams{
		ID:           "cred-provider-mismatch-1",
		AccountID:    accountID,
		ProviderID:   wrongProviderID,
		Kind:         domain.CredentialKindAPIKey,
		Active:       true,
		PlaintextKey: "canary-secret-should-never-leave-its-own-provider",
	}); err != nil {
		t.Fatalf("store credential: %v", err)
	}

	seedModelForCert(t, db, "mismatch-model")
	seedOfferingForCert(t, db, accountID, wrongProviderID, "mismatch-model", "mismatch-model")
	seedOfferingOperationForCert(t, db, "op-mismatch-vision", accountID, wrongProviderID, "mismatch-model", "vision", "probing", "unknown", 1, "")

	svc, err := BuildUsabilityService(db, kr, func() time.Time { return clock })
	if err != nil {
		t.Fatalf("BuildUsabilityService: %v", err)
	}

	// The cross-provider mismatch: accountID belongs to clinepass, but this
	// call names opencode-zen — a DIFFERENT, also-valid specs-map provider.
	svc.VerifyAccount(context.Background(), string(providers.OpenCodeZenID), accountID)

	status, truth := readCertificationState(t, db, "op-mismatch-vision")
	if status != "probing" || truth != "unknown" {
		t.Fatalf("certification status/truth = %q/%q after a provider-mismatched VerifyAccount call, want probing/unknown (untouched) — the mismatch guard let verification run for the wrong account", status, truth)
	}
}
