package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// AgnesAIID is the catalog slug this adapter registers under.
const AgnesAIID ProviderID = "agnes-ai"

// AgnesAIBaseURL is agnes-ai's OpenAI-compatible API base (03 §3). It MUST
// equal the BuiltinCatalog entry — asserted by a test.
const AgnesAIBaseURL = "https://apihub.agnes-ai.com/v1"

// agnesSyntheticPlan is the display-only plan label agnes-ai has no identity
// endpoint to source (03 §3: "synthetic Free"). It is NOT funding evidence:
// funding stays "" (evidence_required) regardless of this label.
const agnesSyntheticPlan = "Free"

// agnesCapabilityVocab maps the capability labels agnes-ai's own /v1/models
// response may declare onto Venom's fixed operation vocabulary (internal/models
// Operations). This is label NORMALIZATION of an EXPLICIT provider declaration
// — never inference from a model name or description (§2). A label absent from
// this table is dropped (fail closed): an unknown provider label is not our
// vocabulary's problem. It is a map (not a switch) so the no-slug-switch guard
// is satisfied by construction.
var agnesCapabilityVocab = map[string]string{
	"tools":             "tools",
	"tool_call":         "tools",
	"tool_use":          "tools",
	"function_calling":  "tools",
	"vision":            "vision",
	"image_input":       "vision",
	"structured_output": "structured_output",
	"json_mode":         "structured_output",
}

// AgnesAIAdapter implements APIKeyAdapter, ModelDiscoveryAdapter and
// HealthAdapter for agnes-ai (03 §3). It has no identity endpoint (identity is
// the key fingerprint + a synthetic Free plan), funding is unknown
// (evidence_required — the Free label is not evidence), and it registers NO
// quota adapter (none exists). Validation and health are the authentic
// two-step chat probe (resolve a real model id, then a max_tokens:1 chat call);
// discovery reads the provider's own /v1/models (there is no models.dev entry
// for agnes, so the response itself is the only fact source).
type AgnesAIAdapter struct {
	chatProbe   ChatProbe
	modelsProbe ModelsProbe
}

// NewAgnesAIAdapter builds the adapter over the injected chat/models probes.
func NewAgnesAIAdapter(chatProbe ChatProbe, modelsProbe ModelsProbe) *AgnesAIAdapter {
	return &AgnesAIAdapter{chatProbe: chatProbe, modelsProbe: modelsProbe}
}

// ConnectAPIKey validates key via the authentic chat probe and, on success,
// reports identity as the key's fingerprint with a synthetic Free plan. Funding
// stays "" (evidence_required). key is never logged.
func (a *AgnesAIAdapter) ConnectAPIKey(ctx context.Context, key string) (IdentityResult, StoredCredentials, error) {
	switch ValidateAPIKey(ctx, a.chatProbe, AgnesAIBaseURL, key) {
	case ValidationValid:
		normalized := NormalizeAPIKey(key)
		return IdentityResult{ExternalID: fingerprintAPIKey(normalized), Plan: agnesSyntheticPlan}, StoredCredentials{Value: normalized}, nil
	case ValidationInvalid:
		return IdentityResult{}, StoredCredentials{}, ErrInvalidCredential
	default:
		return IdentityResult{}, StoredCredentials{}, ErrProviderUnavailable
	}
}

// agnesModelEntry is the subset of one agnes-ai /v1/models row this adapter
// reads. Context length is spelled three different ways in the wild
// (context_window / context_length / max_model_len); all three are read.
// Capabilities is RAW because the provider sends it as EITHER a string array
// OR an object of booleans.
type agnesModelEntry struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	DisplayName   string          `json:"display_name"`
	ContextWindow *int            `json:"context_window"`
	ContextLength *int            `json:"context_length"`
	MaxModelLen   *int            `json:"max_model_len"`
	Capabilities  json.RawMessage `json:"capabilities"`
}

type agnesModelList struct {
	Data []agnesModelEntry `json:"data"`
}

// DiscoverModels reads GET /v1/models and maps each surviving row from its OWN
// explicit fields (there is no models.dev entry for agnes). Video models are
// dropped (03 §3). Every capability comes from the row's explicit capabilities
// field, normalized onto our vocabulary; nothing is inferred.
func (a *AgnesAIAdapter) DiscoverModels(ctx context.Context, creds StoredCredentials) ([]DiscoveredModel, error) {
	body, err := a.modelsProbe(ctx, AgnesAIBaseURL, creds.Value)
	if err != nil {
		return nil, fmt.Errorf("providers: agnes-ai discover models: %w", err)
	}
	var list agnesModelList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("providers: agnes-ai discover models: parse response: %w", err)
	}

	models := make([]DiscoveredModel, 0, len(list.Data))
	for _, e := range list.Data {
		if e.ID == "" {
			continue
		}
		labels, present := parseAgnesCapabilities(e.Capabilities)
		if agnesIsVideo(e.ID, labels, present) {
			continue
		}
		models = append(models, DiscoveredModel{
			ProviderModelID: e.ID,
			DisplayName:     agnesDisplayName(e),
			ContextLength:   agnesContextLength(e),
			Capabilities:    agnesCapabilities(labels),
		})
	}
	return models, nil
}

