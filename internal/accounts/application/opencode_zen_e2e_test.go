package application_test

// opencode_zen_e2e_test.go is P2b-TEST-003 C.1's opencode-zen fixture
// contract test: the full end-to-end connect flow (ConnectService.
// ConnectAPIKeyAccount against a fixtureOpenCodeZenServer), asserting
// the persisted account's identity/funding/health AND a canary that the
// submitted key never appears in any persisted row.
//
// connect_service_test.go's TestConnectService_ValidKey_
// CreatesExactlyOneAccountCredentialFunding (same package) already
// proves the row-count and funding shape of this exact flow — it is NOT
// duplicated here. What it does NOT assert, and what this file adds:
//   - the persisted account's ExternalID (identity) — the key's own
//     SHA-256 fingerprint, per providers/opencode_zen.go's
//     ConnectAPIKey doc comment ("reports identity as the key's SHA-256
//     fingerprint (hex)");
//   - the persisted account's HealthState — HealthUnknown, since
//     ConnectAPIKeyAccount performs no health probe of its own this
//     phase (see connect_service.go's ConnectAPIKeyAccount doc comment);
//   - a canary that the submitted plaintext key never appears in any
//     column of any persisted table (accounts, account_credentials,
//     account_funding_evidence) — only its encrypted envelope / SHA-256
//     fingerprint may exist.
//
// Reuses fixtureOpenCodeZenServer/fixtureChatProbe/fixtureModelsProbe/
// sequentialIDGenerator from connect_service_test.go and
// assertNoFragmentAnywhere from oauth_service_test.go (all same
// package, application_test) rather than redefining any of them.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// fixtureOpenCodeZenServerAcceptingKey mirrors connect_service_test.go's
// fixtureOpenCodeZenServer but parameterizes which key the chat probe
// accepts, so this test can push a distinctive canary key through the
// real fixture rather than the shared "good-key" literal every other
// test in this package uses.
func fixtureOpenCodeZenServerAcceptingKey(t *testing.T, acceptedKey string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer "+acceptedKey {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	})
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	})
	return httptest.NewServer(mux)
}

// TestConnectService_OpenCodeZen_E2E_IdentityFundingHealthAndKeyNeverLeaks
// is the full CI-gating fixture contract test for opencode-zen (no real
// network — httptest.NewServer only): connects a fixture account (keyed
// on a distinctive canary value, so the leak canary below has something
// unique to search for) through the real ConnectService, then asserts
// the persisted account's external_id/funding/health-state shape plus
// the secret-clean canary across every table the connect flow touches.
func TestConnectService_OpenCodeZen_E2E_IdentityFundingHealthAndKeyNeverLeaks(t *testing.T) {
	const canaryKey = "CANARY-E2E-9f3Kx2Qw8pLm0Zt7Vb4Nr1Hy6Dc5Ea-Ws3Rq8Ln-connect"

	mux := fixtureOpenCodeZenServerAcceptingKey(t, canaryKey)
	defer mux.Close()

	db := migratedDB(t)
	seedProvider(t, db, "opencode-zen")

	adapter := providers.NewOpenCodeZenAdapter(fixtureChatProbe(mux.URL), fixtureModelsProbe(mux.URL), fixtureUnusedModelsDevProbe(t), nil)
	enrollment := storage.NewEnrollmentRepo(db)
	accounts := storage.NewAccountRepo(db)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc := application.NewConnectService(enrollment, accounts, newTestKeyring(t), sequentialIDGenerator("e2e-id"), func() time.Time { return now })

	account, err := svc.ConnectAPIKeyAccount(context.Background(), application.ConnectAPIKeyAccountParams{
		ProviderID: "opencode-zen", Adapter: adapter, PlaintextKey: canaryKey,
		FundingMode: domain.FundingModeOwnerPolicy,
	})
	if err != nil {
		t.Fatalf("ConnectAPIKeyAccount: %v", err)
	}

	// Identity: the persisted account's ExternalID is the key's own
	// SHA-256 hex fingerprint (opencode_zen.go's ConnectAPIKey contract),
	// never the raw key itself.
	sum := sha256.Sum256([]byte(canaryKey))
	wantExternalID := hex.EncodeToString(sum[:])
	if account.ExternalID != wantExternalID {
		t.Fatalf("account.ExternalID = %q, want the key's SHA-256 fingerprint %q", account.ExternalID, wantExternalID)
	}
	if account.ExternalID == canaryKey {
		t.Fatalf("account.ExternalID equals the raw key verbatim — must be the fingerprint, never the key itself")
	}

	// Funding: free/owner_policy (opencode-zen's catalog default via
	// FundingModeOwnerPolicy — see connect_service_test.go's sibling
	// test for the same assertion on the row-count/funding shape).
	fund, ok, err := storage.NewFundingEvidenceRepo(db).CurrentForAccount(context.Background(), account.ID)
	if err != nil || !ok {
		t.Fatalf("CurrentForAccount: ok=%v err=%v", ok, err)
	}
	if fund.Funding != domain.FundingFree || fund.Source != domain.FundingSourceOwnerPolicy {
		t.Fatalf("funding = %+v, want free/owner_policy", fund)
	}

	// Health: HealthUnknown — ConnectAPIKeyAccount performs no health
	// probe of its own this phase (connect_service.go's own doc comment
	// on the account literal it builds).
	if account.HealthState != domain.HealthUnknown {
		t.Fatalf("account.HealthState = %q, want %q (no health probe at connect time this phase)", account.HealthState, domain.HealthUnknown)
	}
	got, ok, err := accounts.GetByID(context.Background(), account.ID)
	if err != nil || !ok {
		t.Fatalf("GetByID: ok=%v err=%v", ok, err)
	}
	if got.HealthState != domain.HealthUnknown {
		t.Fatalf("persisted HealthState = %q, want %q", got.HealthState, domain.HealthUnknown)
	}
	if got.ConnectionState != domain.ConnectionConnected {
		t.Fatalf("persisted ConnectionState = %q, want connected", got.ConnectionState)
	}

	// Canary: the submitted plaintext key never appears in ANY column of
	// ANY table the connect flow touches — only its encrypted envelope
	// (nonce/ciphertext) and its SHA-256 fingerprint may exist.
	assertNoFragmentAnywhere(t, db, "accounts", canaryKey)
	assertNoFragmentAnywhere(t, db, "account_credentials", canaryKey)
	assertNoFragmentAnywhere(t, db, "account_funding_evidence", canaryKey)
}
