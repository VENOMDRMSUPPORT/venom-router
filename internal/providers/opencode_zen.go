package providers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// OpenCodeZenBaseURL is opencode-zen's fixed API base (03 §3).
const OpenCodeZenBaseURL = "https://opencode.ai/zen"

// OpenCodeZenID is the catalog slug this adapter registers under —
// must match PROV-002's BuiltinCatalog entry.
const OpenCodeZenID ProviderID = "opencode-zen"

// modelsDevOpenCodeKey is opencode-zen's slug inside the models.dev
// dataset (the top-level map key whose `models` entry carries the cost
// facts the free-only intersection reads).
const modelsDevOpenCodeKey = "opencode"

// modelsDevCacheTTL is how long a successfully parsed models.dev free-set
// is served from cache before the dataset is re-fetched (03 §3: "cached
// ~10 min").
const modelsDevCacheTTL = 10 * time.Minute

// ErrInvalidCredential is returned by an APIKeyAdapter's ConnectAPIKey
// when the authentic-validation probe (PROV-004) classifies the key as
// genuinely invalid (401/403).
var ErrInvalidCredential = errors.New("providers: credential is invalid")

// ErrProviderUnavailable is returned when the validation probe could
// not reach the provider or got an ambiguous/retryable response (429,
// 5xx, or a transport error) — the key's validity is simply unknown,
// never treated as invalid.
var ErrProviderUnavailable = errors.New("providers: provider unavailable, try again later")

// ErrModelsDevUnavailable is DiscoverModels' typed failure when the
// models.dev dataset cannot be fetched or parsed and no fresh cache
// exists. 03 §3's contract is absolute — "a free account must never
// surface paid models" — so discovery fails LOUDLY here rather than ever
// falling back to the unfiltered provider list or silently returning
// empty.
var ErrModelsDevUnavailable = errors.New("providers: opencode-zen free-model dataset (models.dev) unavailable and no fresh cache exists")

// ModelsProbe fetches the raw JSON body of the provider's model-listing
// endpoint. Like ChatProbe, this keeps internal/providers free of
// net/http (01 §3/§8): the concrete HTTP implementation is supplied by
// the caller (internal/accounts/application, P2b-PROV-005).
type ModelsProbe func(ctx context.Context, baseURL, key string) ([]byte, error)

// ModelsDevProbe fetches the raw JSON body of the PUBLIC models.dev
// dataset (https://models.dev/api.json). Same seam pattern as
// ChatProbe/ModelsProbe — no net/http in this package — and, being a
// public dataset, its implementation must never send any account
// credential.
type ModelsDevProbe func(ctx context.Context) ([]byte, error)

// ModelsProbeStatusError is the typed error a ModelsProbe implementation
// returns when the provider answered with a non-2xx status, so a caller
// can read the REAL wire status instead of an opaque string.
// Transport-level failures stay plain errors. (Zen health no longer reads
// this type — its models endpoint ignores credentials, see checkHealth —
// but the seam contract stays: discovery and future adapters still get
// the typed status.)
type ModelsProbeStatusError struct {
	StatusCode int
}

func (e *ModelsProbeStatusError) Error() string {
	return fmt.Sprintf("providers: models probe returned status %d", e.StatusCode)
}

// OpenCodeZenAdapter implements APIKeyAdapter, ModelDiscoveryAdapter and
// HealthAdapter for opencode-zen (03 §3): no identity endpoint exists, so
// identity is the key's own fingerprint with a synthetic "Free" plan;
// funding is decided by the funding domain at connect time (02 §2), never
// fabricated here. Discovery is the free-only intersection against
// models.dev; health is the same authentic chat-validation probe
// ConnectAPIKey uses (NOT the models listing — see checkHealth for why).
type OpenCodeZenAdapter struct {
	chatProbe      ChatProbe
	modelsProbe    ModelsProbe
	modelsDevProbe ModelsDevProbe
	now            func() time.Time

	// The parsed models.dev free-set cache (mutex-guarded; the clock is
	// injected so tests need no timers). fetchedAt is the zero time until
	// the first successful parse.
	mu             sync.Mutex
	freeSet        map[string]struct{}
	freeSetFetched time.Time
}

