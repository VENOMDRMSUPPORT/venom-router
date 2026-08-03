package providers

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeGoogleProbe is a GoogleModelsProbe. It serves pages keyed by the
// pageToken it receives (so paging can be exercised) and records the tokens it
// was called with. A nil `pages` entry for a token is a miss (empty body).
type fakeGoogleProbe struct {
	status    int
	err       error
	pages     map[string]string // pageToken -> response body
	gotTokens []string
	callCount int
}

func (f *fakeGoogleProbe) probe(_ context.Context, _ /*baseURL*/, _ /*key*/, pageToken string) (int, []byte, error) {
	f.callCount++
	f.gotTokens = append(f.gotTokens, pageToken)
	if f.err != nil {
		return 0, nil, f.err
	}
	body := f.pages[pageToken]
	return f.status, []byte(body), nil
}

func newGeminiAdapter(p *fakeGoogleProbe) *GeminiCLIAdapter {
	return NewGeminiCLIAdapter(p.probe, frozenClock())
}

// TestGeminiCLI_BaseURLMatchesCatalog proves the base URL const equals the
// catalog entry.
func TestGeminiCLI_BaseURLMatchesCatalog(t *testing.T) {
	var entry CatalogEntry
	for _, e := range BuiltinCatalog() {
		if e.ID == GeminiCLIID {
			entry = e
		}
	}
	if entry.BaseURL != GeminiCLIBaseURL {
		t.Fatalf("GeminiCLIBaseURL = %q, catalog BaseURL = %q — they must match", GeminiCLIBaseURL, entry.BaseURL)
	}
}

// TestGeminiCLI_TransportKindIsNativeAPI proves the adapter registers with the
// native_api transport kind (paired with the httpapi wiring test).
func TestGeminiCLI_TransportKindIsNativeAPI(t *testing.T) {
	reg := NewRegistry()
	if err := RegisterGeminiCLI(reg, (&fakeGoogleProbe{}).probe, nil); err != nil {
		t.Fatalf("RegisterGeminiCLI() error = %v", err)
	}
	def, ok := reg.Definition(GeminiCLIID)
	if !ok {
		t.Fatal("gemini-cli not registered")
	}
	if def.Transport != TransportKindNativeAPI {
		t.Fatalf("Transport = %q, want %q", def.Transport, TransportKindNativeAPI)
	}
}

// TestGeminiCLI_ThreeWayClassification proves valid/invalid/unavailable via the
// listing status, and funding stays "" despite the synthetic Free plan.
func TestGeminiCLI_ThreeWayClassification(t *testing.T) {
	t.Run("valid + funding empty", func(t *testing.T) {
		a := newGeminiAdapter(&fakeGoogleProbe{status: 200, pages: map[string]string{"": `{"models":[]}`}})
		res, _, err := a.ConnectAPIKey(context.Background(), "k")
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if res.ExternalID == "" || res.Plan != geminiSyntheticPlan {
			t.Fatalf("identity = %+v, want fingerprint + synthetic Free", res)
		}
		if res.Funding != "" {
			t.Fatalf("Funding = %q, want empty (evidence_required; Free is not evidence)", res.Funding)
		}
	})
	t.Run("invalid", func(t *testing.T) {
		a := newGeminiAdapter(&fakeGoogleProbe{status: 403})
		if _, _, err := a.ConnectAPIKey(context.Background(), "k"); !errors.Is(err, ErrInvalidCredential) {
			t.Fatalf("error = %v, want ErrInvalidCredential", err)
		}
	})
	t.Run("unavailable", func(t *testing.T) {
		a := newGeminiAdapter(&fakeGoogleProbe{status: 500})
		if _, _, err := a.ConnectAPIKey(context.Background(), "k"); !errors.Is(err, ErrProviderUnavailable) {
			t.Fatalf("error = %v, want ErrProviderUnavailable", err)
		}
	})
}

// TestGeminiCLI_PrefixStrippedAndFiltered covers the prefix strip, the
// generateContent gate, the family exclusions, and that capabilities are
// EXACTLY ["chat"] even for a thinking/multimodal-described row.
func TestGeminiCLI_PrefixStrippedAndFiltered(t *testing.T) {
	page := `{"models":[
	  {"name":"models/gemini-x","displayName":"Gemini X","supportedGenerationMethods":["generateContent","countTokens"],"inputTokenLimit":1000000,"outputTokenLimit":8192},
	  {"name":"models/gemini-x-tts","supportedGenerationMethods":["generateContent"]},
	  {"name":"models/imagen-4.0-image-preview","supportedGenerationMethods":["generateContent"]},
	  {"name":"models/gemini-native-audio-dialog","supportedGenerationMethods":["generateContent"]},
	  {"name":"models/gemini-2.0-live-preview","supportedGenerationMethods":["generateContent"]},
	  {"name":"models/text-embedding-004","supportedGenerationMethods":["embedContent"]}
	]}`
	a := newGeminiAdapter(&fakeGoogleProbe{status: 200, pages: map[string]string{"": page}})
	models, err := a.DiscoverModels(context.Background(), StoredCredentials{Value: "k"})
	if err != nil {
		t.Fatalf("DiscoverModels() error = %v", err)
	}
	if len(models) != 1 {
		ids := []string{}
		for _, m := range models {
			ids = append(ids, m.ProviderModelID)
		}
		t.Fatalf("survivors = %v, want exactly [gemini-x]", ids)
	}
	m := models[0]
	if m.ProviderModelID != "gemini-x" {
		t.Fatalf("id = %q, want gemini-x (models/ prefix stripped)", m.ProviderModelID)
	}
	if len(m.Capabilities) != 1 || m.Capabilities[0] != "chat" {
		t.Fatalf("caps = %v, want exactly [chat]", m.Capabilities)
	}
	if m.ContextLength == nil || *m.ContextLength != 1000000 || m.MaxInputTokens == nil || *m.MaxInputTokens != 1000000 {
		t.Fatalf("context/input = %v/%v, want both 1000000 (from inputTokenLimit)", m.ContextLength, m.MaxInputTokens)
	}
	if m.MaxOutputTokens == nil || *m.MaxOutputTokens != 8192 {
		t.Fatalf("output = %v, want 8192", m.MaxOutputTokens)
	}
}

