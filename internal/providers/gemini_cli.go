package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// GeminiCLIID is the catalog slug this adapter registers under.
const GeminiCLIID ProviderID = "gemini-cli"

// GeminiCLIBaseURL is gemini-cli's Google generativelanguage base (03 §3). It
// MUST equal the BuiltinCatalog entry — asserted by a test. Discovery/health
// hit the /v1beta path under it; the inference transport is handed
// GeminiCLIBaseURL + "/v1beta" (it appends /models/{id}:generateContent).
const GeminiCLIBaseURL = "https://generativelanguage.googleapis.com"

// geminiSyntheticPlan is the display-only plan label gemini-cli has no identity
// endpoint to source (03 §3). It is NOT funding evidence.
const geminiSyntheticPlan = "Free"

// maxGeminiListPages bounds discovery paging: the listing is provider-
// controlled data, so an unbounded follow-the-token loop would be a
// denial-of-service vector. 50 pages at pageSize 200 is 10k models — far more
// than Google lists — so hitting the bound means the pager never terminated,
// which is a typed failure, not a silent truncation.
const maxGeminiListPages = 50

// ErrGeminiPagingBudgetExceeded is returned when discovery follows
// nextPageToken past maxGeminiListPages without the provider ever returning an
// empty token — a runaway pager, surfaced loudly rather than looping forever.
var ErrGeminiPagingBudgetExceeded = errors.New("providers: gemini-cli model listing exceeded the page budget")

// GoogleModelsProbe fetches one page of Google's model listing. It carries the
// Google API-key auth header (x-goog-api-key, NOT Bearer) inside its concrete
// implementation, and sends the paging value as the request parameter
// `pageToken` (the RESPONSE field is `nextPageToken` — the two are NOT the same
// name). pageToken is "" for the first page. It returns the raw status + body
// so the adapter can classify (validation) and parse (discovery). The concrete
// net/http implementation is injected by the caller (httpapi) so this package
// holds no net/http import. It must never log key.
type GoogleModelsProbe func(ctx context.Context, baseURL, key, pageToken string) (statusCode int, body []byte, err error)

// GeminiCLIAdapter implements APIKeyAdapter, ModelDiscoveryAdapter and
// HealthAdapter for gemini-cli over Google's model schema (03 §3). The listing
// itself authenticates, so validation needs no two-step chat probe (unlike the
// OpenAI-compatible providers). Identity is the key fingerprint + a synthetic
// Free plan; funding stays "" (evidence_required — the Free label is a display
// string, 03 §3). No quota adapter (none proven).
type GeminiCLIAdapter struct {
	probe GoogleModelsProbe
	now   func() time.Time
}

// NewGeminiCLIAdapter builds the adapter over the injected Google models probe.
// now defaults to time.Now when nil (every real caller); it is injectable so the
// health observation's CheckedAt stamp is testable without a real clock.
func NewGeminiCLIAdapter(probe GoogleModelsProbe, now func() time.Time) *GeminiCLIAdapter {
	if now == nil {
		now = time.Now
	}
	return &GeminiCLIAdapter{probe: probe, now: now}
}

// ConnectAPIKey validates key by a single model-listing call (the listing
// authenticates), and on success reports identity as the key's fingerprint
// with a synthetic Free plan. 401/403 -> invalid; 429/5xx/transport ->
// unavailable; 2xx -> valid. Funding stays "" (evidence_required). key is
// never logged.
func (a *GeminiCLIAdapter) ConnectAPIKey(ctx context.Context, key string) (IdentityResult, StoredCredentials, error) {
	normalized := NormalizeAPIKey(key)
	switch a.classifyListing(ctx, normalized) {
	case ValidationValid:
		return IdentityResult{ExternalID: fingerprintAPIKey(normalized), Plan: geminiSyntheticPlan}, StoredCredentials{Value: normalized}, nil
	case ValidationInvalid:
		return IdentityResult{}, StoredCredentials{}, ErrInvalidCredential
	default:
		return IdentityResult{}, StoredCredentials{}, ErrProviderUnavailable
	}
}

// classifyListing runs a single first-page listing call and maps its outcome
// onto the 3-way classification (the same rule ValidateAPIKey applies to an
// HTTP status): transport error -> unavailable; 401/403 -> invalid;
// 429/5xx/other -> unavailable; 2xx -> valid.
func (a *GeminiCLIAdapter) classifyListing(ctx context.Context, key string) ValidationStatus {
	status, _, err := a.probe(ctx, GeminiCLIBaseURL, key, "")
	if err != nil {
		return ValidationUnavailable
	}
	switch {
	case status == 401 || status == 403:
		return ValidationInvalid
	case status == 429 || (status >= 500 && status <= 599):
		return ValidationUnavailable
	case status >= 200 && status < 300:
		return ValidationValid
	default:
		return ValidationUnavailable
	}
}

// geminiListModel is the subset of one Google models.list row this adapter
// reads. Name comes back PREFIXED "models/". The token limits are pointers so
// an absent limit stays nil, never a fabricated 0.
type geminiListModel struct {
	Name                       string   `json:"name"`
	DisplayName                string   `json:"displayName"`
	InputTokenLimit            *int     `json:"inputTokenLimit"`
	OutputTokenLimit           *int     `json:"outputTokenLimit"`
	SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
}

type geminiListResponse struct {
	Models        []geminiListModel `json:"models"`
	NextPageToken string            `json:"nextPageToken"`
}

