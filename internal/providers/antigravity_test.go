package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

// fixtureAntigravityTokenProbe returns a deterministic AntigravityTokenProbe:
// an authorization_code exchange always yields access-1/refresh-1, and a
// refresh_token grant yields access-2 while omitting refresh_token (as
// Google's real endpoint commonly does), proving RefreshCredentials
// falls back to the prior refresh token. It never inspects anything
// beyond grant_type, and it captures the last form it received so tests
// can assert the client secret was actually threaded through.
type fixtureAntigravityTokenProbe struct {
	lastForm url.Values
	calls    int
}

func (f *fixtureAntigravityTokenProbe) probe(_ context.Context, tokenURL string, form url.Values) ([]byte, error) {
	f.calls++
	f.lastForm = form
	// Deliberately if/else, not a switch on string-literal cases — this
	// package's own noslugswitch check runs over every .go file in the
	// directory, test files included.
	grantType := form.Get("grant_type")
	if grantType == "authorization_code" {
		return []byte(`{"access_token":"access-1","refresh_token":"refresh-1","expires_in":3600}`), nil
	}
	if grantType == "refresh_token" {
		return []byte(`{"access_token":"access-2","expires_in":3600}`), nil
	}
	return nil, errors.New("fixture: unrecognized grant_type")
}

func fixtureAntigravityUserInfoProbe(email string) AntigravityGetProbe {
	return func(_ context.Context, _, _ string) ([]byte, error) {
		body, _ := json.Marshal(map[string]string{"email": email})
		return body, nil
	}
}

func fixtureAntigravityLoadCodeAssistProbe(projectID, tierID, tierName string) AntigravityPostProbe {
	return func(_ context.Context, _, _ string, _ []byte) ([]byte, error) {
		body, _ := json.Marshal(map[string]any{
			"cloudaicompanionProject": projectID,
			"currentTier":             map[string]string{"id": tierID, "name": tierName},
		})
		return body, nil
	}
}

func TestAntigravityAdapter_CompleteOAuth_FreePlanMapsToFreeConfidence095(t *testing.T) {
	tokenProbe := &fixtureAntigravityTokenProbe{}
	adapter := NewAntigravityAdapter("client-id", "client-secret", tokenProbe.probe,
		fixtureAntigravityUserInfoProbe("owner@example.com"),
		fixtureAntigravityLoadCodeAssistProbe("proj-123", "tier-free", "Free"),
	)

	identity, creds, err := adapter.CompleteOAuth(context.Background(), "auth-code", "verifier", "http://127.0.0.1/callback")
	if err != nil {
		t.Fatalf("CompleteOAuth: %v", err)
	}
	if identity.ExternalID != "owner@example.com:proj-123" {
		t.Fatalf("ExternalID = %q, want %q", identity.ExternalID, "owner@example.com:proj-123")
	}
	if identity.Email != "owner@example.com" {
		t.Fatalf("Email = %q, want owner@example.com", identity.Email)
	}
	if identity.Plan != "Free" {
		t.Fatalf("Plan = %q, want Free", identity.Plan)
	}
	if identity.Funding != "free" {
		t.Fatalf("Funding = %q, want free", identity.Funding)
	}
	if identity.Confidence != antigravityConfidenceRecognizedPlan {
		t.Fatalf("Confidence = %v, want %v", identity.Confidence, antigravityConfidenceRecognizedPlan)
	}
	if identity.Evidence["tier_id"] != "tier-free" {
		t.Fatalf("Evidence[tier_id] = %v, want tier-free", identity.Evidence["tier_id"])
	}
	if creds.Value == "" || !strings.Contains(creds.Value, "access-1") {
		t.Fatalf("StoredCredentials.Value = %q, want it to carry the access token", creds.Value)
	}
	if tokenProbe.lastForm.Get("client_secret") != "client-secret" {
		t.Fatalf("token exchange form did not carry the client secret")
	}
}

func TestAntigravityAdapter_CompleteOAuth_ProPlanMapsToPaidConfidence095(t *testing.T) {
	tokenProbe := &fixtureAntigravityTokenProbe{}
	adapter := NewAntigravityAdapter("client-id", "client-secret", tokenProbe.probe,
		fixtureAntigravityUserInfoProbe("owner@example.com"),
		fixtureAntigravityLoadCodeAssistProbe("proj-456", "tier-pro", "Pro"),
	)

	identity, _, err := adapter.CompleteOAuth(context.Background(), "auth-code", "verifier", "http://127.0.0.1/callback")
	if err != nil {
		t.Fatalf("CompleteOAuth: %v", err)
	}
	if identity.Funding != "paid" {
		t.Fatalf("Funding = %q, want paid", identity.Funding)
	}
	if identity.Confidence != antigravityConfidenceRecognizedPlan {
		t.Fatalf("Confidence = %v, want %v", identity.Confidence, antigravityConfidenceRecognizedPlan)
	}
}

