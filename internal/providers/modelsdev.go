package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// This file is the SHARED models.dev per-model FACTS source used by the
// provider_evidence / evidence_required OpenAI-compatible adapters whose own
// model listing carries no capability or limit metadata — ollama-cloud
// (P7-PROV-006) and nvidia-nim (P7-PROV-009). It reads the public dataset
// (https://models.dev/api.json) keyed by provider key and reports only what
// each entry EXPLICITLY declares.
//
// It is deliberately NOT the same parse as opencode_zen.go's
// parseModelsDevFreeSet: that one encodes a FREE-ONLY COST FILTER ("unknown
// cost is not free"), which is opencode-zen's owner-policy contract. Applying
// a cost filter here would invent a funding policy these providers' catalog
// entries do not declare, so this parse NEVER looks at cost at all.

// modelsDevFactsCacheTTL is how long a fetched dataset is served from cache
// before it is re-fetched (03 §3: "cached ~10 min").
const modelsDevFactsCacheTTL = 10 * time.Minute

// ModelsDevFacts is the per-model subset of one models.dev entry the
// evidence-required OpenAI-compatible adapters read. Every field is taken from
// an EXPLICIT dataset field; an absent field stays the zero value / nil —
// never a guess. (OutputAllText is true for an absent/empty output-modality
// list: "unknown output" must not cause a drop.)
type ModelsDevFacts struct {
	DisplayName      string // the entry's `name` (may be "")
	ToolCall         bool   // explicit `tool_call`
	StructuredOutput bool   // explicit `structured_output`
	ImageInput       bool   // `modalities.input` explicitly contains "image"
	OutputAllText    bool   // every `modalities.output` entry is "text" (empty/absent => true)
	Deprecated       bool   // `status == "deprecated"`
	Context          *int   // `limit.context`; nil when absent
	Output           *int   // `limit.output`; nil when absent
	Reasoning        bool   // explicit `reasoning`
	ImageOutput      bool   // `modalities.output` explicitly contains "image"
	MaxInput         *int   // `limit.input`; nil when absent
}

// modelsDevRawEntry is the subset of one models.dev model entry this parse
// reads. Limit legs are pointers so an absent limit stays nil, never a
// fabricated 0.
type modelsDevRawEntry struct {
	Name             string `json:"name"`
	ToolCall         bool   `json:"tool_call"`
	StructuredOutput bool   `json:"structured_output"`
	Status           string `json:"status"`
	Reasoning        bool   `json:"reasoning"`
	Modalities       struct {
		Input  []string `json:"input"`
		Output []string `json:"output"`
	} `json:"modalities"`
	Limit struct {
		Context *int `json:"context"`
		Output  *int `json:"output"`
		Input   *int `json:"input"`
	} `json:"limit"`
}

// ModelsDevSource fetches and caches the public models.dev dataset and parses
// per-provider-key facts on demand. The clock is injected so the TTL is
// testable without timers; the probe is injected so the package stays free of
// net/http (01 §3/§8). One source may be shared across providers — it caches
// the raw dataset body once and parses each requested key at most once per
// cache window.
type ModelsDevSource struct {
	probe ModelsDevProbe
	now   func() time.Time
	ttl   time.Duration

	mu        sync.Mutex
	raw       []byte
	fetchedAt time.Time
	byKey     map[string]map[string]ModelsDevFacts
}

// NewModelsDevSource builds a source over the injected dataset probe. now
// defaults to time.Now when nil (every real caller); it is injectable so the
// cache TTL is testable without timers.
func NewModelsDevSource(probe ModelsDevProbe, now func() time.Time) *ModelsDevSource {
	if now == nil {
		now = time.Now
	}
	return &ModelsDevSource{probe: probe, now: now, ttl: modelsDevFactsCacheTTL}
}

// Facts returns the per-model facts declared for providerKey in the models.dev
// dataset, keyed by the provider's own model id. It serves the cached dataset
// while it is fresher than the TTL and re-fetches otherwise. A provider key
// absent from the dataset yields an empty map with a nil error (no facts is a
// fact, not a failure); only a fetch failure or a top-level parse failure
// returns a non-nil error — which the caller treats as "no enrichment" (still
// list the live ids), never as an empty account.
func (s *ModelsDevSource) Facts(ctx context.Context, providerKey string) (map[string]ModelsDevFacts, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	if s.raw == nil || now.Sub(s.fetchedAt) >= s.ttl {
		body, err := s.probe(ctx)
		if err != nil {
			return nil, fmt.Errorf("providers: models.dev facts fetch: %w", err)
		}
		// Validate the top-level shape once so a malformed dataset is a typed
		// failure rather than silently-empty facts for every key.
		var probeShape map[string]json.RawMessage
		if err := json.Unmarshal(body, &probeShape); err != nil {
			return nil, fmt.Errorf("providers: models.dev facts parse: %w", err)
		}
		s.raw = body
		s.fetchedAt = now
		s.byKey = make(map[string]map[string]ModelsDevFacts)
	}

	if facts, ok := s.byKey[providerKey]; ok {
		return facts, nil
	}
	facts, err := parseModelsDevFacts(s.raw, providerKey)
	if err != nil {
		return nil, err
	}
	s.byKey[providerKey] = facts
	return facts, nil
}

