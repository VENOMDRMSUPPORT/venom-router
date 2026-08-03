package providers

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeChatProbe is a providers.ChatProbe returning a fixed status/err.
type fakeChatProbe struct {
	status int
	err    error
}

func (f *fakeChatProbe) probe(_ context.Context, _ /*baseURL*/, _ /*key*/ string) (int, error) {
	return f.status, f.err
}

const agnesModelsList = `{"data":[
  {"id":"chat-model","name":"Chat","capabilities":["tools","structured_output","telepathy"],"context_window":8000},
  {"id":"vid-model","capabilities":["video"]},
  {"id":"obj-model","capabilities":{"vision":true,"tools":false}},
  {"id":"ctxalt-model","context_length":4096},
  {"id":"ctxlen3-model","max_model_len":2048},
  {"id":"noctx-model"},
  {"id":"legacy-agnes-video-x"},
  {"id":"kept-video","capabilities":["tools"]},
  {"id":"unreadable-caps-video","capabilities":"tools,video"}
]}`

func newAgnesAdapter(chat *fakeChatProbe, models *fakeModelsProbe) *AgnesAIAdapter {
	return NewAgnesAIAdapter(chat.probe, models.probe, frozenClock())
}

// TestAgnesAI_BaseURLMatchesCatalog proves the base URL const equals the
// BuiltinCatalog entry.
func TestAgnesAI_BaseURLMatchesCatalog(t *testing.T) {
	var entry CatalogEntry
	for _, e := range BuiltinCatalog() {
		if e.ID == AgnesAIID {
			entry = e
		}
	}
	if entry.BaseURL != AgnesAIBaseURL {
		t.Fatalf("AgnesAIBaseURL = %q, catalog BaseURL = %q — they must match", AgnesAIBaseURL, entry.BaseURL)
	}
}

// TestAgnesAI_ThreeWayClassification is mutation row 3 at the adapter level:
// valid -> identity, invalid -> ErrInvalidCredential, unavailable ->
// ErrProviderUnavailable.
func TestAgnesAI_ThreeWayClassification(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		a := newAgnesAdapter(&fakeChatProbe{status: 200}, &fakeModelsProbe{})
		res, _, err := a.ConnectAPIKey(context.Background(), "k")
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
		if res.ExternalID == "" || res.Plan != agnesSyntheticPlan {
			t.Fatalf("identity = %+v, want fingerprint + synthetic Free", res)
		}
	})
	t.Run("invalid", func(t *testing.T) {
		a := newAgnesAdapter(&fakeChatProbe{status: 401}, &fakeModelsProbe{})
		if _, _, err := a.ConnectAPIKey(context.Background(), "k"); !errors.Is(err, ErrInvalidCredential) {
			t.Fatalf("error = %v, want ErrInvalidCredential", err)
		}
	})
	t.Run("unavailable", func(t *testing.T) {
		a := newAgnesAdapter(&fakeChatProbe{err: errors.New("boom")}, &fakeModelsProbe{})
		if _, _, err := a.ConnectAPIKey(context.Background(), "k"); !errors.Is(err, ErrProviderUnavailable) {
			t.Fatalf("error = %v, want ErrProviderUnavailable", err)
		}
	})
}

// TestAgnesAI_FundingStaysEmpty is mutation row 2: the synthetic Free plan must
// not leak into Funding (evidence_required — the label is not evidence).
func TestAgnesAI_FundingStaysEmpty(t *testing.T) {
	a := newAgnesAdapter(&fakeChatProbe{status: 200}, &fakeModelsProbe{})
	res, _, err := a.ConnectAPIKey(context.Background(), "k")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if res.Plan != "Free" {
		t.Fatalf("Plan = %q, want the synthetic Free label", res.Plan)
	}
	if res.Funding != "" {
		t.Fatalf("Funding = %q, want empty (evidence_required; a Free label is not evidence)", res.Funding)
	}
}

