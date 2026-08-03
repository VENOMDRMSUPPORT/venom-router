package providers

import (
	"context"
	"errors"
	"testing"
	"time"
)

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
