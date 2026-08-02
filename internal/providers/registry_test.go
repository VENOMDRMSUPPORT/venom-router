package providers

import (
	"context"
	"net/url"
	"testing"
)

// Stub probe functions for provider registration tests.
func stubAntigravityTokenProbe(_ context.Context, _ string, _ url.Values) ([]byte, error) {
	return []byte(`{"access_token":"tok","refresh_token":"ref","expires_in":3600}`), nil
}

func stubAntigravityGetProbe(_ context.Context, _, _ string) ([]byte, error) {
	return []byte(`{"email":"x@example.com"}`), nil
}

func stubAntigravityPostProbe(_ context.Context, _, _ string, _ []byte) ([]byte, error) {
	return []byte(`{"cloudaicompanionProject":"p","currentTier":{"id":"1","name":"Free"}}`), nil
}

type fakeAPIKeyAdapter struct{}

func (fakeAPIKeyAdapter) ConnectAPIKey(ctx context.Context, key string) (IdentityResult, StoredCredentials, error) {
	return IdentityResult{}, StoredCredentials{}, nil
}

type fakeOAuthAdapter struct{}

func (fakeOAuthAdapter) BeginOAuth(ctx context.Context, redirectURI, state, pkceChallenge string) (string, error) {
	return "", nil
}

func (fakeOAuthAdapter) CompleteOAuth(ctx context.Context, code, pkceVerifier, redirectURI string) (IdentityResult, StoredCredentials, error) {
	return IdentityResult{}, StoredCredentials{}, nil
}

func (fakeOAuthAdapter) RefreshCredentials(ctx context.Context, creds StoredCredentials) (StoredCredentials, error) {
	return StoredCredentials{}, nil
}

type fakeHealthAdapter struct{}

func (fakeHealthAdapter) CheckAccountHealth(ctx context.Context, creds StoredCredentials) (HealthObservation, error) {
	return HealthObservation{}, nil
}

func (fakeHealthAdapter) CheckOfferingHealth(ctx context.Context, creds StoredCredentials, providerModelID string) (HealthObservation, error) {
	return HealthObservation{}, nil
}

type fakeQuotaAdapter struct{}

func (fakeQuotaAdapter) FetchQuota(ctx context.Context, creds StoredCredentials) (QuotaResult, error) {
	return QuotaResult{}, nil
}

func TestRegistry_DispatchByCapability(t *testing.T) {
	reg := NewRegistry()

	if err := reg.Register(Definition{
		ID:        "openai",
		AuthMode:  AuthModeAPIKey,
		Transport: TransportKindOpenAICompatible,
		APIKey:    fakeAPIKeyAdapter{},
		Health:    fakeHealthAdapter{},
	}); err != nil {
		t.Fatalf("Register(openai): %v", err)
	}

	if _, ok := reg.APIKeyAdapter("openai"); !ok {
		t.Fatalf("APIKeyAdapter(openai) ok = false, want true (registered)")
	}
	if _, ok := reg.HealthAdapter("openai"); !ok {
		t.Fatalf("HealthAdapter(openai) ok = false, want true (registered)")
	}
	if _, ok := reg.OAuthAdapter("openai"); ok {
		t.Fatalf("OAuthAdapter(openai) ok = true, want false (openai registers api_key, not oauth2)")
	}
	if _, ok := reg.QuotaAdapter("openai"); ok {
		t.Fatalf("QuotaAdapter(openai) ok = true, want false (no quota adapter was registered)")
	}
	if _, ok := reg.ModelDiscoveryAdapter("openai"); ok {
		t.Fatalf("ModelDiscoveryAdapter(openai) ok = true, want false (none registered)")
	}
	if _, ok := reg.IdentityAdapter("openai"); ok {
		t.Fatalf("IdentityAdapter(openai) ok = true, want false (none registered)")
	}
}

