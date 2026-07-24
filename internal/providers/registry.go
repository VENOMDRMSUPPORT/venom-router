package providers

import "fmt"

// AuthMode identifies which primary auth adapter a provider Definition
// registers. A provider has exactly one, never both (03 §1).
type AuthMode string

const (
	AuthModeAPIKey AuthMode = "api_key"
	AuthModeOAuth  AuthMode = "oauth2"
)

// Definition is one provider's registered adapter set: exactly one
// primary auth adapter (APIKey XOR OAuth, selected by AuthMode) plus any
// optional adapters. Concrete adapters are supplied by later units
// (PROV-002/005/007); this unit only freezes the registration shape.
type Definition struct {
	ID        ProviderID
	AuthMode  AuthMode
	APIKey    APIKeyAdapter         // set iff AuthMode == AuthModeAPIKey
	OAuth     OAuthAdapter          // set iff AuthMode == AuthModeOAuth
	Health    HealthAdapter         // optional
	Discovery ModelDiscoveryAdapter // optional
	Quota     QuotaAdapter          // optional
	Identity  IdentityAdapter       // optional
}

// Registry holds one Definition per provider and dispatches to adapters
// by typed capability lookup — never by a switch on the provider's slug
// string (01 §4.5 / 08 §8). A zero Registry is not ready for use; call
// NewRegistry.
type Registry struct {
	defs map[ProviderID]Definition
}

// NewRegistry returns an empty, ready-to-use Registry.
func NewRegistry() *Registry {
	return &Registry{defs: make(map[ProviderID]Definition)}
}

// Register adds def to the registry. It rejects a Definition with no ID,
// an AuthMode whose required primary adapter is missing, a Definition
// that sets both APIKey and OAuth, and re-registration of an already
// registered ID — never silently overwriting or guessing.
func (r *Registry) Register(def Definition) error {
	if def.ID == "" {
		return fmt.Errorf("providers: register: empty provider ID")
	}
	if _, exists := r.defs[def.ID]; exists {
		return fmt.Errorf("providers: register %s: already registered", def.ID)
	}

	if def.AuthMode != AuthModeAPIKey && def.AuthMode != AuthModeOAuth {
		return fmt.Errorf("providers: register %s: unknown AuthMode %q", def.ID, def.AuthMode)
	}
	if def.AuthMode == AuthModeAPIKey && def.APIKey == nil {
		return fmt.Errorf("providers: register %s: AuthMode %s requires an APIKey adapter", def.ID, def.AuthMode)
	}
	if def.AuthMode == AuthModeOAuth && def.OAuth == nil {
		return fmt.Errorf("providers: register %s: AuthMode %s requires an OAuth adapter", def.ID, def.AuthMode)
	}
	if def.APIKey != nil && def.OAuth != nil {
		return fmt.Errorf("providers: register %s: a provider registers APIKey XOR OAuth, never both", def.ID)
	}

	r.defs[def.ID] = def
	return nil
}

// APIKeyAdapter returns id's registered APIKeyAdapter, or ok=false if id
// is unknown or has no APIKey adapter registered — never a panic.
func (r *Registry) APIKeyAdapter(id ProviderID) (adapter APIKeyAdapter, ok bool) {
	def, found := r.defs[id]
	if !found || def.APIKey == nil {
		return nil, false
	}
	return def.APIKey, true
}

// OAuthAdapter returns id's registered OAuthAdapter, or ok=false if id is
// unknown or has no OAuth adapter registered.
func (r *Registry) OAuthAdapter(id ProviderID) (adapter OAuthAdapter, ok bool) {
	def, found := r.defs[id]
	if !found || def.OAuth == nil {
		return nil, false
	}
	return def.OAuth, true
}

// HealthAdapter returns id's registered HealthAdapter, or ok=false if id
// is unknown or has none registered (health is optional).
func (r *Registry) HealthAdapter(id ProviderID) (adapter HealthAdapter, ok bool) {
	def, found := r.defs[id]
	if !found || def.Health == nil {
		return nil, false
	}
	return def.Health, true
}

// ModelDiscoveryAdapter returns id's registered ModelDiscoveryAdapter, or
// ok=false if id is unknown or has none registered (discovery is optional).
func (r *Registry) ModelDiscoveryAdapter(id ProviderID) (adapter ModelDiscoveryAdapter, ok bool) {
	def, found := r.defs[id]
	if !found || def.Discovery == nil {
		return nil, false
	}
	return def.Discovery, true
}

// QuotaAdapter returns id's registered QuotaAdapter, or ok=false if id is
// unknown or has none registered (quota is optional).
func (r *Registry) QuotaAdapter(id ProviderID) (adapter QuotaAdapter, ok bool) {
	def, found := r.defs[id]
	if !found || def.Quota == nil {
		return nil, false
	}
	return def.Quota, true
}

// IdentityAdapter returns id's registered IdentityAdapter, or ok=false if
// id is unknown or has none registered (identity is optional).
func (r *Registry) IdentityAdapter(id ProviderID) (adapter IdentityAdapter, ok bool) {
	def, found := r.defs[id]
	if !found || def.Identity == nil {
		return nil, false
	}
	return def.Identity, true
}
