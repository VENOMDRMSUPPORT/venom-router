package providers

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const nvidiaDataset = `{
  "nvidia": {"models": {
    "meta/llama-keep": {"name": "Llama Keep", "tool_call": true,
      "modalities": {"input": ["text"], "output": ["text"]}, "limit": {"context": 4096}},
    "dep/model": {"status": "deprecated", "modalities": {"output": ["text"]}},
    "img/gen": {"modalities": {"output": ["image"]}}
  }}
}`

const nvidiaModelsList = `{"data":[
  {"id":"meta/llama-keep"},{"id":"dep/model"},{"id":"img/gen"},{"id":"meta/uncatalogued"}
]}`

func newNvidiaAdapter(chat *fakeChatProbe, models *fakeModelsProbe, dataset ModelsDevProbe) *NvidiaNIMAdapter {
	return NewNvidiaNIMAdapter(chat.probe, models.probe, dataset, frozenClock())
}

// TestNvidiaNIM_BaseURLMatchesCatalog proves the base URL const equals the
// BuiltinCatalog entry.
func TestNvidiaNIM_BaseURLMatchesCatalog(t *testing.T) {
	var entry CatalogEntry
	for _, e := range BuiltinCatalog() {
		if e.ID == NvidiaNIMID {
			entry = e
		}
	}
	if entry.BaseURL != NvidiaNIMBaseURL {
		t.Fatalf("NvidiaNIMBaseURL = %q, catalog BaseURL = %q — they must match", NvidiaNIMBaseURL, entry.BaseURL)
	}
}

// TestNvidiaNIM_ThreeWayClassification proves valid/invalid/unavailable, and is
// mutation row 5's fail-closed check: an unrecognized status (418) is
// unavailable, never valid.
func TestNvidiaNIM_ThreeWayClassification(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		a := newNvidiaAdapter(&fakeChatProbe{status: 200}, &fakeModelsProbe{}, staticModelsDevProbe("{}", nil))
		res, _, err := a.ConnectAPIKey(context.Background(), "k")
		if err != nil || res.ExternalID == "" || res.Plan != nvidiaSyntheticPlan {
			t.Fatalf("valid: res=%+v err=%v, want fingerprint + synthetic Free", res, err)
		}
	})
	t.Run("invalid", func(t *testing.T) {
		a := newNvidiaAdapter(&fakeChatProbe{status: 401}, &fakeModelsProbe{}, staticModelsDevProbe("{}", nil))
		if _, _, err := a.ConnectAPIKey(context.Background(), "k"); !errors.Is(err, ErrInvalidCredential) {
			t.Fatalf("error = %v, want ErrInvalidCredential", err)
		}
	})
	t.Run("unrecognized status 418 is unavailable, not valid", func(t *testing.T) {
		a := newNvidiaAdapter(&fakeChatProbe{status: 418}, &fakeModelsProbe{}, staticModelsDevProbe("{}", nil))
		if _, _, err := a.ConnectAPIKey(context.Background(), "k"); !errors.Is(err, ErrProviderUnavailable) {
			t.Fatalf("error = %v, want ErrProviderUnavailable (fail closed on an unrecognized status)", err)
		}
	})
}

// TestNvidiaNIM_FundingStaysEmpty is mutation row 4: funding is "" despite the
// synthetic Free plan.
func TestNvidiaNIM_FundingStaysEmpty(t *testing.T) {
	a := newNvidiaAdapter(&fakeChatProbe{status: 200}, &fakeModelsProbe{}, staticModelsDevProbe("{}", nil))
	res, _, err := a.ConnectAPIKey(context.Background(), "k")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if res.Funding != "" {
		t.Fatalf("Funding = %q, want empty (evidence_required)", res.Funding)
	}
}

// TestNvidiaNIM_DiscoveryExactIDs is mutations row 1 (no hardcoded additions)
// and row 3 (facts read from the "nvidia" key): the surviving ids and count are
// EXACTLY the fixture's, deprecated/non-text-output are dropped, the kept model
// is enriched, and an uncatalogued id is chat-only with nil limits.
func TestNvidiaNIM_DiscoveryExactIDs(t *testing.T) {
	a := newNvidiaAdapter(&fakeChatProbe{status: 200}, &fakeModelsProbe{body: nvidiaModelsList}, staticModelsDevProbe(nvidiaDataset, nil))
	models, err := a.DiscoverModels(context.Background(), StoredCredentials{Value: "k"})
	if err != nil {
		t.Fatalf("DiscoverModels() error = %v", err)
	}
	byID := map[string]DiscoveredModel{}
	for _, m := range models {
		byID[m.ProviderModelID] = m
	}
	if len(models) != 2 {
		t.Fatalf("survivors = %d (%v), want EXACTLY 2 (meta/llama-keep, meta/uncatalogued)", len(models), byID)
	}
	if _, ok := byID["meta/llama-keep"]; !ok {
		t.Fatal("meta/llama-keep must survive")
	}
	if _, ok := byID["meta/uncatalogued"]; !ok {
		t.Fatal("meta/uncatalogued must survive (chat-only)")
	}
	keep := byID["meta/llama-keep"]
	if !hasAll(keep.Capabilities, "chat", "tools") {
		t.Fatalf("meta/llama-keep caps = %v, want chat/tools (from the nvidia key)", keep.Capabilities)
	}
	if keep.ContextLength == nil || *keep.ContextLength != 4096 {
		t.Fatalf("meta/llama-keep ctx = %v, want 4096 (facts enrichment from the nvidia key)", keep.ContextLength)
	}
	uncat := byID["meta/uncatalogued"]
	if len(uncat.Capabilities) != 1 || uncat.Capabilities[0] != "chat" || uncat.ContextLength != nil {
		t.Fatalf("meta/uncatalogued = %+v, want chat-only nil limits", uncat)
	}
}

// TestNvidiaNIM_EmptyListIsAFact is mutation row 2: an empty provider list
// yields an empty result and NOT an error (an empty catalog is a fact).
func TestNvidiaNIM_EmptyListIsAFact(t *testing.T) {
	a := newNvidiaAdapter(&fakeChatProbe{status: 200}, &fakeModelsProbe{body: `{"data":[]}`}, staticModelsDevProbe(nvidiaDataset, nil))
	models, err := a.DiscoverModels(context.Background(), StoredCredentials{Value: "k"})
	if err != nil {
		t.Fatalf("DiscoverModels() error = %v, want nil for an empty list", err)
	}
	if len(models) != 0 {
		t.Fatalf("models = %v, want empty", models)
	}
}

// TestNvidiaNIM_KeyNeverInError proves the credential never appears in a
// returned error string.
func TestNvidiaNIM_KeyNeverInError(t *testing.T) {
	const secret = "nvapi-super-secret"
	a := newNvidiaAdapter(&fakeChatProbe{status: 401}, &fakeModelsProbe{}, staticModelsDevProbe("{}", nil))
	_, _, err := a.ConnectAPIKey(context.Background(), secret)
	if err != nil && strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaks the credential: %v", err)
	}
}
