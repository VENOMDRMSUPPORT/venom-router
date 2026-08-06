package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// NvidiaNIMID is the catalog slug this adapter registers under.
const NvidiaNIMID ProviderID = "nvidia-nim"

// NvidiaNIMBaseURL is nvidia-nim's OpenAI-compatible API base (03 §3). It MUST
// equal the BuiltinCatalog entry — asserted by a test.
const NvidiaNIMBaseURL = "https://integrate.api.nvidia.com/v1"

// nvidiaSyntheticPlan is the display-only plan label nvidia-nim has no identity
// endpoint to source. It is NOT funding evidence.
const nvidiaSyntheticPlan = "Free"

// NvidiaNIMAdapter implements APIKeyAdapter, ModelDiscoveryAdapter and
// HealthAdapter for nvidia-nim (03 §3). It has no identity endpoint (identity
// is the key fingerprint + a synthetic Free plan), funding is unknown
// (evidence_required), and it registers NO quota adapter: NVIDIA publishes no
// usage API and the per-model request limit is undocumented and
// dashboard-only. Validation and health are the authentic two-step chat probe;
// discovery reads the live id list from /v1/models and enriches it from the
// models.dev `nvidia` key via the shared facts source.
type NvidiaNIMAdapter struct {
	chatProbe   ChatProbe
	modelsProbe ModelsProbe
	facts       *ModelsDevSource
	now         func() time.Time
}

// NewNvidiaNIMAdapter builds the adapter over the injected chat/models probes
// and the shared models.dev facts source (built from modelsDevProbe + now). now
// defaults to time.Now when nil (every real caller); it is injectable so both
// the models.dev cache TTL and the health observation's CheckedAt stamp are
// testable without a real clock.
func NewNvidiaNIMAdapter(chatProbe ChatProbe, modelsProbe ModelsProbe, modelsDevProbe ModelsDevProbe, now func() time.Time) *NvidiaNIMAdapter {
	if now == nil {
		now = time.Now
	}
	return &NvidiaNIMAdapter{
		chatProbe:   chatProbe,
		modelsProbe: modelsProbe,
		facts:       NewModelsDevSource(modelsDevProbe, now),
		now:         now,
	}
}

// ConnectAPIKey validates key via the authentic two-step chat probe and, on
// success, reports identity as the key's fingerprint with a synthetic Free
// plan. Funding stays "" (evidence_required). key is never logged.
func (a *NvidiaNIMAdapter) ConnectAPIKey(ctx context.Context, key string) (IdentityResult, StoredCredentials, error) {
	switch ValidateAPIKey(ctx, a.chatProbe, NvidiaNIMBaseURL, key) {
	case ValidationValid:
		normalized := NormalizeAPIKey(key)
		return IdentityResult{ExternalID: fingerprintAPIKey(normalized), Plan: nvidiaSyntheticPlan}, StoredCredentials{Value: normalized}, nil
	case ValidationInvalid:
		return IdentityResult{}, StoredCredentials{}, ErrInvalidCredential
	default:
		return IdentityResult{}, StoredCredentials{}, ErrProviderUnavailable
	}
}

// DiscoverModels reads the live id list from GET /v1/models and enriches it
// with the models.dev `nvidia` facts (verified 2026-08-03: the dataset key is
// "nvidia", NOT "nvidia-nim", and its 98 model ids match /v1/models 1:1),
// resolved through FactsForProvider so a dataset entry whose `api` no longer
// matches NvidiaNIMBaseURL is refused rather than silently joined (see
// FactsForProvider's doc — this is the opencode/opencode-go collision
// scenario applied to nvidia-nim). Applies the shared discovery rules
// (modelsFromLiveIDs): deprecated dropped (narrowed from an earlier
// non-text-output drop — reasoning/image_generation/context_window are now
// certifiable operations, not reasons to hide a model), capabilities from
// tool_call/structured_output/image-input/context/image-output/reasoning,
// limits from limit.context/limit.input/limit.output, uncatalogued ids
// chat-only, dataset-down still lists the live ids.
//
// It deliberately does NOT try to distinguish a true chat/instruct model from a
// text-output non-generative one (embedding, rerank, safety classifier):
// models.dev carries no field for that, so those pass through as chat-only and
// the runtime usability probe is what surfaces them. Guessing from a model name
// or description is forbidden (§2).
func (a *NvidiaNIMAdapter) DiscoverModels(ctx context.Context, creds StoredCredentials) ([]DiscoveredModel, error) {
	body, err := a.modelsProbe(ctx, NvidiaNIMBaseURL, creds.Value)
	if err != nil {
		return nil, fmt.Errorf("providers: nvidia-nim discover models: %w", err)
	}
	var list openAICompatModelList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("providers: nvidia-nim discover models: parse response: %w", err)
	}

	facts, ferr := a.facts.FactsForProvider(ctx, NvidiaNIMID, NvidiaNIMBaseURL)
	if ferr != nil {
		// Dataset unavailable/unparseable: still list the live ids chat-only.
		facts = nil
	}
	return modelsFromLiveIDs(modelIDsFrom(list), facts), nil
}

// CheckAccountHealth implements HealthAdapter via the same authentic two-step
// chat probe ConnectAPIKey uses.
func (a *NvidiaNIMAdapter) CheckAccountHealth(ctx context.Context, creds StoredCredentials) (HealthObservation, error) {
	return a.checkHealth(ctx, creds, "account")
}

// CheckOfferingHealth reuses the account-level probe: nvidia-nim has no
// per-model health endpoint.
func (a *NvidiaNIMAdapter) CheckOfferingHealth(ctx context.Context, creds StoredCredentials, _ string) (HealthObservation, error) {
	return a.checkHealth(ctx, creds, "offering")
}

// checkHealth maps the authentic validation outcome onto the shared health
// observation, stamped with the injected clock (observationFromValidation).
func (a *NvidiaNIMAdapter) checkHealth(ctx context.Context, creds StoredCredentials, scope string) (HealthObservation, error) {
	status := ValidateAPIKey(ctx, a.chatProbe, NvidiaNIMBaseURL, creds.Value)
	return observationFromValidation(status, scope, a.now().Unix()), nil
}

// RegisterNvidiaNIM registers the nvidia-nim APIKey + Health + Discovery
// adapters into reg. now may be nil (real callers); tests inject a fake clock
// for the models.dev cache TTL. It does NOT wire itself into any composition
// root — that is the caller's job (httpapi's registerNvidiaNIM).
func RegisterNvidiaNIM(reg *Registry, chatProbe ChatProbe, modelsProbe ModelsProbe, modelsDevProbe ModelsDevProbe, now func() time.Time) error {
	adapter := NewNvidiaNIMAdapter(chatProbe, modelsProbe, modelsDevProbe, now)
	return reg.Register(Definition{
		ID:        NvidiaNIMID,
		AuthMode:  AuthModeAPIKey,
		Transport: TransportKindOpenAICompatible,
		APIKey:    adapter,
		Health:    adapter,
		Discovery: adapter,
	})
}
