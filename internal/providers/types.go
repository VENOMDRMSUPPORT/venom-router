package providers

// ProviderID identifies a provider by its slug (e.g. "openai",
// "anthropic"). This is the canonical definition; internal/execution
// currently carries its own minimal placeholder of the same name pending
// a later unification (see the package doc comment).
type ProviderID string

// StoredCredentials is the resolved credential material an adapter
// operates on. Its full shape (API key vs. OAuth token, expiry, envelope
// reference, etc.) belongs to internal/secrets; this is a minimal typed
// placeholder so adapter signatures are not stringly typed. Refining it
// is a later unit's concern, not this one's.
type StoredCredentials struct {
	Value string
}

// IdentityResult is the identity of a connected account as the provider
// reports it (03 §1).
type IdentityResult struct {
	ExternalID string // immutable provider ID when available
	Email      string
	Plan       string
	Funding    string         // free | paid | unknown (provider_evidence only)
	Evidence   map[string]any // sanitized before storage
}

// HealthFailure is a minimal, providers-local failure envelope for
// HealthObservation.Failure. 03 §1 documents this field as populated
// "from the InferenceTransport taxonomy" (internal/execution.TypedFailure)
// but this package must not import internal/execution (see the package
// doc comment), so this type stands in for that taxonomy until a later
// unit unifies them.
type HealthFailure struct {
	Class       string // high-level failure category, mirrors execution.FailureClass's vocabulary
	Retryable   bool
	SafeMessage string // user-safe description, never raw provider text
	Evidence    map[string]any
}

// HealthObservation is returned by the standalone HealthAdapter (03 §1).
type HealthObservation struct {
	Status             string // healthy | degraded | unreachable (unreachable maps to account health_state "unavailable")
	Scope              string // account | offering
	CredentialValid    bool
	TransportReachable bool
	CheckedAt          int64
	ExpiresAt          *int64
	Failure            *HealthFailure // nil when healthy
	Evidence           map[string]any
}

// DiscoveredModel is one model reported by a ModelDiscoveryAdapter (03 §1).
type DiscoveredModel struct {
	ProviderModelID string
	DisplayName     string
	ContextLength   *int // nil = unknown (never 0-as-unknown)
	MaxInputTokens  *int
	MaxOutputTokens *int
	Capabilities    []string // only from explicit provider fields
	Pricing         map[string]any
	Evidence        map[string]any
}

// QuotaWindow is one concurrently-tracked provider-evidence budget
// dimension (03 §1 / 02 §3). A provider may report several at once.
type QuotaWindow struct {
	Unit            string // requests | input_tokens | output_tokens | tokens | credits | balance | percent
	WindowType      string // rolling_5h | rolling_7d | rpm | tpm | balance | provider window key
	WindowKey       string // provider-native window identifier when supplied ("" otherwise)
	DurationSeconds *int   // nil for non-time-boxed windows (balance)
	Used            *float64
	Remaining       *float64
	Total           *float64
	ResetAt         *int64
	Confidence      float64
	Evidence        map[string]any
}

// QuotaResult is the set of provider-evidence quota windows returned by a
// QuotaAdapter. An empty Windows slice means "no provider quota evidence"
// — never "unlimited" (03 §1).
type QuotaResult struct {
	Windows []QuotaWindow
}
