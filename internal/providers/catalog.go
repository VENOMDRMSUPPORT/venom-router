package providers

import "sort"

// CatalogAuthMode identifies the auth mechanism a catalog entry
// describes. This is a descriptor-level type distinct from the frozen
// Registry's AuthMode (which only ever has api_key/oauth2, since it
// gates a live adapter registration): the catalog also needs to
// describe the custom OpenAI-compatible path, which has no frozen
// adapter of its own.
type CatalogAuthMode string

const (
	CatalogAuthAPIKey    CatalogAuthMode = "api_key"
	CatalogAuthOAuth     CatalogAuthMode = "oauth2"
	CatalogAuthCustomOAI CatalogAuthMode = "custom_openai"
)

// FundingMode mirrors the M2 providers.funding_mode column vocabulary
// (02 §2). This is a providers-local duplicate, not a re-export of
// DOM-002's funding enums — providers must not import accounts/domain
// (01 §3 layering).
type FundingMode string

const (
	FundingModeFixed            FundingMode = "fixed"
	FundingModeOwnerPolicy      FundingMode = "owner_policy"
	FundingModeProviderEvidence FundingMode = "provider_evidence"
	FundingModeEvidenceRequired FundingMode = "evidence_required"
)

// Funding mirrors the M2 providers.funding_fixed column vocabulary (02
// §2): free | paid | unknown. Only meaningful when Mode == FundingModeFixed.
type Funding string

const (
	FundingFree    Funding = "free"
	FundingPaid    Funding = "paid"
	FundingUnknown Funding = "unknown"
)

// FundingPolicy is a catalog entry's declared funding classification —
// the M2 providers table's funding_mode/funding_fixed/funding_locked/
// funding_non_expiring columns, as a value type.
type FundingPolicy struct {
	Mode FundingMode
	// Fixed is set only when Mode == FundingModeFixed; "" otherwise.
	Fixed       Funding
	Locked      bool
	NonExpiring bool
}

// CatalogEntry is one provider's descriptive metadata — display
// strings, auth mechanism, funding policy, and the environment
// variables a confidential-client OAuth provider requires. It carries
// no adapter and no behavior; concrete adapters are registered
// separately into a Registry by later units (PROV-005/PROV-007), and
// capabilities are derived from whatever is registered there (see
// DerivedCapabilities), never stored on the entry itself.
type CatalogEntry struct {
	ID          ProviderID
	DisplayName string
	Description string
	AuthMode    CatalogAuthMode
	BaseURL     string
	Funding     FundingPolicy
	// RequiredEnv lists environment variable NAMES a confidential OAuth
	// client needs (e.g. antigravity's client secret). Never a value —
	// only names are ever surfaced to callers.
	RequiredEnv []string
	// StableID documents which field the provider uses as its
	// multi-account-safe stable external identity (03 §4), for
	// reference/display only.
	StableID string
}

// BuiltinCatalog returns the 11 built-in provider descriptors (03 §3/
// §4), in the order documented there. Values (base URL, auth mode,
// funding classification, required env) are taken verbatim from the
// catalog doc — this function does not compute or infer any of them.
func BuiltinCatalog() []CatalogEntry {
	return []CatalogEntry{
		{
			ID:          "opencode-zen",
			DisplayName: "OpenCode Zen",
			Description: "API-key provider; free-only model catalog intersected with models.dev.",
			AuthMode:    CatalogAuthAPIKey,
			BaseURL:     "https://opencode.ai/zen",
			Funding:     FundingPolicy{Mode: FundingModeOwnerPolicy},
			StableID:    "fingerprint",
		},
		{
			ID:          "agnes-ai",
			DisplayName: "Agnes AI",
			Description: "API-key provider; funding cannot be assumed free or paid.",
			AuthMode:    CatalogAuthAPIKey,
			BaseURL:     "https://apihub.agnes-ai.com/v1",
			Funding:     FundingPolicy{Mode: FundingModeEvidenceRequired},
			StableID:    "fingerprint",
		},
		{
			ID:          "gemini-cli",
			DisplayName: "Gemini CLI",
			Description: "API-key provider using Google's schema (x-goog-api-key), not OpenAI's.",
			AuthMode:    CatalogAuthAPIKey,
			BaseURL:     "https://generativelanguage.googleapis.com",
			Funding:     FundingPolicy{Mode: FundingModeEvidenceRequired},
			StableID:    "fingerprint",
		},
		{
			ID:          "ollama-cloud",
			DisplayName: "Ollama Cloud",
			Description: "API-key provider with an immutable account ID from /api/me.",
			AuthMode:    CatalogAuthAPIKey,
			BaseURL:     "https://ollama.com/v1",
			Funding:     FundingPolicy{Mode: FundingModeProviderEvidence},
			StableID:    "account.ID",
		},
		{
			ID:          "nvidia-nim",
			DisplayName: "NVIDIA NIM",
			Description: "API-key provider; no documented quota API.",
			AuthMode:    CatalogAuthAPIKey,
			BaseURL:     "https://integrate.api.nvidia.com/v1",
			Funding:     FundingPolicy{Mode: FundingModeEvidenceRequired},
			StableID:    "fingerprint",
		},
		{
			ID:          "antigravity",
			DisplayName: "Antigravity",
			Description: "OAuth2 confidential client; requires an owner-supplied client secret.",
			AuthMode:    CatalogAuthOAuth,
			BaseURL:     "https://cloudcode-pa.googleapis.com",
			Funding:     FundingPolicy{Mode: FundingModeProviderEvidence},
			RequiredEnv: []string{"VENOM_ANTIGRAVITY_CLIENT_SECRET", "VENOM_ANTIGRAVITY_CLIENT_ID"},
			StableID:    "email+project_id",
		},
		{
			ID:          "claude-code",
			DisplayName: "Claude Code",
			Description: "OAuth2 (PKCE); stable external ID is the account UUID.",
			AuthMode:    CatalogAuthOAuth,
			BaseURL:     "https://api.anthropic.com",
			Funding:     FundingPolicy{Mode: FundingModeProviderEvidence},
			StableID:    "account.uuid",
		},
		{
			ID:          "codex",
			DisplayName: "Codex (OpenAI)",
			Description: "OAuth2 (PKCE, fixed redirect); stable external ID is the ChatGPT account ID.",
			AuthMode:    CatalogAuthOAuth,
			BaseURL:     "https://chatgpt.com/backend-api/codex/responses",
			Funding:     FundingPolicy{Mode: FundingModeProviderEvidence},
			StableID:    "chatgpt_account_id",
		},
		{
			ID:          "github-copilot",
			DisplayName: "GitHub Copilot",
			Description: "OAuth2 (two-token exchange: GitHub token then Copilot token).",
			AuthMode:    CatalogAuthOAuth,
			BaseURL:     "https://api.githubcopilot.com",
			Funding:     FundingPolicy{Mode: FundingModeProviderEvidence},
			StableID:    "user.id",
		},
		{
			ID:          "clinepass",
			DisplayName: "ClinePass",
			Description: "OAuth extension flow; funding is a locked, non-expiring paid balance.",
			AuthMode:    CatalogAuthOAuth,
			BaseURL:     "https://api.cline.bot",
			Funding:     FundingPolicy{Mode: FundingModeFixed, Fixed: FundingPaid, Locked: true},
			StableID:    "clineUserId",
		},
		{
			ID:          "xai",
			DisplayName: "xAI (Grok)",
			Description: "OAuth2 (PKCE, Grok Build); stable external ID is the JWT subject.",
			AuthMode:    CatalogAuthOAuth,
			BaseURL:     "https://api.x.ai/v1",
			Funding:     FundingPolicy{Mode: FundingModeProviderEvidence},
			StableID:    "sub",
		},
	}
}