// agnesIsVideo decides whether a row is a video model, which 03 §3 drops.
// Preferred (explicit) route: the row's own capability field declares "video".
// ONLY when the row carries no capability field at all does it fall back to the
// id-shape rule the card mandates (`-video` suffix / `agnes-video`) — this is
// the SINGLE id-shape rule in the adapter, and it exists solely because the
// card orders the drop, not as a capability guess.
func agnesIsVideo(id string, labels []string, capabilitiesPresent bool) bool {
	if capabilitiesPresent {
		for _, l := range labels {
			if strings.EqualFold(l, "video") {
				return true
			}
		}
		return false
	}
	lower := strings.ToLower(id)
	return strings.HasSuffix(lower, "-video") || strings.Contains(lower, "agnes-video")
}

// parseAgnesCapabilities accepts the capabilities field in EITHER shape: a
// string array, or an object of booleans (only `true` entries count). It
// returns the declared labels and whether the field was present at all (an
// absent field means "no capability info", which changes how video is
// detected). Order within an object is not defined, but only membership is
// used downstream, so it does not matter.
func parseAgnesCapabilities(raw json.RawMessage) (labels []string, present bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, false
	}
	var asArray []string
	if err := json.Unmarshal(raw, &asArray); err == nil {
		return asArray, true
	}
	var asObject map[string]bool
	if err := json.Unmarshal(raw, &asObject); err == nil {
		out := make([]string, 0, len(asObject))
		for k, v := range asObject {
			if v {
				out = append(out, k)
			}
		}
		return out, true
	}
	// Present but an unrecognized shape: treat as present-with-no-labels so the
	// id-shape video fallback does NOT fire (the row did carry a field).
	return nil, true
}

// agnesCapabilities maps the declared labels onto our vocabulary. "chat" is
// always asserted (agnes is an OpenAI-compatible chat-completions gateway, so a
// listed model is a chat model — the same explicit grounding zen uses); every
// other capability is added only when the row's own label maps to a vocabulary
// token, and unknown labels are dropped.
func agnesCapabilities(labels []string) []string {
	caps := []string{"chat"}
	for _, l := range labels {
		if tok, ok := agnesCapabilityVocab[strings.ToLower(strings.TrimSpace(l))]; ok {
			caps = appendUniqueCap(caps, tok)
		}
	}
	return caps
}

// appendUniqueCap appends tok to caps only if it is not already present.
func appendUniqueCap(caps []string, tok string) []string {
	for _, c := range caps {
		if c == tok {
			return caps
		}
	}
	return append(caps, tok)
}

// agnesDisplayName prefers the row's display_name, then name, then the raw id —
// no brand-casing table.
func agnesDisplayName(e agnesModelEntry) string {
	if e.DisplayName != "" {
		return e.DisplayName
	}
	if e.Name != "" {
		return e.Name
	}
	return e.ID
}

// agnesContextLength returns the first positive of the three context-length
// spellings, or nil when none is present (never a fabricated 0).
func agnesContextLength(e agnesModelEntry) *int {
	for _, v := range []*int{e.ContextWindow, e.ContextLength, e.MaxModelLen} {
		if v != nil {
			return v
		}
	}
	return nil
}

// CheckAccountHealth implements HealthAdapter via the same authentic two-step
// chat probe ConnectAPIKey uses.
func (a *AgnesAIAdapter) CheckAccountHealth(ctx context.Context, creds StoredCredentials) (HealthObservation, error) {
	return a.checkHealth(ctx, creds, "account")
}

// CheckOfferingHealth reuses the account-level probe: agnes-ai has no per-model
// health endpoint.
func (a *AgnesAIAdapter) CheckOfferingHealth(ctx context.Context, creds StoredCredentials, _ string) (HealthObservation, error) {
	return a.checkHealth(ctx, creds, "offering")
}

func (a *AgnesAIAdapter) checkHealth(ctx context.Context, creds StoredCredentials, scope string) (HealthObservation, error) {
	switch ValidateAPIKey(ctx, a.chatProbe, AgnesAIBaseURL, creds.Value) {
	case ValidationValid:
		return HealthObservation{Status: "healthy", Scope: scope, CredentialValid: true, TransportReachable: true}, nil
	case ValidationInvalid:
		return HealthObservation{
			Status: "expired", Scope: scope, CredentialValid: false, TransportReachable: true,
			Failure: &HealthFailure{Class: "auth", Retryable: false, SafeMessage: "provider rejected the credential (401/403)"},
		}, nil
	default:
		return HealthObservation{
			Status: "unreachable", Scope: scope, CredentialValid: false, TransportReachable: false,
			Failure: &HealthFailure{Class: "unavailable", Retryable: true, SafeMessage: "provider unavailable or rate limited"},
		}, nil
	}
}

// RegisterAgnesAI registers the agnes-ai APIKey + Health + Discovery adapters
// into reg. It does NOT wire itself into any composition root — that is the
// caller's job (httpapi's registerAgnesAI).
func RegisterAgnesAI(reg *Registry, chatProbe ChatProbe, modelsProbe ModelsProbe) error {
	adapter := NewAgnesAIAdapter(chatProbe, modelsProbe)
	return reg.Register(Definition{
		ID:        AgnesAIID,
		AuthMode:  AuthModeAPIKey,
		Transport: TransportKindOpenAICompatible,
		APIKey:    adapter,
		Health:    adapter,
		Discovery: adapter,
	})
}