// TestGeminiCLI_AbsentLimitsStayNil proves an absent input/output limit stays
// nil (never 0).
func TestGeminiCLI_AbsentLimitsStayNil(t *testing.T) {
	page := `{"models":[{"name":"models/gemini-nolimits","supportedGenerationMethods":["generateContent"]}]}`
	a := newGeminiAdapter(&fakeGoogleProbe{status: 200, pages: map[string]string{"": page}})
	models, err := a.DiscoverModels(context.Background(), StoredCredentials{Value: "k"})
	if err != nil {
		t.Fatalf("DiscoverModels() error = %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("survivors = %d, want 1", len(models))
	}
	m := models[0]
	if m.ContextLength != nil || m.MaxInputTokens != nil || m.MaxOutputTokens != nil {
		t.Fatalf("limits = %v/%v/%v, want all nil (absent limits are nil, never 0)", m.ContextLength, m.MaxInputTokens, m.MaxOutputTokens)
	}
}

// TestGeminiCLI_MethodlessRowFilteredOut proves a row without generateContent
// is filtered out, never defaulted to chat.
func TestGeminiCLI_MethodlessRowFilteredOut(t *testing.T) {
	page := `{"models":[{"name":"models/no-method","supportedGenerationMethods":["countTokens"]}]}`
	a := newGeminiAdapter(&fakeGoogleProbe{status: 200, pages: map[string]string{"": page}})
	models, err := a.DiscoverModels(context.Background(), StoredCredentials{Value: "k"})
	if err != nil {
		t.Fatalf("DiscoverModels() error = %v", err)
	}
	if len(models) != 0 {
		t.Fatalf("survivors = %v, want none (a method-less row is filtered, not defaulted to chat)", models)
	}
}

// TestGeminiCLI_Paging proves a two-page fixture yields the union, the second
// request carries pageToken == page one's nextPageToken, and a one-page fixture
// makes exactly one request.
func TestGeminiCLI_Paging(t *testing.T) {
	t.Run("two pages union + token threading", func(t *testing.T) {
		pages := map[string]string{
			"":     `{"models":[{"name":"models/a","supportedGenerationMethods":["generateContent"]}],"nextPageToken":"tok2"}`,
			"tok2": `{"models":[{"name":"models/b","supportedGenerationMethods":["generateContent"]}]}`,
		}
		p := &fakeGoogleProbe{status: 200, pages: pages}
		models, err := newGeminiAdapter(p).DiscoverModels(context.Background(), StoredCredentials{Value: "k"})
		if err != nil {
			t.Fatalf("DiscoverModels() error = %v", err)
		}
		if len(models) != 2 {
			t.Fatalf("survivors = %d, want 2 (union of both pages)", len(models))
		}
		if len(p.gotTokens) != 2 || p.gotTokens[0] != "" || p.gotTokens[1] != "tok2" {
			t.Fatalf("tokens = %v, want ['', 'tok2'] (second request carries page one's nextPageToken)", p.gotTokens)
		}
	})
	t.Run("one page = one request", func(t *testing.T) {
		p := &fakeGoogleProbe{status: 200, pages: map[string]string{"": `{"models":[]}`}}
		if _, err := newGeminiAdapter(p).DiscoverModels(context.Background(), StoredCredentials{Value: "k"}); err != nil {
			t.Fatalf("error = %v", err)
		}
		if p.callCount != 1 {
			t.Fatalf("callCount = %d, want 1", p.callCount)
		}
	})
}

// TestGeminiCLI_RunawayPagerHitsBound proves a never-ending pager hits the page
// budget and returns the typed error rather than looping forever.
func TestGeminiCLI_RunawayPagerHitsBound(t *testing.T) {
	// Every page returns a nextPageToken, so the loop never terminates naturally.
	p := &neverEndingGoogleProbe{}
	_, err := NewGeminiCLIAdapter(p.probe, frozenClock()).DiscoverModels(context.Background(), StoredCredentials{Value: "k"})
	if !errors.Is(err, ErrGeminiPagingBudgetExceeded) {
		t.Fatalf("error = %v, want ErrGeminiPagingBudgetExceeded", err)
	}
	if p.calls != maxGeminiListPages {
		t.Fatalf("calls = %d, want the bound %d", p.calls, maxGeminiListPages)
	}
}

type neverEndingGoogleProbe struct{ calls int }

func (p *neverEndingGoogleProbe) probe(_ context.Context, _, _, _ string) (int, []byte, error) {
	p.calls++
	return 200, []byte(`{"models":[],"nextPageToken":"more"}`), nil
}

// TestGeminiCLI_KeyNeverInError proves the credential never appears in a
// returned error string.
func TestGeminiCLI_KeyNeverInError(t *testing.T) {
	const secret = "AIza-super-secret-key"
	a := newGeminiAdapter(&fakeGoogleProbe{status: 403})
	_, _, err := a.ConnectAPIKey(context.Background(), secret)
	if err != nil && strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaks the credential: %v", err)
	}
}