// NewOpenCodeZenAdapter builds the adapter over the three injected HTTP
// seams. now defaults to time.Now when nil (every real caller), and is
// injectable so the free-set cache TTL is testable without timers.
func NewOpenCodeZenAdapter(chatProbe ChatProbe, modelsProbe ModelsProbe, modelsDevProbe ModelsDevProbe, now func() time.Time) *OpenCodeZenAdapter {
	if now == nil {
		now = time.Now
	}
	return &OpenCodeZenAdapter{chatProbe: chatProbe, modelsProbe: modelsProbe, modelsDevProbe: modelsDevProbe, now: now}
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

// DiscoverModels parses opencode-zen's GET /v1/models response and
// intersects it with the models.dev free set (03 §3): only models the
// dataset EXPLICITLY prices at zero (cost.input == 0 && cost.output == 0)
// and does not mark deprecated survive. A model present in zen's list but
// absent from the models.dev opencode entry — or present without an
// explicit cost — is EXCLUDED: unknown cost is not free. If the free set
// cannot be established (models.dev unreachable/unparseable and no fresh
// cache), the typed ErrModelsDevUnavailable is returned so the discovery
// job fails loudly — never the unfiltered list, never a silent empty.
func (a *OpenCodeZenAdapter) DiscoverModels(ctx context.Context, creds StoredCredentials) ([]DiscoveredModel, error) {
	body, err := a.modelsProbe(ctx, OpenCodeZenBaseURL, creds.Value)
	if err != nil {
		return nil, fmt.Errorf("providers: opencode-zen discover models: %w", err)
	}

	var parsed openCodeZenModelsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("providers: opencode-zen discover models: parse response: %w", err)
	}

	freeSet, err := a.freeModelSet(ctx)
	if err != nil {
		return nil, err
	}

	models := make([]DiscoveredModel, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		if _, free := freeSet[m.ID]; !free {
			continue
		}
		models = append(models, DiscoveredModel{ProviderModelID: m.ID, DisplayName: m.ID})
	}
	return models, nil
}

// freeModelSet returns the current models.dev free set for opencode-zen,
// serving the cached parse while it is fresher than modelsDevCacheTTL and
// re-fetching otherwise. On a failed fetch/parse it returns the typed
// ErrModelsDevUnavailable (wrapping the cause) — a STALE cache is
// deliberately not a fallback: serving day-old cost facts to a free-only
// account is the exact failure 03 §3's contract exists to prevent.
func (a *OpenCodeZenAdapter) freeModelSet(ctx context.Context) (map[string]struct{}, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := a.now()
	if a.freeSet != nil && now.Sub(a.freeSetFetched) < modelsDevCacheTTL {
		return a.freeSet, nil
	}

	body, err := a.modelsDevProbe(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrModelsDevUnavailable, err)
	}
	freeSet, err := parseModelsDevFreeSet(body)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrModelsDevUnavailable, err)
	}

	a.freeSet = freeSet
	a.freeSetFetched = now
	return freeSet, nil
}

// modelsDevModel is the subset of one models.dev model entry the free
// classification reads. Cost and its two legs are POINTERS on purpose:
// an absent cost is an unknown cost, and unknown is never classified as
// free — only an explicit {input: 0, output: 0} qualifies.
type modelsDevModel struct {
	Cost *struct {
		Input  *float64 `json:"input"`
		Output *float64 `json:"output"`
	} `json:"cost"`
	Status string `json:"status"`
}

// parseModelsDevFreeSet parses the full models.dev dataset body and
// returns the set of opencode model ids that are EXPLICITLY zero-cost and
// not deprecated. A dataset without the opencode entry is a parse-level
// failure (the shape this adapter depends on has drifted), never an
// authoritative "nothing is free".
func parseModelsDevFreeSet(body []byte) (map[string]struct{}, error) {
	var dataset map[string]struct {
		Models map[string]modelsDevModel `json:"models"`
	}
	if err := json.Unmarshal(body, &dataset); err != nil {
		return nil, fmt.Errorf("parse models.dev dataset: %w", err)
	}
	entry, ok := dataset[modelsDevOpenCodeKey]
	if !ok || entry.Models == nil {
		return nil, fmt.Errorf("models.dev dataset has no %q provider entry", modelsDevOpenCodeKey)
	}

	freeSet := make(map[string]struct{})
	for id, m := range entry.Models {
		if m.Status == "deprecated" {
			continue
		}
		if m.Cost == nil || m.Cost.Input == nil || m.Cost.Output == nil {
			continue // unknown cost is NOT free
		}
		if *m.Cost.Input == 0 && *m.Cost.Output == 0 {
			freeSet[id] = struct{}{}
		}
	}
	return freeSet, nil
}

