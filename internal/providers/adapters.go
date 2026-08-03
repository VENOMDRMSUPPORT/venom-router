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

// OmitStateFromCallback is implemented by OAuth adapters whose provider's
// authorize redirect OMITS the `state` parameter (verified per-provider on
// live re-verification — clinepass is the proven case; legacy 2026-08-03).
// For these providers the OAuth enrollment service embeds the unguessable
// transaction id into the callback URL itself, and the callback resolves the
// transaction by id instead of by state hash. The transaction id is already
// this project's capability token for the OAuth status endpoint, so it carries
// the same unguessability guarantee the state nonce provides, without relying
// on the provider to echo it. Adapters that do NOT implement this interface
// (the default) keep the strict state-hash binding.
type OmitStateFromCallback interface {
	OmitStateFromCallback() bool
}

// RequiresManualCode is implemented by OAuth adapters whose provider's
// registered client NEVER redirects back to Venom: after the owner
// authorizes, the browser lands on a HOSTED page that displays a code to
// copy and paste back into the dashboard (claude-code is the proven case —
// its public client's only registered redirect_uri is Anthropic's hosted
// code page, live-verified 2026-08-03). The enrollment UI shows a paste
// field instead of the popup-and-poll flow when this is true, and the
// dashboard submits the pasted code to POST .../oauth/complete. Adapters
// that do NOT implement this interface (the default) use the browser
// redirect flow.
type RequiresManualCode interface {
	RequiresManualCode() bool
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
