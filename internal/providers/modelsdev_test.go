package providers

import (
	"context"
	_ "embed"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"
)

//go:embed testdata/modelsdev-fixture.json
var modelsDevFixture []byte

// countingModelsDevProbe returns a fixed body and counts how many times it was
// called, so the cache-TTL test can prove a second call within the window does
// NOT re-fetch and a call after the window DOES.
type countingModelsDevProbe struct {
	body  []byte
	err   error
	calls int
}

func (p *countingModelsDevProbe) probe(context.Context) ([]byte, error) {
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	return p.body, nil
}

const modelsDevTwoProviderFixture = `{
  "ollama-cloud": {"models": {
    "gpt-oss:20b": {"name": "GPT-OSS 20B", "tool_call": true, "structured_output": true,
      "modalities": {"input": ["text","image"], "output": ["text"]},
      "limit": {"context": 131072, "output": 32768}},
    "deepseek-v3.1:671b": {"name": "DeepSeek V3.1", "modalities": {"input": ["text"], "output": ["text"]}}
  }},
  "nvidia": {"models": {
    "meta/llama-3.1-8b-instruct": {"name": "Llama 3.1 8B", "tool_call": true,
      "modalities": {"input": ["text"], "output": ["text"]}, "limit": {"context": 131072}}
  }}
}`

// TestModelsDevSource_CacheTTL proves the fetched dataset is served from cache
// within the TTL (no second probe) and re-fetched after it (injected clock).
func TestModelsDevSource_CacheTTL(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	clock := base
	probe := &countingModelsDevProbe{body: []byte(modelsDevTwoProviderFixture)}
	src := NewModelsDevSource(probe.probe, func() time.Time { return clock })

	if _, err := src.Facts(context.Background(), modelsDevOllamaKey); err != nil {
		t.Fatalf("first Facts() error = %v", err)
	}
	if probe.calls != 1 {
		t.Fatalf("after first call, probe.calls = %d, want 1", probe.calls)
	}

	// A second call for the same key, still within the TTL, must be served from
	// cache — no second probe.
	clock = base.Add(5 * time.Minute)
	if _, err := src.Facts(context.Background(), modelsDevOllamaKey); err != nil {
		t.Fatalf("cached Facts() error = %v", err)
	}
	if probe.calls != 1 {
		t.Fatalf("within TTL, probe.calls = %d, want 1 (served from cache)", probe.calls)
	}

	// Past the TTL, the next call re-fetches.
	clock = base.Add(modelsDevFactsCacheTTL + time.Second)
	if _, err := src.Facts(context.Background(), modelsDevOllamaKey); err != nil {
		t.Fatalf("post-TTL Facts() error = %v", err)
	}
	if probe.calls != 2 {
		t.Fatalf("past TTL, probe.calls = %d, want 2 (re-fetched)", probe.calls)
	}
}

// TestModelsDevSource_MissingKeyIsEmptyNotError proves a provider key absent
// from the dataset yields an empty map and a nil error (no facts is a fact).
func TestModelsDevSource_MissingKeyIsEmptyNotError(t *testing.T) {
	probe := &countingModelsDevProbe{body: []byte(modelsDevTwoProviderFixture)}
	src := NewModelsDevSource(probe.probe, func() time.Time { return time.Unix(1, 0) })

	facts, err := src.Facts(context.Background(), "no-such-provider")
	if err != nil {
		t.Fatalf("Facts() error = %v, want nil for a missing key", err)
	}
	if len(facts) != 0 {
		t.Fatalf("facts = %v, want empty for a missing key", facts)
	}
}

// TestModelsDevSource_FetchAndParseFailures proves a fetch failure and a
// malformed dataset both surface as a non-nil error (so the adapter can fall
// back to listing live ids chat-only).
func TestModelsDevSource_FetchAndParseFailures(t *testing.T) {
	t.Run("fetch failure", func(t *testing.T) {
		probe := &countingModelsDevProbe{err: errors.New("boom")}
		src := NewModelsDevSource(probe.probe, func() time.Time { return time.Unix(1, 0) })
		if _, err := src.Facts(context.Background(), modelsDevOllamaKey); err == nil {
			t.Fatal("Facts() error = nil, want non-nil on a fetch failure")
		}
	})

	t.Run("malformed dataset", func(t *testing.T) {
		probe := &countingModelsDevProbe{body: []byte("{not json")}
		src := NewModelsDevSource(probe.probe, func() time.Time { return time.Unix(1, 0) })
		if _, err := src.Facts(context.Background(), modelsDevOllamaKey); err == nil {
			t.Fatal("Facts() error = nil, want non-nil on a malformed dataset")
		}
	})
}