// CheckAccountHealth implements HealthAdapter via the SAME authentic
// chat-validation probe ConnectAPIKey uses (ValidateAPIKey over the
// injected ChatProbe; zen models are free, so the max_tokens:1 probe
// costs nothing).
//
// It deliberately does NOT probe GET /v1/models: that endpoint is PUBLIC
// and ignores the Bearer credential entirely — verified live 2026-08-02,
// it answers 200 both with no Authorization header at all and with a
// garbage key — so a models-list health check reports healthy for ANY
// stored key, including a revoked one (a false green). 03 §3 documents
// the free-model chat probe as the credential-authentic alternative, and
// that is the only one of the two that actually reads the credential. Do
// not "simplify" this back to the models listing.
//
// The classification NEVER guesses:
//
//	ValidationValid       -> healthy (the provider authenticated the key —
//	                         a 2xx chat response, or a CreditsError-style
//	                         billing rejection the seam proves was
//	                         computed AFTER recognizing the key)
//	ValidationInvalid     -> expired (the provider READ and REJECTED the
//	                         credential; transport reachable)
//	ValidationUnavailable -> unreachable (retryable — a rate limit,
//	                         provider fault, ambiguous 401, or transport
//	                         error says nothing about the key)
//
// The observation is always returned with a nil error — every real
// outcome above IS the observation; there is no failure mode left to
// signal separately.
func (a *OpenCodeZenAdapter) CheckAccountHealth(ctx context.Context, creds StoredCredentials) (HealthObservation, error) {
	return a.checkHealth(ctx, creds, "account")
}

// CheckOfferingHealth reuses the account-level probe: opencode-zen has no
// per-model health endpoint, so the credential-authentic chat probe is
// the health call regardless of model (03 §3).
func (a *OpenCodeZenAdapter) CheckOfferingHealth(ctx context.Context, creds StoredCredentials, providerModelID string) (HealthObservation, error) {
	return a.checkHealth(ctx, creds, "offering")
}

func (a *OpenCodeZenAdapter) checkHealth(ctx context.Context, creds StoredCredentials, scope string) (HealthObservation, error) {
	checkedAt := a.now().Unix()

	switch ValidateAPIKey(ctx, a.chatProbe, OpenCodeZenBaseURL, creds.Value) {
	case ValidationValid:
		return HealthObservation{
			Status:             "healthy",
			Scope:              scope,
			CredentialValid:    true,
			TransportReachable: true,
			CheckedAt:          checkedAt,
		}, nil
	case ValidationInvalid:
		// The provider read the credential and rejected it — a definitive
		// auth failure, NOT unavailability.
		return HealthObservation{
			Status:             "expired",
			Scope:              scope,
			CredentialValid:    false,
			TransportReachable: true,
			CheckedAt:          checkedAt,
			Failure: &HealthFailure{
				Class:       "auth",
				Retryable:   false,
				SafeMessage: "provider rejected the credential (401/403)",
			},
		}, nil
	default:
		// 429, 5xx, an ambiguous status, or a transport error: the key's
		// validity is unknown and the check is retryable — never expired.
		return HealthObservation{
			Status:             "unreachable",
			Scope:              scope,
			CredentialValid:    false,
			TransportReachable: false,
			CheckedAt:          checkedAt,
			Failure: &HealthFailure{
				Class:       "unavailable",
				Retryable:   true,
				SafeMessage: "provider unavailable or rate limited",
			},
		}, nil
	}
}

// RegisterOpenCodeZen registers the opencode-zen APIKey + Health +
// Discovery adapters into reg under OpenCodeZenID. now may be nil (real
// callers); tests inject a fake clock for the models.dev cache TTL. It
// does NOT wire itself into any composition root / ControlMux — that is
// the caller's job (internal/httpapi's registerOpenCodeZen).
func RegisterOpenCodeZen(reg *Registry, chatProbe ChatProbe, modelsProbe ModelsProbe, modelsDevProbe ModelsDevProbe, now func() time.Time) error {
	adapter := NewOpenCodeZenAdapter(chatProbe, modelsProbe, modelsDevProbe, now)
	return reg.Register(Definition{
		ID:        OpenCodeZenID,
		AuthMode:  AuthModeAPIKey,
		Transport: TransportKindOpenAICompatible,
		APIKey:    adapter,
		Health:    adapter,
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