func TestRegistry_UnknownProvider_NeverPanics(t *testing.T) {
	reg := NewRegistry()

	if _, ok := reg.APIKeyAdapter("does-not-exist"); ok {
		t.Fatalf("APIKeyAdapter(unknown) ok = true, want false")
	}
	if _, ok := reg.OAuthAdapter("does-not-exist"); ok {
		t.Fatalf("OAuthAdapter(unknown) ok = true, want false")
	}
	if _, ok := reg.HealthAdapter("does-not-exist"); ok {
		t.Fatalf("HealthAdapter(unknown) ok = true, want false")
	}
	if _, ok := reg.QuotaAdapter("does-not-exist"); ok {
		t.Fatalf("QuotaAdapter(unknown) ok = true, want false")
	}
	if _, ok := reg.ModelDiscoveryAdapter("does-not-exist"); ok {
		t.Fatalf("ModelDiscoveryAdapter(unknown) ok = true, want false")
	}
	if _, ok := reg.IdentityAdapter("does-not-exist"); ok {
		t.Fatalf("IdentityAdapter(unknown) ok = true, want false")
	}
}

func TestRegistry_OAuthProvider(t *testing.T) {
	reg := NewRegistry()

	if err := reg.Register(Definition{
		ID:        "chatgpt",
		AuthMode:  AuthModeOAuth,
		Transport: TransportKindOpenAICompatible,
		OAuth:     fakeOAuthAdapter{},
	}); err != nil {
		t.Fatalf("Register(chatgpt): %v", err)
	}

	if _, ok := reg.OAuthAdapter("chatgpt"); !ok {
		t.Fatalf("OAuthAdapter(chatgpt) ok = false, want true (registered)")
	}
	if _, ok := reg.APIKeyAdapter("chatgpt"); ok {
		t.Fatalf("APIKeyAdapter(chatgpt) ok = true, want false (chatgpt registers oauth2, not api_key)")
	}
}

func TestRegistry_Register_RejectsBothPrimaryAdapters(t *testing.T) {
	reg := NewRegistry()

	err := reg.Register(Definition{
		ID:       "bad",
		AuthMode: AuthModeAPIKey,
		APIKey:   fakeAPIKeyAdapter{},
		OAuth:    fakeOAuthAdapter{},
	})
	if err == nil {
		t.Fatalf("Register succeeded with both APIKey and OAuth set, want rejection")
	}
}

func TestRegistry_Register_RejectsMissingPrimaryAdapter(t *testing.T) {
	reg := NewRegistry()

	err := reg.Register(Definition{
		ID:       "bad",
		AuthMode: AuthModeAPIKey,
		// APIKey deliberately left nil.
	})
	if err == nil {
		t.Fatalf("Register succeeded with AuthMode api_key but no APIKey adapter, want rejection")
	}
}

func TestRegistry_Register_RejectsDuplicateID(t *testing.T) {
	reg := NewRegistry()
	def := Definition{ID: "dup", AuthMode: AuthModeAPIKey, Transport: TransportKindOpenAICompatible, APIKey: fakeAPIKeyAdapter{}}

	if err := reg.Register(def); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := reg.Register(def); err == nil {
		t.Fatalf("second Register with the same ID succeeded, want rejection")
	}
}

func TestRegistry_Register_RejectsEmptyID(t *testing.T) {
	reg := NewRegistry()

	err := reg.Register(Definition{AuthMode: AuthModeAPIKey, APIKey: fakeAPIKeyAdapter{}})
	if err == nil {
		t.Fatalf("Register succeeded with empty ID, want rejection")
	}
}

func TestRegistry_Register_RejectsEmptyTransport(t *testing.T) {
	reg := NewRegistry()

	err := reg.Register(Definition{ID: "no-transport", AuthMode: AuthModeAPIKey, APIKey: fakeAPIKeyAdapter{}})
	if err == nil {
		t.Fatalf("Register succeeded with empty Transport, want rejection")
	}
}