func TestAntigravityAdapter_CompleteOAuth_UnrecognizedPlanMapsToUnknown(t *testing.T) {
	tokenProbe := &fixtureAntigravityTokenProbe{}
	adapter := NewAntigravityAdapter("client-id", "client-secret", tokenProbe.probe,
		fixtureAntigravityUserInfoProbe("owner@example.com"),
		fixtureAntigravityLoadCodeAssistProbe("proj-789", "tier-enterprise", "Enterprise"),
	)

	identity, _, err := adapter.CompleteOAuth(context.Background(), "auth-code", "verifier", "http://127.0.0.1/callback")
	if err != nil {
		t.Fatalf("CompleteOAuth: %v", err)
	}
	if identity.Funding != "unknown" {
		t.Fatalf("Funding = %q, want unknown for an unrecognized plan name", identity.Funding)
	}
	if identity.Confidence != antigravityConfidenceUnrecognizedPlan {
		t.Fatalf("Confidence = %v, want %v for an unrecognized plan", identity.Confidence, antigravityConfidenceUnrecognizedPlan)
	}
}

// TestAntigravityAdapter_RefreshCredentials_UsesClientSecretAndFallsBackRefreshToken
// proves RefreshCredentials threads the client secret to the token
// endpoint, and — since the fixture's refresh grant response omits
// refresh_token, exactly like Google's real behavior often does — keeps
// the prior refresh token rather than dropping it.
func TestAntigravityAdapter_RefreshCredentials_UsesClientSecretAndFallsBackRefreshToken(t *testing.T) {
	tokenProbe := &fixtureAntigravityTokenProbe{}
	adapter := NewAntigravityAdapter("client-id", "the-client-secret", tokenProbe.probe,
		fixtureAntigravityUserInfoProbe("owner@example.com"),
		fixtureAntigravityLoadCodeAssistProbe("proj-123", "tier-free", "Free"),
	)

	stored := antigravityStoredToken{AccessToken: "stale-access", RefreshToken: "refresh-1", ExpiresAt: 0}
	value, err := json.Marshal(stored)
	if err != nil {
		t.Fatalf("marshal stored token: %v", err)
	}

	refreshed, err := adapter.RefreshCredentials(context.Background(), StoredCredentials{Value: string(value)})
	if err != nil {
		t.Fatalf("RefreshCredentials: %v", err)
	}
	if tokenProbe.calls != 1 {
		t.Fatalf("token probe called %d times, want 1", tokenProbe.calls)
	}
	if tokenProbe.lastForm.Get("grant_type") != "refresh_token" {
		t.Fatalf("grant_type = %q, want refresh_token", tokenProbe.lastForm.Get("grant_type"))
	}
	if tokenProbe.lastForm.Get("client_secret") != "the-client-secret" {
		t.Fatalf("refresh form did not carry the client secret")
	}
	if tokenProbe.lastForm.Get("refresh_token") != "refresh-1" {
		t.Fatalf("refresh form refresh_token = %q, want refresh-1 (the stored one)", tokenProbe.lastForm.Get("refresh_token"))
	}

	var out antigravityStoredToken
	if err := json.Unmarshal([]byte(refreshed.Value), &out); err != nil {
		t.Fatalf("unmarshal refreshed credentials: %v", err)
	}
	if out.AccessToken != "access-2" {
		t.Fatalf("refreshed access token = %q, want access-2", out.AccessToken)
	}
	if out.RefreshToken != "refresh-1" {
		t.Fatalf("refreshed refresh token = %q, want the prior refresh-1 to survive (fixture omits a new one)", out.RefreshToken)
	}

	// Canary: the client secret must never appear in any log/error/
	// return-shape a test can observe — it legitimately appears only in
	// the outbound form (asserted above), never in the refreshed
	// credentials value or in any error text.
	assertNoKeyFragment(t, refreshed.Value, "the-client-secret", "refreshed StoredCredentials.Value")
}

// TestAntigravityAdapter_RefreshCredentials_NoRefreshTokenRejected proves
// a refresh attempt with no stored refresh token fails closed rather
// than calling the token endpoint with an empty grant.
func TestAntigravityAdapter_RefreshCredentials_NoRefreshTokenRejected(t *testing.T) {
	tokenProbe := &fixtureAntigravityTokenProbe{}
	adapter := NewAntigravityAdapter("client-id", "client-secret", tokenProbe.probe, nil, nil)

	stored := antigravityStoredToken{AccessToken: "stale-access"}
	value, _ := json.Marshal(stored)

	if _, err := adapter.RefreshCredentials(context.Background(), StoredCredentials{Value: string(value)}); err == nil {
		t.Fatalf("RefreshCredentials with no refresh token = nil error, want a rejection")
	}
	if tokenProbe.calls != 0 {
		t.Fatalf("token probe called %d times, want 0 (no refresh token to refresh with)", tokenProbe.calls)
	}
}

