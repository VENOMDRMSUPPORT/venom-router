package providers

import "context"

// HealthAdapter checks account/offering health. It works with any account
// type (API key, OAuth, custom) because health is independent of the
// credential mechanism (03 §1).
type HealthAdapter interface {
	CheckAccountHealth(ctx context.Context, creds StoredCredentials) (HealthObservation, error)
	CheckOfferingHealth(ctx context.Context, creds StoredCredentials, providerModelID string) (HealthObservation, error)
}

// APIKeyAdapter is the primary auth adapter for API-key providers:
// credential only, no health responsibility.
type APIKeyAdapter interface {
	// ConnectAPIKey validates the key authentically (not merely that the
	// host is up) and returns the connected identity plus credentials to
	// store.
	ConnectAPIKey(ctx context.Context, key string) (IdentityResult, StoredCredentials, error)
}

// OAuthAdapter is the primary auth adapter for OAuth providers: credential
// only, no health responsibility. A provider registers APIKeyAdapter XOR
// OAuthAdapter, never both.
type OAuthAdapter interface {
	BeginOAuth(ctx context.Context, redirectURI, state, pkceChallenge string) (authorizeURL string, err error)
	CompleteOAuth(ctx context.Context, code, pkceVerifier, redirectURI string) (IdentityResult, StoredCredentials, error)
	RefreshCredentials(ctx context.Context, creds StoredCredentials) (StoredCredentials, error)
}

// ModelDiscoveryAdapter is an optional capability, dispatched by the
// Registry only for providers that register it.
type ModelDiscoveryAdapter interface {
	DiscoverModels(ctx context.Context, creds StoredCredentials) ([]DiscoveredModel, error)
}

// QuotaAdapter is an optional capability, dispatched by the Registry only
// for providers that register it.
type QuotaAdapter interface {
	FetchQuota(ctx context.Context, creds StoredCredentials) (QuotaResult, error)
}

// IdentityAdapter is an optional capability, dispatched by the Registry
// only for providers that register it.
type IdentityAdapter interface {
	FetchIdentity(ctx context.Context, creds StoredCredentials) (IdentityResult, error)
}