// TestModelsDevSource_ParsesExplicitFieldsOnly proves each fact comes from an
// explicit dataset field, and absent limits stay nil.
func TestModelsDevSource_ParsesExplicitFieldsOnly(t *testing.T) {
	probe := &countingModelsDevProbe{body: []byte(modelsDevTwoProviderFixture)}
	src := NewModelsDevSource(probe.probe, func() time.Time { return time.Unix(1, 0) })
	facts, err := src.Facts(context.Background(), modelsDevOllamaKey)
	if err != nil {
		t.Fatalf("Facts() error = %v", err)
	}
	rich := facts["gpt-oss:20b"]
	if rich.DisplayName != "GPT-OSS 20B" || !rich.ToolCall || !rich.StructuredOutput || !rich.ImageInput || !rich.OutputAllText {
		t.Fatalf("rich facts = %+v, want name/tool_call/structured_output/image-input/all-text", rich)
	}
	if rich.Context == nil || *rich.Context != 131072 || rich.Output == nil || *rich.Output != 32768 {
		t.Fatalf("rich limits = ctx %v out %v, want 131072 / 32768", rich.Context, rich.Output)
	}
	lean := facts["deepseek-v3.1:671b"]
	if lean.ToolCall || lean.StructuredOutput || lean.ImageInput {
		t.Fatalf("lean facts = %+v, want no capability flags", lean)
	}
	if lean.Context != nil || lean.Output != nil {
		t.Fatalf("lean limits = ctx %v out %v, want nil/nil (absent limits are nil, never 0)", lean.Context, lean.Output)
	}
}

// TestParseModelsDevFacts_ReadsReasoningImageOutputAndMaxInput proves
// Reasoning, ImageOutput and MaxInput are parsed from the real vendored
// fixture (models.dev/api.json, captured 2026-08-05): reasoning: true/false,
// an image-only output modality list, and limit.input where declared.
func TestParseModelsDevFacts_ReadsReasoningImageOutputAndMaxInput(t *testing.T) {
	facts, err := parseModelsDevFacts(modelsDevFixture, "cline-pass")
	if err != nil {
		t.Fatalf("parseModelsDevFacts: %v", err)
	}

	k3, ok := facts["cline-pass/kimi-k3"]
	if !ok {
		t.Fatalf("facts = %v, want an entry for cline-pass/kimi-k3", facts)
	}
	if !k3.Reasoning {
		t.Fatal("kimi-k3 Reasoning = false, want true (the dataset declares reasoning: true)")
	}
	if k3.ImageOutput {
		t.Fatal("kimi-k3 ImageOutput = true, want false (its output modality is text only)")
	}
	if k3.Context == nil || *k3.Context != 1048576 {
		t.Fatalf("kimi-k3 Context = %v, want 1048576", k3.Context)
	}
	if k3.MaxInput != nil {
		t.Fatalf("kimi-k3 MaxInput = %v, want nil (the entry declares no limit.input)", *k3.MaxInput)
	}

	img := facts["cline-pass/image-out-example"]
	if !img.ImageOutput {
		t.Fatal("image-out-example ImageOutput = false, want true")
	}

	ollama, err := parseModelsDevFacts(modelsDevFixture, "ollama-cloud")
	if err != nil {
		t.Fatalf("parseModelsDevFacts(ollama-cloud): %v", err)
	}
	glm := ollama["glm-5.1"]
	if glm.MaxInput == nil || *glm.MaxInput != 190000 {
		t.Fatalf("glm-5.1 MaxInput = %v, want 190000", glm.MaxInput)
	}
}