// DiscoverModels reads GET /v1beta/models paginated by nextPageToken (bounded
// by maxGeminiListPages), keeping only rows that can serve a chat-completions
// request and are not one of the documented non-chat generateContent families.
func (a *GeminiCLIAdapter) DiscoverModels(ctx context.Context, creds StoredCredentials) ([]DiscoveredModel, error) {
	var out []DiscoveredModel
	token := ""
	for page := 0; page < maxGeminiListPages; page++ {
		status, body, err := a.probe(ctx, GeminiCLIBaseURL, creds.Value, token)
		if err != nil {
			return nil, fmt.Errorf("providers: gemini-cli discover models: %w", err)
		}
		if status < 200 || status >= 300 {
			return nil, fmt.Errorf("providers: gemini-cli discover models: listing status %d", status)
		}
		var resp geminiListResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("providers: gemini-cli discover models: parse response: %w", err)
		}
		for _, m := range resp.Models {
			if dm, ok := geminiModelFrom(m); ok {
				out = append(out, dm)
			}
		}
		if resp.NextPageToken == "" {
			return out, nil
		}
		token = resp.NextPageToken
	}
	return nil, ErrGeminiPagingBudgetExceeded
}

// geminiModelFrom maps one listing row to a DiscoveredModel, or ok=false if the
// row must be filtered out. A row is kept only when its supportedGenerationMethods
// EXPLICITLY contains generateContent (a row with no usable generation method
// is filtered, never defaulted to chat) AND it is not one of the documented
// non-chat generateContent families.
func geminiModelFrom(m geminiListModel) (DiscoveredModel, bool) {
	id := strings.TrimPrefix(m.Name, "models/")
	if id == "" {
		return DiscoveredModel{}, false
	}
	if !containsString(m.SupportedGenerationMethods, "generateContent") {
		return DiscoveredModel{}, false
	}
	if geminiExcludedFamily(id) {
		return DiscoveredModel{}, false
	}
	display := m.DisplayName
	if display == "" {
		display = id
	}
	dm := DiscoveredModel{
		ProviderModelID: id,
		DisplayName:     display,
		// chat only: the listing carries no explicit field for tools,
		// streaming, structured_output or vision, and `thinking`/`description`
		// are not capability evidence. Asserting any of them would be
		// fabrication (§2), so the omission is a decision, not an oversight.
		Capabilities: []string{"chat"},
		// inputTokenLimit IS the model's context ceiling — Google publishes no
		// separate total-context field, so BOTH ContextLength and
		// MaxInputTokens come from it (leaving ContextLength nil would revive
		// the "ctx unknown" display defect). outputTokenLimit -> MaxOutputTokens.
		// Each stays nil when its field is absent (never 0).
		ContextLength:   m.InputTokenLimit,
		MaxInputTokens:  m.InputTokenLimit,
		MaxOutputTokens: m.OutputTokenLimit,
	}
	return dm, true
}

// geminiExcludedFamily reports whether a (prefix-stripped) model id is one of
// the non-chat generateContent families 03 §3 documents as excluded (TTS,
// image, native-audio, live). This is a DOCUMENTED PROVIDER-FAMILY EXCLUSION
// LIST from 03 §3 — not a capability guess from a model name — and it is the
// only id-shape rule in the adapter. Matching is case-insensitive.
func geminiExcludedFamily(id string) bool {
	lower := strings.ToLower(id)
	switch {
	case strings.HasSuffix(lower, "-tts"), strings.HasSuffix(lower, "-tts-preview"):
		return true
	case strings.HasSuffix(lower, "-image"), strings.HasSuffix(lower, "-image-preview"):
		return true
	case strings.Contains(lower, "native-audio"):
		return true
	case strings.HasSuffix(lower, "-live-preview"), strings.HasSuffix(lower, "-live-translate-preview"):
		return true
	default:
		return false
	}
}

// containsString reports whether haystack contains needle (exact match).
func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// CheckAccountHealth implements HealthAdapter via the same credential-authentic
// model-listing call ConnectAPIKey uses.
func (a *GeminiCLIAdapter) CheckAccountHealth(ctx context.Context, creds StoredCredentials) (HealthObservation, error) {
	return a.checkHealth(ctx, creds, "account")
}

// CheckOfferingHealth reuses the account-level probe: gemini-cli has no
// per-model health endpoint.
func (a *GeminiCLIAdapter) CheckOfferingHealth(ctx context.Context, creds StoredCredentials, _ string) (HealthObservation, error) {
	return a.checkHealth(ctx, creds, "offering")
}

// checkHealth maps the authentic listing outcome onto the shared health
// observation, stamped with the injected clock (observationFromValidation).
func (a *GeminiCLIAdapter) checkHealth(ctx context.Context, creds StoredCredentials, scope string) (HealthObservation, error) {
	status := a.classifyListing(ctx, creds.Value)
	return observationFromValidation(status, scope, a.now().Unix()), nil
}

// RegisterGeminiCLI registers the gemini-cli APIKey + Health + Discovery
// adapters into reg with the native_api transport kind. now may be nil (real
// callers); tests inject a fake clock. It does NOT wire itself into any
// composition root — that is the caller's job (httpapi's registerGeminiCLI).
func RegisterGeminiCLI(reg *Registry, probe GoogleModelsProbe, now func() time.Time) error {
	adapter := NewGeminiCLIAdapter(probe, now)
	return reg.Register(Definition{
		ID:        GeminiCLIID,
		AuthMode:  AuthModeAPIKey,
		Transport: TransportKindNativeAPI,
		APIKey:    adapter,
		Health:    adapter,
		Discovery: adapter,
	})
}