func TestRegistry_Register_RejectsUnknownTransport(t *testing.T) {
	reg := NewRegistry()

	err := reg.Register(Definition{ID: "bad-transport", AuthMode: AuthModeAPIKey, APIKey: fakeAPIKeyAdapter{}, Transport: TransportKind("not_a_real_kind")})
	if err == nil {
		t.Fatalf("Register succeeded with unknown Transport, want rejection")
	}
}

func TestRegistry_Register_AcceptsValidTransport(t *testing.T) {
	reg := NewRegistry()

	kinds := []TransportKind{
		TransportKindBifrost,
		TransportKindNativeAPI,
		TransportKindNativeOAuth,
		TransportKindOpenAICompatible,
		TransportKindCustom,
	}
	for i, k := range kinds {
		id := ProviderID("prov-" + string(k))
		var authMode AuthMode
		var apiKey APIKeyAdapter
		var oauth OAuthAdapter
		if i%2 == 0 {
			authMode = AuthModeAPIKey
			apiKey = fakeAPIKeyAdapter{}
		} else {
			authMode = AuthModeOAuth
			oauth = fakeOAuthAdapter{}
		}
		err := reg.Register(Definition{ID: id, AuthMode: authMode, Transport: k, APIKey: apiKey, OAuth: oauth})
		if err != nil {
			t.Errorf("Register(transport=%q) error = %v, want nil", k, err)
		}
	}
}

func TestRegistry_Definition_LookupAndMissingProvider(t *testing.T) {
	reg := NewRegistry()

	if err := reg.Register(Definition{
		ID:        "p1",
		AuthMode:  AuthModeAPIKey,
		Transport: TransportKindOpenAICompatible,
		APIKey:    fakeAPIKeyAdapter{},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	def, ok := reg.Definition("p1")
	if !ok {
		t.Fatalf("Definition(p1) ok = false, want true")
	}
	if def.Transport != TransportKindOpenAICompatible {
		t.Fatalf("Definition(p1).Transport = %q, want %q", def.Transport, TransportKindOpenAICompatible)
	}

	if _, ok := reg.Definition("does-not-exist"); ok {
		t.Fatalf("Definition(unknown) ok = true, want false")
	}
}

func TestRegistrations_ShippedProviders_HaveCorrectTransports(t *testing.T) {
	reg := NewRegistry()
	chatProbe := func(_ context.Context, _, _ string) (int, error) { return 200, nil }
	modelsProbe := func(_ context.Context, _, _ string) ([]byte, error) { return []byte(`{"data":[]}`), nil }
	modelsDevProbe := func(_ context.Context) ([]byte, error) { return []byte(`{"opencode":{"models":{}}}`), nil }
	if err := RegisterOpenCodeZen(reg, chatProbe, modelsProbe, modelsDevProbe, nil); err != nil {
		t.Fatalf("RegisterOpenCodeZen: %v", err)
	}

	def, ok := reg.Definition(OpenCodeZenID)
	if !ok {
		t.Fatalf("Definition(%q) ok = false", OpenCodeZenID)
	}
	if def.Transport != TransportKindOpenAICompatible {
		t.Fatalf("opencode-zen transport = %q, want %q", def.Transport, TransportKindOpenAICompatible)
	}
}

func TestRegistrations_Antigravity_HasNativeOAuthTransport(t *testing.T) {
	reg := NewRegistry()

	if err := RegisterAntigravity(reg, "cid", "csecret",
		stubAntigravityTokenProbe,
		stubAntigravityGetProbe,
		stubAntigravityPostProbe,
	); err != nil {
		t.Fatalf("RegisterAntigravity: %v", err)
	}

	def, ok := reg.Definition(AntigravityID)
	if !ok {
		t.Fatalf("Definition(%q) ok = false", AntigravityID)
	}
	if def.Transport != TransportKindNativeOAuth {
		t.Fatalf("antigravity transport = %q, want %q", def.Transport, TransportKindNativeOAuth)
	}
}