// parseModelsDevFacts extracts providerKey's per-model facts from the full
// dataset body. A dataset without providerKey yields an empty (non-nil) map —
// the provider simply has no models.dev entry, which is a fact, not a failure.
func parseModelsDevFacts(body []byte, providerKey string) (map[string]ModelsDevFacts, error) {
	var dataset map[string]struct {
		Models map[string]modelsDevRawEntry `json:"models"`
	}
	if err := json.Unmarshal(body, &dataset); err != nil {
		return nil, fmt.Errorf("providers: models.dev facts parse: %w", err)
	}
	facts := make(map[string]ModelsDevFacts)
	entry, ok := dataset[providerKey]
	if !ok {
		return facts, nil
	}
	for id, e := range entry.Models {
		facts[id] = ModelsDevFacts{
			DisplayName:      e.Name,
			ToolCall:         e.ToolCall,
			StructuredOutput: e.StructuredOutput,
			ImageInput:       containsImageModality(e.Modalities.Input),
			OutputAllText:    allTextModalities(e.Modalities.Output),
			Deprecated:       e.Status == "deprecated",
			Context:          e.Limit.Context,
			Output:           e.Limit.Output,
			Reasoning:        e.Reasoning,
			ImageOutput:      containsImageModality(e.Modalities.Output),
			MaxInput:         e.Limit.Input,
		}
	}
	return facts, nil
}

// allTextModalities reports whether every declared output modality is "text".
// An empty or absent list is "all text" (vacuously): an unknown output
// modality must not cause a chat model to be dropped.
func allTextModalities(outputs []string) bool {
	for _, m := range outputs {
		if m != "text" {
			return false
		}
	}
	return true
}

// openAICompatModelList is the subset of the OpenAI-compatible /v1/models
// response the evidence-required adapters read to get the live id list. Shared
// by ollama-cloud and nvidia-nim (and any future OpenAI-compatible discovery).
type openAICompatModelList struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// modelIDsFrom returns the non-empty model ids from an OpenAI-compatible
// listing, in wire order.
func modelIDsFrom(list openAICompatModelList) []string {
	ids := make([]string, 0, len(list.Data))
	for _, m := range list.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	return ids
}

// modelsFromLiveIDs is the SHARED discovery rule for the evidence-required
// OpenAI-compatible providers whose own listing carries no metadata: the live
// ids are the source of truth for WHICH models exist, and models.dev supplies
// the facts. All grounding is explicit (03 §1: capabilities only from explicit
// provider fields):
//
//   - a live id with a models.dev entry that is deprecated, or whose declared
//     output modalities are not all text (it cannot answer a chat-completions
//     request), is DROPPED;
//   - "chat" is asserted for every surviving model (the endpoint is a
//     chat-completions gateway); "tools"/"structured_output"/"vision" only
//     when the entry explicitly declares tool_call / structured_output /
//     image input; NOTHING else;
//   - limits come from limit.context / limit.output, nil when absent;
//   - DisplayName is the entry's name when present, else the raw id;
//   - a live id with NO models.dev entry (uncatalogued, or the whole dataset
//     was unavailable so facts is empty) passes through as chat-only with nil
//     limits — explicitly, never enriched by a guess.
func modelsFromLiveIDs(liveIDs []string, facts map[string]ModelsDevFacts) []DiscoveredModel {
	out := make([]DiscoveredModel, 0, len(liveIDs))
	for _, id := range liveIDs {
		f, known := facts[id]
		if known && (f.Deprecated || !f.OutputAllText) {
			continue
		}
		caps := []string{"chat"}
		if known {
			if f.ToolCall {
				caps = append(caps, "tools")
			}
			if f.StructuredOutput {
				caps = append(caps, "structured_output")
			}
			if f.ImageInput {
				caps = append(caps, "vision")
			}
		}
		displayName := id
		if known && f.DisplayName != "" {
			displayName = f.DisplayName
		}
		dm := DiscoveredModel{
			ProviderModelID: id,
			DisplayName:     displayName,
			Capabilities:    caps,
		}
		if known {
			dm.ContextLength = f.Context
			dm.MaxOutputTokens = f.Output
		}
		out = append(out, dm)
	}
	return out
}