// TestAntigravityAdapter_BeginOAuth_PureURLNoNetworkCall proves BeginOAuth
// is pure string construction (no seam is ever invoked) and carries the
// PKCE challenge, state, and the documented scopes.
func TestAntigravityAdapter_BeginOAuth_PureURLNoNetworkCall(t *testing.T) {
	// clientID/clientSecret deliberately share no 8+ character substring
	// with each other (assertNoKeyFragment's canary window is 8 chars,
	// and the authorize URL legitimately carries client_id) — otherwise
	// the leak assertion below would false-positive on the shared prefix.
	adapter := NewAntigravityAdapter("cid-alpha-999", "sec-zulu-777-value", nil, nil, nil)

	authorizeURL, err := adapter.BeginOAuth(context.Background(), "http://127.0.0.1/callback", "state-xyz", "challenge-abc")
	if err != nil {
		t.Fatalf("BeginOAuth: %v", err)
	}
	parsed, err := url.Parse(authorizeURL)
	if err != nil {
		t.Fatalf("parse authorize URL: %v", err)
	}
	q := parsed.Query()
	if q.Get("client_id") != "cid-alpha-999" {
		t.Fatalf("client_id = %q, want cid-alpha-999", q.Get("client_id"))
	}
	if q.Get("state") != "state-xyz" {
		t.Fatalf("state = %q, want state-xyz", q.Get("state"))
	}
	if q.Get("code_challenge") != "challenge-abc" || q.Get("code_challenge_method") != "S256" {
		t.Fatalf("code_challenge/method = %q/%q, want challenge-abc/S256", q.Get("code_challenge"), q.Get("code_challenge_method"))
	}
	for _, scope := range antigravityScopes {
		if !strings.Contains(q.Get("scope"), scope) {
			t.Fatalf("scope %q missing from authorize URL scope param %q", scope, q.Get("scope"))
		}
	}
	// The client secret must NEVER appear in the (browser-visible, not
	// even loopback-only) authorize URL.
	assertNoKeyFragment(t, authorizeURL, "sec-zulu-777-value", "authorize URL")
}

func TestRegisterAntigravity_CapabilitiesDeriveOAuth2(t *testing.T) {
	tokenProbe := &fixtureAntigravityTokenProbe{}
	reg := NewRegistry()
	if err := RegisterAntigravity(reg, "client-id", "client-secret", tokenProbe.probe,
		fixtureAntigravityUserInfoProbe("owner@example.com"),
		fixtureAntigravityLoadCodeAssistProbe("proj-123", "tier-free", "Free"),
	); err != nil {
		t.Fatalf("RegisterAntigravity: %v", err)
	}

	caps := DerivedCapabilities(reg, AntigravityID)
	if len(caps) != 1 || caps[0] != "oauth2" {
		t.Fatalf("DerivedCapabilities = %v, want [oauth2]", caps)
	}
}

// TestAntigravityAdapter_Canary_ClientSecretNeverInIdentityOrError pushes
// a distinctive canary client secret through CompleteOAuth (success) and
// through a forced token-endpoint failure, asserting the secret never
// appears in the returned identity, the stored credentials, or any
// error message.
func TestAntigravityAdapter_Canary_ClientSecretNeverInIdentityOrError(t *testing.T) {
	// Deliberately no English words (e.g. "antigravity", "secret",
	// "token") in this value — those substrings legitimately appear in
	// this package's own error message text, which would make
	// assertNoKeyFragment's window scan false-positive on the structure
	// rather than a real leak.
	const canarySecret = "CANARY-9f3Kx2Qw8pLm0Zt7Vb4Nr1Hy6Dc5Ea-Ws3Rq8Ln"

	tokenProbe := &fixtureAntigravityTokenProbe{}
	adapter := NewAntigravityAdapter("client-id", canarySecret, tokenProbe.probe,
		fixtureAntigravityUserInfoProbe("owner@example.com"),
		fixtureAntigravityLoadCodeAssistProbe("proj-123", "tier-free", "Free"),
	)

	identity, creds, err := adapter.CompleteOAuth(context.Background(), "auth-code", "verifier", "http://127.0.0.1/callback")
	if err != nil {
		t.Fatalf("CompleteOAuth: %v", err)
	}
	assertNoKeyFragment(t, identity.ExternalID, canarySecret, "identity.ExternalID")
	assertNoKeyFragment(t, identity.Email, canarySecret, "identity.Email")
	assertNoKeyFragment(t, identity.Plan, canarySecret, "identity.Plan")
	assertNoKeyFragment(t, creds.Value, canarySecret, "StoredCredentials.Value")
	// The Evidence map is part of the reported identity too — a secret must
	// never leak through it either (it is not otherwise covered above).
	for k, v := range identity.Evidence {
		assertNoKeyFragment(t, fmt.Sprintf("%v", v), canarySecret, "identity.Evidence["+k+"]")
	}

	// Force a token-endpoint failure and prove the secret never leaks
	// into the resulting error text either.
	failingAdapter := NewAntigravityAdapter("client-id", canarySecret,
		func(_ context.Context, _ string, _ url.Values) ([]byte, error) {
			return nil, errors.New("simulated transport failure")
		}, nil, nil,
	)
	_, _, err = failingAdapter.CompleteOAuth(context.Background(), "auth-code", "verifier", "http://127.0.0.1/callback")
	if err == nil {
		t.Fatalf("CompleteOAuth with a failing token probe = nil error, want an error")
	}
	assertNoKeyFragment(t, err.Error(), canarySecret, "CompleteOAuth error text")
}
