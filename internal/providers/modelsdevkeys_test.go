package providers

import (
	"context"
	"testing"
)

func fixtureSource(t *testing.T) *ModelsDevSource {
	t.Helper()
	return NewModelsDevSource(func(context.Context) ([]byte, error) {
		return modelsDevFixture, nil
	}, nil)
}

func TestModelsDevKeyFor_MapsOurSlugs(t *testing.T) {
	for _, tc := range []struct {
		id   ProviderID
		want string
	}{
		{ClinePassID, "cline-pass"},
		{NvidiaNIMID, "nvidia"},
		{OpenCodeZenID, "opencode"},
		{OllamaCloudID, "ollama-cloud"},
		{ClaudeCodeID, "anthropic"},
	} {
		got, ok := ModelsDevKeyFor(tc.id)
		if !ok || got != tc.want {
			t.Fatalf("ModelsDevKeyFor(%q) = (%q, %v), want (%q, true)", tc.id, got, ok, tc.want)
		}
	}
	if _, ok := ModelsDevKeyFor(AgnesAIID); ok {
		t.Fatal("ModelsDevKeyFor(agnes-ai) reported a key; agnes-ai has no models.dev entry and must report none")
	}
}

func TestFactsForProvider_ReturnsFactsWhenTheBaseURLMatches(t *testing.T) {
	facts, err := fixtureSource(t).FactsForProvider(context.Background(), ClinePassID, ClinePassBaseURL)
	if err != nil {
		t.Fatalf("FactsForProvider: %v", err)
	}
	if _, ok := facts["cline-pass/kimi-k3"]; !ok {
		t.Fatalf("facts = %v, want the cline-pass entries", facts)
	}
}

func TestFactsForProvider_RefusesOnBaseURLMismatch(t *testing.T) {
	facts, err := fixtureSource(t).FactsForProvider(context.Background(), ClinePassID, "https://impostor.example.com")
	if err != nil {
		t.Fatalf("FactsForProvider returned an error; a refusal is empty facts, not a failure: %v", err)
	}
	if len(facts) != 0 {
		t.Fatalf("facts = %v, want empty — the dataset api host did not match our base URL, so the key must be refused", facts)
	}
}

func TestFactsForProvider_SkipsTheCheckWhenTheDatasetDeclaresNoAPI(t *testing.T) {
	// anthropic carries no `api` key in the real dataset. The explicit map is
	// then the only evidence and the check cannot run — it must not refuse.
	facts, err := fixtureSource(t).FactsForProvider(context.Background(), ClaudeCodeID, ClaudeCodeAPIBase)
	if err != nil {
		t.Fatalf("FactsForProvider: %v", err)
	}
	if _, ok := facts["claude-sonnet-4-6"]; !ok {
		t.Fatalf("facts = %v, want the anthropic entries", facts)
	}
}

func TestFactsForProvider_UnmappedProviderYieldsNoFacts(t *testing.T) {
	facts, err := fixtureSource(t).FactsForProvider(context.Background(), AgnesAIID, AgnesAIBaseURL)
	if err != nil {
		t.Fatalf("FactsForProvider: %v", err)
	}
	if len(facts) != 0 {
		t.Fatalf("facts = %v, want empty for an unmapped provider", facts)
	}
}