// CustomProviderID is the descriptor ID used for the generic
// OpenAI-compatible custom path (03 §3 "Custom"/02 §2c). It is a path
// template, not a connectable built-in — it is never seeded into the
// providers table (each configured custom account gets its own row,
// under enrollment, not this unit's concern).
const CustomProviderID ProviderID = "custom"

// CustomPathDescriptor returns the generic OpenAI-compatible custom
// provider descriptor: base URL, key, headers, and funding are all
// supplied per-account by the owner at enrollment time, so this
// descriptor carries no fixed BaseURL and funding defaults to
// evidence_required (nothing can be assumed about an arbitrary
// OpenAI-compatible endpoint until the owner classifies it).
func CustomPathDescriptor() CatalogEntry {
	return CatalogEntry{
		ID:          CustomProviderID,
		DisplayName: "Custom (OpenAI-compatible)",
		Description: "Generic OpenAI-compatible endpoint; base URL, key, and funding are supplied per account.",
		AuthMode:    CatalogAuthCustomOAI,
		Funding:     FundingPolicy{Mode: FundingModeEvidenceRequired},
		StableID:    "fingerprint",
	}
}

// DerivedCapabilities reports which capability strings id currently
// has, based ONLY on which typed adapters are registered for it in
// reg — never a hard-coded per-slug list. An unregistered id, or a nil
// reg, yields an empty (non-nil-vs-nil is not asserted; callers should
// only rely on len()==0) set. This phase no provider has a registered
// adapter, so every catalog entry correctly reports zero capabilities
// — that is the derived truth, not a bug: live adapters land in
// PROV-005/PROV-007.
func DerivedCapabilities(reg *Registry, id ProviderID) []string {
	if reg == nil {
		return nil
	}

	var caps []string
	if _, ok := reg.APIKeyAdapter(id); ok {
		caps = append(caps, "api_key")
	}
	if _, ok := reg.OAuthAdapter(id); ok {
		caps = append(caps, "oauth2")
	}
	if _, ok := reg.HealthAdapter(id); ok {
		caps = append(caps, "health")
	}
	if _, ok := reg.ModelDiscoveryAdapter(id); ok {
		caps = append(caps, "discovery")
	}
	if _, ok := reg.QuotaAdapter(id); ok {
		caps = append(caps, "quota")
	}
	if _, ok := reg.IdentityAdapter(id); ok {
		caps = append(caps, "identity")
	}
	if _, ok := reg.Definition(id); ok {
		def, _ := reg.Definition(id)
		if def.OAuth != nil {
			if mc, ok := def.OAuth.(RequiresManualCode); ok && mc.RequiresManualCode() {
				caps = append(caps, "manual_code")
			}
			if om, ok := def.OAuth.(OmitStateFromCallback); ok && om.OmitStateFromCallback() {
				caps = append(caps, "omit_state_callback")
			}
		}
	}

	sort.Strings(caps)
	return caps
}