// TestOperationsFromFacts_DerivesEveryCatalogBackedOperation proves
// OperationsFromFacts is the single derivation from ModelsDevFacts to
// operation strings, in models.Operations() order, grounded only in explicit
// dataset fields.
func TestOperationsFromFacts_DerivesEveryCatalogBackedOperation(t *testing.T) {
	facts, err := parseModelsDevFacts(modelsDevFixture, "cline-pass")
	if err != nil {
		t.Fatalf("parseModelsDevFacts: %v", err)
	}

	got := OperationsFromFacts(facts["cline-pass/kimi-k3"])
	want := []string{"chat", "tools", "structured_output", "vision", "context_window", "reasoning"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OperationsFromFacts(kimi-k3) = %v, want %v", got, want)
	}

	img := OperationsFromFacts(facts["cline-pass/image-out-example"])
	if !slices.Contains(img, "image_generation") {
		t.Fatalf("OperationsFromFacts(image-out-example) = %v, want image_generation present", img)
	}

	// A zero-value ModelsDevFacts has no explicit output-modality evidence at
	// all (absent, not declared non-text-only), so chat is vacuously assumed
	// supported — the same "unknown output must not cause a drop" convention
	// OutputAllText already uses. It must claim nothing else.
	bare := OperationsFromFacts(ModelsDevFacts{})
	if !reflect.DeepEqual(bare, []string{"chat"}) {
		t.Fatalf("OperationsFromFacts(zero) = %v, want [chat] only", bare)
	}
}

// TestOperationsFromFacts_ChatGroundedInDeclaredTextOutput is Task 5 fix
// round 1's pin: "chat" is grounded in the entry's own declared output
// modalities, not asserted unconditionally. An entry whose modalities.output
// is explicitly non-empty and excludes "text" (pure image/media output) gets
// NO "chat"; one that explicitly declares "text" gets chat; one with no
// modalities.output at all still gets chat (unknown output is vacuously
// assumed to support text).
func TestOperationsFromFacts_ChatGroundedInDeclaredTextOutput(t *testing.T) {
	const dataset = `{
	  "chat-ground": {"models": {
	    "image-only": {"modalities": {"output": ["image"]}},
	    "text-only": {"modalities": {"output": ["text"]}},
	    "no-modalities": {}
	  }}
	}`
	facts, err := parseModelsDevFacts([]byte(dataset), "chat-ground")
	if err != nil {
		t.Fatalf("parseModelsDevFacts: %v", err)
	}

	imageOnly := OperationsFromFacts(facts["image-only"])
	if slices.Contains(imageOnly, "chat") {
		t.Fatalf(`OperationsFromFacts(image-only) = %v, want NO chat (modalities.output = ["image"] declares non-text-only output)`, imageOnly)
	}
	if !reflect.DeepEqual(imageOnly, []string{"image_generation"}) {
		t.Fatalf(`OperationsFromFacts(image-only) = %v, want exactly [image_generation]`, imageOnly)
	}

	textOnly := OperationsFromFacts(facts["text-only"])
	if !slices.Contains(textOnly, "chat") {
		t.Fatalf(`OperationsFromFacts(text-only) = %v, want chat (modalities.output explicitly contains "text")`, textOnly)
	}

	noModalities := OperationsFromFacts(facts["no-modalities"])
	if !slices.Contains(noModalities, "chat") {
		t.Fatalf("OperationsFromFacts(no-modalities) = %v, want chat (absent modalities.output is vacuously assumed to support text)", noModalities)
	}
}

// TestModelsFromLiveIDs_KeepsImageOutputModelsAndDropsDeprecated proves the
// drop condition narrowed to deprecation only: an image-output entry is no
// longer hidden now that image_generation is in the operation vocabulary,
// while a deprecated entry is still dropped.
func TestModelsFromLiveIDs_KeepsImageOutputModelsAndDropsDeprecated(t *testing.T) {
	facts, err := parseModelsDevFacts(modelsDevFixture, "cline-pass")
	if err != nil {
		t.Fatalf("parseModelsDevFacts: %v", err)
	}
	out := modelsFromLiveIDs([]string{"cline-pass/image-out-example", "cline-pass/deprecated-example"}, facts)

	if len(out) != 1 {
		t.Fatalf("got %d models, want 1 — the deprecated entry is dropped and the image-output entry is kept", len(out))
	}
	if out[0].ProviderModelID != "cline-pass/image-out-example" {
		t.Fatalf("kept %q, want the image-output model", out[0].ProviderModelID)
	}
	if !slices.Contains(out[0].Capabilities, "image_generation") {
		t.Fatalf("capabilities = %v, want image_generation", out[0].Capabilities)
	}
}
