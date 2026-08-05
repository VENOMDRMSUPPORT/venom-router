package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// modelsDevKeys maps our provider slug onto the models.dev dataset key.
//
// The map is AUTHORITATIVE and the dataset's `api` field is only a CHECK.
// Resolution by URL is impossible in general: opencode.ai is the host of two
// dataset providers — "opencode" (/zen/v1) and "opencode-go" (/zen/go/v1) —
// so a host match cannot select between them. Stating the intent here and
// verifying it below is both explicit and self-checking.
//
// Every entry was verified against the live dataset on 2026-08-05. Providers
// absent from this map simply get no catalog enrichment; agnes-ai has no
// models.dev entry under any key, which is an upstream gap, not a bug here.
//
// github-copilot and xai are deliberately NOT mapped here: as of this task
// neither has an exported ProviderID constant in this package (no adapter has
// landed for them yet — see internal/providers, no github_copilot.go or
// xai.go). Inventing a constant or a bare string literal for an
// unimplemented provider would be exactly the drift this map exists to
// prevent, so those two entries are omitted until their adapters exist.
var modelsDevKeys = map[ProviderID]string{
	ClinePassID:   "cline-pass",
	NvidiaNIMID:   "nvidia",
	OpenCodeZenID: "opencode",
	OllamaCloudID: "ollama-cloud",
	ClaudeCodeID:  "anthropic",
	GeminiCLIID:   "google",
}

// ModelsDevKeyFor reports the dataset key for providerID, and false when the
// provider has no mapped entry.
func ModelsDevKeyFor(providerID ProviderID) (string, bool) {
	key, ok := modelsDevKeys[providerID]
	return key, ok
}

// modelsDevProviderAPI extracts one provider entry's `api` field. An absent
// or empty value yields "" — several real entries (anthropic, openai, google,
// xai) carry no api at all.
func modelsDevProviderAPI(body []byte, key string) string {
	var dataset map[string]struct {
		API string `json:"api"`
	}
	if err := json.Unmarshal(body, &dataset); err != nil {
		return ""
	}
	return dataset[key].API
}

// sameHost reports whether two URLs share a host. Both must parse and both
// must carry a host; anything else is not a match.
func sameHost(a, b string) bool {
	ua, err := url.Parse(a)
	if err != nil || ua.Host == "" {
		return false
	}
	ub, err := url.Parse(b)
	if err != nil || ub.Host == "" {
		return false
	}
	return ua.Host == ub.Host
}

// FactsForProvider returns the models.dev facts for providerID, keyed by the
// provider's own model id.
//
// It fails CLOSED in both directions that matter: an unmapped provider and a
// provider whose dataset entry declares an `api` on a different host both
// yield an EMPTY map. Neither is an error — "no catalog facts" is a fact, and
// discovery must still list the provider's live models. Only a fetch or
// top-level parse failure returns a non-nil error.
//
// When the dataset entry declares no `api` (anthropic, google, openai, xai),
// the check cannot run and the map entry stands as the only evidence. That is
// recorded here rather than silently assumed.
//
// The facts and the raw dataset bytes used for the api-field check are read
// together via factsAndRaw under a single s.mu acquisition, so a concurrent
// refetch cannot swap the dataset out between "get the facts" and "check the
// api field" — the api checked is always the one the facts came from.
func (s *ModelsDevSource) FactsForProvider(ctx context.Context, providerID ProviderID, baseURL string) (map[string]ModelsDevFacts, error) {
	key, mapped := ModelsDevKeyFor(providerID)
	if !mapped {
		return map[string]ModelsDevFacts{}, nil
	}

	facts, raw, err := s.factsAndRaw(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("providers: models.dev facts for %q: %w", providerID, err)
	}

	if api := modelsDevProviderAPI(raw, key); api != "" && !sameHost(api, baseURL) {
		// The dataset moved under us. Refuse the key rather than join the
		// wrong provider's facts onto our models.
		return map[string]ModelsDevFacts{}, nil
	}

	return facts, nil
}