// TestAgnesAI_FingerprintStableAcrossWhitespace is mutation row 6: two
// whitespace variants of one key produce the SAME fingerprint (the fingerprint
// is computed over the normalized key).
func TestAgnesAI_FingerprintStableAcrossWhitespace(t *testing.T) {
	a := newAgnesAdapter(&fakeChatProbe{status: 200}, &fakeModelsProbe{})
	r1, _, _ := a.ConnectAPIKey(context.Background(), "sk-agnes  key")
	r2, _, _ := a.ConnectAPIKey(context.Background(), "  sk-agnes key  ")
	if r1.ExternalID == "" || r1.ExternalID != r2.ExternalID {
		t.Fatalf("fingerprints differ across whitespace: %q vs %q", r1.ExternalID, r2.ExternalID)
	}
}

// TestAgnesAI_Discovery covers mutation rows 1 (video drop), 4 (context
// spellings), and 5 (object-form capabilities): video dropped by the explicit
// route, the id-shape fallback only when no capability field, the three
// context spellings resolved, and only `true` object keys counted.
func TestAgnesAI_Discovery(t *testing.T) {
	a := newAgnesAdapter(&fakeChatProbe{status: 200}, &fakeModelsProbe{body: agnesModelsList})
	models, err := a.DiscoverModels(context.Background(), StoredCredentials{Value: "k"})
	if err != nil {
		t.Fatalf("DiscoverModels() error = %v", err)
	}
	byID := map[string]DiscoveredModel{}
	for _, m := range models {
		byID[m.ProviderModelID] = m
	}

	if _, ok := byID["vid-model"]; ok {
		t.Fatal("vid-model (explicit video capability) must be dropped")
	}
	if _, ok := byID["legacy-agnes-video-x"]; ok {
		t.Fatal("legacy-agnes-video-x (no capability field, video id-shape) must be dropped")
	}
	// kept-video ends in a video-ish token but DECLARES a capability field with
	// no video label, so the id-shape fallback must NOT fire.
	if _, ok := byID["kept-video"]; !ok {
		t.Fatal("kept-video declares capabilities (no video) and must be kept")
	}
	// A capabilities field we could not PARSE (here: a bare string, neither
	// array nor object) is not authority that the row is non-video — it is the
	// absence of usable information, so the id-shape fallback must still fire.
	// Treating unreadable data as "confirmed fine" would be failing OPEN.
	if _, ok := byID["unreadable-caps-video"]; ok {
		t.Fatal("unreadable-caps-video: an unparseable capabilities field must not suppress the video id-shape drop")
	}

	chat := byID["chat-model"]
	if !hasAll(chat.Capabilities, "chat", "tools", "structured_output") {
		t.Fatalf("chat-model caps = %v, want chat/tools/structured_output", chat.Capabilities)
	}
	for _, c := range chat.Capabilities {
		if c == "telepathy" {
			t.Fatal("an out-of-vocabulary label (telepathy) must be dropped, not surfaced")
		}
	}
	if chat.ContextLength == nil || *chat.ContextLength != 8000 {
		t.Fatalf("chat-model ctx = %v, want 8000 (context_window)", chat.ContextLength)
	}

	// object form: only vision (true) counts, not tools (false).
	obj := byID["obj-model"]
	if !hasAll(obj.Capabilities, "chat", "vision") {
		t.Fatalf("obj-model caps = %v, want chat/vision", obj.Capabilities)
	}
	for _, c := range obj.Capabilities {
		if c == "tools" {
			t.Fatal("obj-model tools was false in the object form and must NOT be enabled")
		}
	}

	// alternative context spellings.
	if v := byID["ctxalt-model"].ContextLength; v == nil || *v != 4096 {
		t.Fatalf("ctxalt-model ctx = %v, want 4096 (context_length alias)", v)
	}
	if v := byID["ctxlen3-model"].ContextLength; v == nil || *v != 2048 {
		t.Fatalf("ctxlen3-model ctx = %v, want 2048 (max_model_len alias)", v)
	}
	if v := byID["noctx-model"].ContextLength; v != nil {
		t.Fatalf("noctx-model ctx = %v, want nil (no context field)", v)
	}
}

// TestAgnesAI_KeyNeverInError proves the credential never appears in a returned
// error string.
func TestAgnesAI_KeyNeverInError(t *testing.T) {
	const secret = "sk-agnes-super-secret"
	a := newAgnesAdapter(&fakeChatProbe{status: 401}, &fakeModelsProbe{})
	_, _, err := a.ConnectAPIKey(context.Background(), secret)
	if err != nil && strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaks the credential: %v", err)
	}
}
