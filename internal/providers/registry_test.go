package providers

import (
	"context"
	"testing"
)

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
		ID:       "openai",
		AuthMode: AuthModeAPIKey,
		APIKey:   fakeAPIKeyAdapter{},
		Health:   fakeHealthAdapter{},
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
		ID:       "chatgpt",
		AuthMode: AuthModeOAuth,
		OAuth:    fakeOAuthAdapter{},
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
	def := Definition{ID: "dup", AuthMode: AuthModeAPIKey, APIKey: fakeAPIKeyAdapter{}}

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
