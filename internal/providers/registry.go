package providers

import (
	"fmt"
	"sort"
)

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
	ID       ProviderID
	AuthMode AuthMode
	// Transport is the execution transport kind this provider uses (01 §4.4:
	// transport selection is declared in the provider catalog). It must be
	// one of the five closed TransportKind values; empty or unknown is
	// rejected by Register.
	Transport TransportKind
	// WireSchema is the SECOND catalog-declared dimension native_oauth needs
	// (P7-EXEC-001 part 2): the wire PROTOCOL a native_oauth provider speaks,
	// since one OAuth-bearer transport serves several differing schemas and
	// selection may never be a slug switch. Register enforces exactly one rule:
	// a native_oauth Definition MUST carry a valid WireSchema; any other
	// transport MUST leave it empty. This is a bounded additive unfreeze of the
	// frozen contract, mirroring the one P4-EXEC-001 made when it added Transport.
	WireSchema WireSchema
	APIKey     APIKeyAdapter         // set iff AuthMode == AuthModeAPIKey
	OAuth      OAuthAdapter          // set iff AuthMode == AuthModeOAuth
	Health     HealthAdapter         // optional
	Discovery  ModelDiscoveryAdapter // optional
	Quota      QuotaAdapter          // optional
	Identity   IdentityAdapter       // optional
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

	if _, err := ParseTransportKind(string(def.Transport)); err != nil {
		return fmt.Errorf("providers: register %s: %w", def.ID, err)
	}

	// WireSchema is required for and exclusive to native_oauth (P7-EXEC-001
	// part 2): that one transport serves several wire schemas, so a native_oauth
	// provider that declares none cannot be dispatched (fail closed), and a
	// non-native_oauth provider declaring one is a contradiction.
	if def.Transport == TransportKindNativeOAuth {
		if _, err := ParseWireSchema(string(def.WireSchema)); err != nil {
			return fmt.Errorf("providers: register %s: native_oauth requires a wire schema: %w", def.ID, err)
		}
	} else if def.WireSchema != "" {
		return fmt.Errorf("providers: register %s: transport %q must not declare a wire schema (only native_oauth may)", def.ID, def.Transport)
	}

	r.defs[def.ID] = def
	return nil
}

// Definition returns the full Definition registered for id, or ok=false if
// id is unknown. Used by httpapi's transport resolver.
func (r *Registry) Definition(id ProviderID) (Definition, bool) {
	def, found := r.defs[id]
	return def, found
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

// IDs returns every registered provider id, sorted, so two registries can be
// compared and a composition can be asserted against.
func (r *Registry) IDs() []ProviderID {
	out := make([]ProviderID, 0, len(r.defs))
	for id := range r.defs {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
