package application_test

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// fixtureAntigravityTokenProbe/UserInfo/LoadCodeAssist are pure, in-memory
// fixtures for providers.Antigravity*Probe — no real network call, no
// httptest server — proving this integration test exercises the real
// providers.AntigravityAdapter end to end through OAuthEnrollmentService
// without ever touching the network (P2b-PROV-007's "no real network"
// test requirement).
func fixtureAntigravityTokenProbeFn(clientSecret string) providers.AntigravityTokenProbe {
	return func(_ context.Context, _ string, form url.Values) ([]byte, error) {
		if form.Get("client_secret") != clientSecret {
			return nil, context.DeadlineExceeded // wrong secret: fail closed, distinctly
		}
		return []byte(`{"access_token":"acc-tok-9f3kx2qw","refresh_token":"ref-tok-8pl0zt7v","expires_in":3600}`), nil
	}
}

func fixtureAntigravityUserInfoProbeFn(email string) providers.AntigravityGetProbe {
	return func(_ context.Context, _, _ string) ([]byte, error) {
		body, _ := json.Marshal(map[string]string{"email": email})
		return body, nil
	}
}

func fixtureAntigravityLoadCodeAssistProbeFn(projectID, tierID, tierName string) providers.AntigravityPostProbe {
	return func(_ context.Context, _, _ string, _ []byte) ([]byte, error) {
		body, _ := json.Marshal(map[string]any{
			"cloudaicompanionProject": projectID,
			"currentTier":             map[string]string{"id": tierID, "name": tierName},
		})
		return body, nil
	}
}

// TestOAuthService_Antigravity_FreeplanPersistsConfidence095 proves the
// PROV-006 funding-confidence gap this unit closes: a connected
// antigravity Free-plan account persists funding=free,
// source=provider_evidence, confidence=0.95 — the adapter's OWN
// confidence, not a hard-coded 1.0 — in account_funding_evidence.
func TestOAuthService_Antigravity_FreeplanPersistsConfidence095(t *testing.T) {
	const canarySecret = "Jk2Qm7Xr4Nt9Vy6Bp3Ld8Fc1Ha5Zs0Rw-client-secret"

	db := migratedDB(t)
	seedProvider(t, db, "antigravity")

	adapter := providers.NewAntigravityAdapter(
		"antigravity-client-id", canarySecret,
		fixtureAntigravityTokenProbeFn(canarySecret),
		fixtureAntigravityUserInfoProbeFn("owner@example.com"),
		fixtureAntigravityLoadCodeAssistProbeFn("proj-free-1", "tier-free", "Free"),
	)

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc, _ := newOAuthTestService(t, db, func() time.Time { return now })

	beginResult, err := svc.Begin(context.Background(), application.BeginOAuthParams{
		ProviderID: "antigravity", Adapter: adapter, RedirectURI: "http://127.0.0.1:8081/api/control/v1/oauth/antigravity/callback",
	})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	_, account, err := svc.Complete(context.Background(), application.CompleteOAuthParams{
		ProviderID: "antigravity", Adapter: adapter, RawState: mustCaptureState(t, adapter, beginResult),
		Code: "auth-code-from-google", RedirectURI: "http://127.0.0.1:8081/api/control/v1/oauth/antigravity/callback",
		FundingMode: domain.FundingModeProviderEvidence,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	fund, ok, err := storage.NewFundingEvidenceRepo(db).CurrentForAccount(context.Background(), account.ID)
	if err != nil || !ok {
		t.Fatalf("CurrentForAccount: ok=%v err=%v", ok, err)
	}
	if fund.Funding != domain.FundingFree {
		t.Fatalf("Funding = %q, want free", fund.Funding)
	}
	if fund.Source != domain.FundingSourceProviderEvidence {
		t.Fatalf("Source = %q, want provider_evidence", fund.Source)
	}
	if fund.Confidence != 0.95 {
		t.Fatalf("Confidence = %v, want 0.95 (the adapter's own confidence, not a hard-coded 1.0)", fund.Confidence)
	}

	// Canary: the client secret, the authorization code, and the raw
	// access/refresh tokens must never land in ANY DB column in
	// plaintext outside the encrypted credential envelope.
	assertNoFragmentAnywhere(t, db, "accounts", canarySecret)
	assertNoFragmentAnywhere(t, db, "account_credentials", canarySecret)
	assertNoFragmentAnywhere(t, db, "account_funding_evidence", canarySecret)
	assertNoFragmentAnywhere(t, db, "oauth_transactions", canarySecret)
	assertNoFragmentAnywhere(t, db, "accounts", "auth-code-from-google")
	assertNoFragmentAnywhere(t, db, "account_credentials", "auth-code-from-google")
	assertNoFragmentAnywhere(t, db, "accounts", "acc-tok-9f3kx2qw")
	assertNoFragmentAnywhere(t, db, "accounts", "ref-tok-8pl0zt7v")

	// The credential row's own plaintext columns (kind/state/fingerprint)
	// must never literally equal the raw access token either — only its
	// sha256 fingerprint may correlate with it, never a plain substring.
	var fingerprint string
	if err := db.Conn().QueryRow(`SELECT fingerprint_sha256 FROM account_credentials WHERE account_id = ?`, account.ID).Scan(&fingerprint); err != nil {
		t.Fatalf("query fingerprint: %v", err)
	}
	assertNoFragment(t, fingerprint, "acc-tok-9f3kx2qw", "stored fingerprint_sha256")
}

// mustCaptureState re-derives the raw state Begin minted for this
// transaction by exploiting the fact that antigravity's BeginOAuth
// embeds it verbatim in the authorize URL's "state" query parameter —
// exactly like fakeOAuthAdapter's own lastState capture elsewhere in
// this package's tests, just read back out of the real URL instead of a
// field, since providers.AntigravityAdapter has no test-only capture
// hook of its own (it is the real, production adapter).
func mustCaptureState(t *testing.T, _ *providers.AntigravityAdapter, begin application.BeginOAuthResult) string {
	t.Helper()
	u, err := url.Parse(begin.AuthorizeURL)
	if err != nil {
		t.Fatalf("parse authorize URL: %v", err)
	}
	state := u.Query().Get("state")
	if state == "" {
		t.Fatalf("authorize URL %q carries no state parameter", begin.AuthorizeURL)
	}
	return state
}
