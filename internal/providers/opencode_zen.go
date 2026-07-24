package providers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// OpenCodeZenBaseURL is opencode-zen's fixed API base (03 §3).
const OpenCodeZenBaseURL = "https://opencode.ai/zen"

// OpenCodeZenID is the catalog slug this adapter registers under —
// must match PROV-002's BuiltinCatalog entry.
const OpenCodeZenID ProviderID = "opencode-zen"

// ErrInvalidCredential is returned by an APIKeyAdapter's ConnectAPIKey
// when the authentic-validation probe (PROV-004) classifies the key as
// genuinely invalid (401/403).
var ErrInvalidCredential = errors.New("providers: credential is invalid")

// ErrProviderUnavailable is returned when the validation probe could
// not reach the provider or got an ambiguous/retryable response (429,
// 5xx, or a transport error) — the key's validity is simply unknown,
// never treated as invalid.
var ErrProviderUnavailable = errors.New("providers: provider unavailable, try again later")

// ModelsProbe fetches the raw JSON body of the provider's model-listing
// endpoint. Like ChatProbe, this keeps internal/providers free of
// net/http (01 §3/§8): the concrete HTTP implementation is supplied by
// the caller (internal/accounts/application, P2b-PROV-005).
type ModelsProbe func(ctx context.Context, baseURL, key string) ([]byte, error)

// OpenCodeZenAdapter implements APIKeyAdapter and ModelDiscoveryAdapter
// for opencode-zen (03 §3): no identity endpoint exists, so identity is
// the key's own fingerprint with a synthetic "Free" plan; funding is
// decided by the funding domain at connect time (02 §2), never
// fabricated here.
type OpenCodeZenAdapter struct {
	chatProbe   ChatProbe
	modelsProbe ModelsProbe
}

// NewOpenCodeZenAdapter builds the adapter over the two injected HTTP
// seams.
func NewOpenCodeZenAdapter(chatProbe ChatProbe, modelsProbe ModelsProbe) *OpenCodeZenAdapter {
	return &OpenCodeZenAdapter{chatProbe: chatProbe, modelsProbe: modelsProbe}
}

// ConnectAPIKey validates key via the authentic chat-completions probe
// (PROV-004) and, on success, reports identity as the key's SHA-256
// fingerprint (hex) with plan "Free". It does NOT create any
// account/credential/funding row itself — it only validates and
// reports; persistence is the caller's job (P2b-PROV-005's connect-time
// sync service). key is never logged.
func (a *OpenCodeZenAdapter) ConnectAPIKey(ctx context.Context, key string) (IdentityResult, StoredCredentials, error) {
	status := ValidateAPIKey(ctx, a.chatProbe, OpenCodeZenBaseURL, key)

	switch status {
	case ValidationValid:
		normalized := NormalizeAPIKey(key)
		fingerprint := fingerprintAPIKey(normalized)
		return IdentityResult{ExternalID: fingerprint, Plan: "Free"}, StoredCredentials{Value: normalized}, nil
	case ValidationInvalid:
		return IdentityResult{}, StoredCredentials{}, ErrInvalidCredential
	default:
		return IdentityResult{}, StoredCredentials{}, ErrProviderUnavailable
	}
}

// openCodeZenModelsResponse is the subset of the OpenAI-compatible
// /v1/models response shape this adapter reads.
type openCodeZenModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// DiscoverModels parses opencode-zen's GET /v1/models response into
// DiscoveredModel values. It does NOT intersect against models.dev or
// persist any offering — that is P3a's job; this unit only proves the
// adapter's discovery capability at the fixture level.
func (a *OpenCodeZenAdapter) DiscoverModels(ctx context.Context, creds StoredCredentials) ([]DiscoveredModel, error) {
	body, err := a.modelsProbe(ctx, OpenCodeZenBaseURL, creds.Value)
	if err != nil {
		return nil, fmt.Errorf("providers: opencode-zen discover models: %w", err)
	}

	var parsed openCodeZenModelsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("providers: opencode-zen discover models: parse response: %w", err)
	}

	models := make([]DiscoveredModel, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		models = append(models, DiscoveredModel{ProviderModelID: m.ID, DisplayName: m.ID})
	}
	return models, nil
}

// RegisterOpenCodeZen registers the opencode-zen APIKey + Discovery
// adapters into reg under OpenCodeZenID. It does NOT wire itself into
// any composition root / ControlMux — that is later (composition)
// work; this function only proves the registration + capability
// derivation at the fixture level.
func RegisterOpenCodeZen(reg *Registry, chatProbe ChatProbe, modelsProbe ModelsProbe) error {
	adapter := NewOpenCodeZenAdapter(chatProbe, modelsProbe)
	return reg.Register(Definition{
		ID:        OpenCodeZenID,
		AuthMode:  AuthModeAPIKey,
		APIKey:    adapter,
		Discovery: adapter,
	})
}

// fingerprintAPIKey computes the hex SHA-256 fingerprint of a
// normalized key — the same dedup/identity fingerprint scheme
// P2b-PROV-003's credential service independently computes for storage
// (both derive it identically: sha256 over the NormalizeAPIKey'd form),
// duplicated here (rather than imported) because internal/providers
// must not import internal/accounts/application (layering).
func fingerprintAPIKey(normalized string) string {
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}
