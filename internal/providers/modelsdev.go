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

	// OutputDeclaresNonTextOnly is true only when `modalities.output` is
	// EXPLICITLY non-empty and contains no "text" entry — the pure
	// image/media-output case (e.g. `["image"]`). It is false both when
	// the list is absent/empty (unknown output — vacuously assume text,
	// same convention as OutputAllText) and when the list explicitly
	// contains "text" (declared text output, possibly alongside other
	// modalities). This is the signal OperationsFromFacts uses to decide
	// whether "chat" itself is grounded, distinct from OutputAllText
	// (which answers a different question: "is every declared output
	// modality text") and from ImageOutput (which only answers "is image
	// among the declared outputs, text or not").
	OutputDeclaresNonTextOnly bool
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
	facts, _, err := s.factsAndRaw(ctx, providerKey)
	return facts, err
}

// factsAndRaw is the shared fetch/cache/parse path behind Facts and
// FactsForProvider. It returns the parsed facts AND the exact raw dataset
// bytes they were parsed from, both read under a single s.mu acquisition —
// callers that also need to inspect the raw dataset (e.g. FactsForProvider's
// api-field verification) must use this instead of calling Facts and then
// separately re-reading s.raw, since a concurrent refetch between two
// separate lock acquisitions could swap s.raw out from under them and leave
// the verification checking a different dataset snapshot than the one the
// facts actually came from.
func (s *ModelsDevSource) factsAndRaw(ctx context.Context, providerKey string) (map[string]ModelsDevFacts, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	if s.raw == nil || now.Sub(s.fetchedAt) >= s.ttl {
		body, err := s.probe(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("providers: models.dev facts fetch: %w", err)
		}
		// Validate the top-level shape once so a malformed dataset is a typed
		// failure rather than silently-empty facts for every key.
		var probeShape map[string]json.RawMessage
		if err := json.Unmarshal(body, &probeShape); err != nil {
			return nil, nil, fmt.Errorf("providers: models.dev facts parse: %w", err)
		}
		s.raw = body
		s.fetchedAt = now
		s.byKey = make(map[string]map[string]ModelsDevFacts)
	}

	if facts, ok := s.byKey[providerKey]; ok {
		return facts, s.raw, nil
	}
	facts, err := parseModelsDevFacts(s.raw, providerKey)
	if err != nil {
		return nil, s.raw, err
	}
	s.byKey[providerKey] = facts
	return facts, s.raw, nil
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
			DisplayName:               e.Name,
			ToolCall:                  e.ToolCall,
			StructuredOutput:          e.StructuredOutput,
			ImageInput:                containsImageModality(e.Modalities.Input),
			OutputAllText:             allTextModalities(e.Modalities.Output),
			Deprecated:                e.Status == "deprecated",
			Context:                   e.Limit.Context,
			Output:                    e.Limit.Output,
			Reasoning:                 e.Reasoning,
			ImageOutput:               containsImageModality(e.Modalities.Output),
			MaxInput:                  e.Limit.Input,
			OutputDeclaresNonTextOnly: declaresNonTextOnlyOutput(e.Modalities.Output),
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

// declaresNonTextOnlyOutput reports whether modalities.output is EXPLICITLY
// non-empty and contains no "text" entry — the pure image/media-output case.
// An absent or empty list answers false (unknown output, vacuously assumed to
// support text — same convention as allTextModalities); a list that includes
// "text" (alone or alongside other modalities) also answers false, since text
// output is then explicitly declared.
func declaresNonTextOnlyOutput(outputs []string) bool {
	if len(outputs) == 0 {
		return false
	}
	for _, m := range outputs {
		if m == "text" {
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

// OperationsFromFacts derives the operation strings a models.dev entry
// DECLARES, in models.Operations() order. Every value is grounded in an
// explicit dataset field; nothing is inferred from the model id.
//
// "chat" is grounded in declared text output, NOT asserted unconditionally:
// an entry whose modalities.output is absent/empty is vacuously assumed to
// support text (unknown output must not cause a chat model to vanish — the
// same convention allTextModalities/OutputAllText already uses); an entry
// that explicitly declares "text" among its outputs gets chat; an entry
// whose modalities.output is explicitly non-empty and excludes "text" (pure
// image/media output) does NOT get chat — asserting chat there would be a
// guess the dataset does not support. (Earlier, this function asserted
// "chat" unconditionally on the theory that internal/intelligence's
// classification layer would keep such an offering out of chat routing
// anyway. That was wrong: Classify (internal/intelligence/classification.go)
// returns a routing candidate the instant it sees ANY models.OperationChat
// entry, before ever consulting native modalities, so an unconditional
// "chat" here would have made every such offering routable. Grounding chat
// in the dataset's own declared output, instead of relying on a downstream
// filter that does not exist, is the actual fix.) "streaming" is
// deliberately ABSENT — models.dev carries no streaming field, and streaming
// is a property of the transport we send with, not of the model (see the
// transport's SupportedCapabilities). "context_window" is emitted when the
// entry declares a context limit, which is what makes the number itself a
// catalog-backed fact.
func OperationsFromFacts(f ModelsDevFacts) []string {
	var ops []string
	if !f.OutputDeclaresNonTextOnly {
		ops = append(ops, "chat")
	}
	if f.ToolCall {
		ops = append(ops, "tools")
	}
	if f.StructuredOutput {
		ops = append(ops, "structured_output")
	}
	if f.ImageInput {
		ops = append(ops, "vision")
	}
	if f.Context != nil {
		ops = append(ops, "context_window")
	}
	if f.ImageOutput {
		ops = append(ops, "image_generation")
	}
	if f.Reasoning {
		ops = append(ops, "reasoning")
	}
	return ops
}

// modelsFromLiveIDs is the SHARED discovery rule for the evidence-required
// OpenAI-compatible providers whose own listing carries no metadata: the live
// ids are the source of truth for WHICH models exist, and models.dev supplies
// the facts. All grounding is explicit (03 §1: capabilities only from explicit
// provider fields):
//
//   - a live id with a models.dev entry that is deprecated is DROPPED. An
//     image-output entry is NOT dropped: image_generation is a recognized
//     operation, so hiding the model would hide a real catalog-backed
//     capability instead of classifying it. (It also no longer needs to be
//     dropped to keep it out of chat routing: OperationsFromFacts now grounds
//     "chat" itself in declared text output, so a pure image-output entry
//     simply comes back without "chat" — see OperationsFromFacts' doc for why
//     the earlier "classification filters it out downstream" rationale here
//     was wrong.)
//   - every operation, INCLUDING "chat", comes from OperationsFromFacts for a
//     known entry, which reads only explicit dataset fields;
//   - limits come from limit.context / limit.input / limit.output, nil when
//     absent;
//   - DisplayName is the entry's name when present, else the raw id;
//   - a live id with NO models.dev entry (uncatalogued, or the whole dataset
//     was unavailable so facts is empty) passes through as chat-only with nil
//     limits — explicitly, never enriched by a guess.
func modelsFromLiveIDs(liveIDs []string, facts map[string]ModelsDevFacts) []DiscoveredModel {
	out := make([]DiscoveredModel, 0, len(liveIDs))
	for _, id := range liveIDs {
		f, known := facts[id]
		if known && f.Deprecated {
			continue
		}
		caps := []string{"chat"}
		if known {
			caps = OperationsFromFacts(f)
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
			dm.MaxInputTokens = f.MaxInput
			dm.MaxOutputTokens = f.Output
		}
		out = append(out, dm)
	}
	return out
}
