package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/execution"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
)

// Vocabulary sync test: every providers.TransportKind constant must have an
// identical string value to the corresponding execution.TransportType
// constant. These two types are intentionally duplicated across separate
// packages (providers may never import execution); this test is the compile-
// time+runtime guard that catches any drift before it reaches the
// BuildProbeTransportMaps cast.
func TestTransportKindVocabularySyncWithTransportType(t *testing.T) {
	pairs := []struct {
		kind providers.TransportKind
		typ  execution.TransportType
		name string
	}{
		{providers.TransportKindBifrost, execution.TransportTypeBifrost, "bifrost"},
		{providers.TransportKindNativeAPI, execution.TransportTypeNativeAPI, "native_api"},
		{providers.TransportKindNativeOAuth, execution.TransportTypeNativeOAuth, "native_oauth"},
		{providers.TransportKindOpenAICompatible, execution.TransportTypeOpenAICompatible, "openai_compatible"},
		{providers.TransportKindCustom, execution.TransportTypeCustom, "custom"},
	}
	for _, p := range pairs {
		if string(p.kind) != string(p.typ) {
			t.Errorf("vocabulary drift: providers.TransportKind(%q) = %q, execution.TransportType(%q) = %q — they must be identical",
				p.name, p.kind, p.name, p.typ)
		}
	}
}

// TestWireSchemaVocabularySync proves providers.WireSchema and
// execution.WireSchema carry byte-identical string values (P7-EXEC-001 part 2).
// The two are duplicated across packages (providers may never import
// execution); this is the guard the schemaStampingTransport's string cast
// relies on.
func TestWireSchemaVocabularySync(t *testing.T) {
	pairs := []struct {
		p providers.WireSchema
		e execution.WireSchema
	}{
		{providers.WireSchemaGoogleGenerateContent, execution.WireSchemaGoogleGenerateContent},
		{providers.WireSchemaAnthropicMessages, execution.WireSchemaAnthropicMessages},
		{providers.WireSchemaOpenAIChat, execution.WireSchemaOpenAIChat},
	}
	for _, p := range pairs {
		if string(p.p) != string(p.e) {
			t.Errorf("wire-schema drift: providers %q vs execution %q — must be identical", p.p, p.e)
		}
	}
}

// TestSchemaStampingTransport_StampsFromRegistry is mutation row 7: the
// decorator fills route.WireSchema from the provider's REGISTRY Definition, not
// a literal. A provider registered as anthropic_messages must reach the wire at
// /v1/messages; if the stamp used a literal (e.g. google) it would hit
// :generateContent instead. This asserts over the PRODUCTION decorator, and the
// route handed in carries NO schema (proving the decorator supplied it).
func TestSchemaStampingTransport_StampsFromRegistry(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
	}))
	t.Cleanup(srv.Close)

	reg := providers.NewRegistry()
	if err := reg.Register(providers.Definition{
		ID:         "p-anthropic",
		AuthMode:   providers.AuthModeOAuth,
		Transport:  providers.TransportKindNativeOAuth,
		WireSchema: providers.WireSchemaAnthropicMessages,
		OAuth:      resolverFakeOAuthAdapter{},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	stamped := newSchemaStampingTransport(reg, execution.NewNativeOAuthTransport(&http.Client{}, 5*time.Second))
	// Route carries NO WireSchema — the decorator must supply it from the registry.
	_, err := stamped.Execute(context.Background(), execution.ResolvedRoute{
		Provider: "p-anthropic", ModelID: "claude-x", BaseURL: srv.URL,
		Credential: execution.StoredCredentials{Value: "tok"},
	}, execution.NormalizedRequest{Operation: execution.OperationChat, Messages: []execution.Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if gotPath != "/v1/messages" {
		t.Fatalf("request path = %q, want /v1/messages (anthropic schema resolved from the registry)", gotPath)
	}
}

// resolverFakeOAuthAdapter is a no-op OAuthAdapter for wiring tests.
type resolverFakeOAuthAdapter struct{}

func (resolverFakeOAuthAdapter) BeginOAuth(context.Context, string, string, string) (string, error) {
	return "", nil
}
func (resolverFakeOAuthAdapter) CompleteOAuth(context.Context, string, string, string) (providers.IdentityResult, providers.StoredCredentials, error) {
	return providers.IdentityResult{}, providers.StoredCredentials{}, nil
}
func (resolverFakeOAuthAdapter) RefreshCredentials(context.Context, providers.StoredCredentials) (providers.StoredCredentials, error) {
	return providers.StoredCredentials{}, nil
}

// stubInferenceTransport satisfies execution.InferenceTransport for wiring in
// resolver tests; none of its methods should be called (this stub is only used
// to prove that BuildProbeTransportMaps places the correct instance in the output maps).
type stubInferenceTransport struct{ id string }

var errStubNotImplemented = errors.New("stub transport: method not implemented in resolver tests")

func (s *stubInferenceTransport) Execute(_ context.Context, _ execution.ResolvedRoute, _ execution.NormalizedRequest) (*execution.NormalizedResponse, error) {
	return nil, errStubNotImplemented
}
func (s *stubInferenceTransport) Stream(_ context.Context, _ execution.ResolvedRoute, _ execution.NormalizedRequest) (<-chan execution.Chunk, error) {
	return nil, errStubNotImplemented
}
func (s *stubInferenceTransport) Cancel(_ context.Context, _ execution.ResolvedRoute, _ string) error {
	return errStubNotImplemented
}
func (s *stubInferenceTransport) NormalizeError(_ error, _ execution.ResolvedRoute) execution.VenomError {
	return execution.VenomError{Code: "stub"}
}
func (s *stubInferenceTransport) Failure(_ error, _ execution.ResolvedRoute) execution.TypedFailure {
	return execution.TypedFailure{}
}
func (s *stubInferenceTransport) SupportedCapabilities(_ execution.ResolvedRoute) []execution.Operation {
	return nil
}

var _ execution.InferenceTransport = (*stubInferenceTransport)(nil)

// resolverFakeAPIKeyAdapter is a no-op APIKeyAdapter for wiring test providers
// that need an AuthModeAPIKey but whose ConnectAPIKey behavior is irrelevant.
type resolverFakeAPIKeyAdapter struct{}

func (resolverFakeAPIKeyAdapter) ConnectAPIKey(_ context.Context, _ string) (providers.IdentityResult, providers.StoredCredentials, error) {
	return providers.IdentityResult{}, providers.StoredCredentials{}, nil
}

// registerTestProvider registers a minimal provider into reg; it is a test
// helper that must only be called from test functions.
func registerTestProvider(t *testing.T, reg *providers.Registry, id providers.ProviderID, kind providers.TransportKind) {
	t.Helper()
	// native_oauth now requires a declared WireSchema (P7-EXEC-001 part 2);
	// any other transport must leave it empty.
	var schema providers.WireSchema
	if kind == providers.TransportKindNativeOAuth {
		schema = providers.WireSchemaGoogleGenerateContent
	}
	err := reg.Register(providers.Definition{
		ID:         id,
		AuthMode:   providers.AuthModeAPIKey,
		Transport:  kind,
		WireSchema: schema,
		APIKey:     resolverFakeAPIKeyAdapter{},
	})
	if err != nil {
		t.Fatalf("Register(%q): %v", id, err)
	}
}

// TestBuildProbeTransportMaps_HappyPath is mutation row 2.1: two providers with
// different transport kinds, both wired, both present in baseURLs →
// returns two-element maps with the correct transport instances and URLs.
func TestBuildProbeTransportMaps_HappyPath(t *testing.T) {
	reg := providers.NewRegistry()
	registerTestProvider(t, reg, "p-openai", providers.TransportKindOpenAICompatible)
	registerTestProvider(t, reg, "p-oauth", providers.TransportKindNativeOAuth)

	implOpenAI := &stubInferenceTransport{id: "openai-compat"}
	implOAuth := &stubInferenceTransport{id: "native-oauth"}
	impls := map[execution.TransportType]execution.InferenceTransport{
		execution.TransportTypeOpenAICompatible: implOpenAI,
		execution.TransportTypeNativeOAuth:      implOAuth,
	}
	baseURLs := map[providers.ProviderID]string{
		"p-openai": "https://api.openai.com/v1",
		"p-oauth":  "https://generativelanguage.googleapis.com/v1beta",
	}

	transports, urls, err := BuildProbeTransportMaps(reg, impls, baseURLs)
	if err != nil {
		t.Fatalf("BuildProbeTransportMaps() error = %v, want nil", err)
	}
	if len(transports) != 2 {
		t.Fatalf("transports length = %d, want 2", len(transports))
	}
	if transports["p-openai"] != implOpenAI {
		t.Fatalf("transports[p-openai] != implOpenAI — wrong instance for openai-compat provider")
	}
	if transports["p-oauth"] != implOAuth {
		t.Fatalf("transports[p-oauth] != implOAuth — wrong instance for native-oauth provider")
	}
	if urls["p-openai"] != "https://api.openai.com/v1" {
		t.Fatalf("urls[p-openai] = %q, want %q", urls["p-openai"], "https://api.openai.com/v1")
	}
	if urls["p-oauth"] != "https://generativelanguage.googleapis.com/v1beta" {
		t.Fatalf("urls[p-oauth] = %q, want %q", urls["p-oauth"], "https://generativelanguage.googleapis.com/v1beta")
	}
}

// TestBuildProbeTransportMaps_EmptyBaseURLs is mutation row 2.2: passing an empty
// baseURLs map returns two empty (non-nil) maps with no error — the caller
// simply wires no providers, which is a valid state for ControlMux before
// any transports are ready.
func TestBuildProbeTransportMaps_EmptyBaseURLs(t *testing.T) {
	reg := providers.NewRegistry()
	impls := map[execution.TransportType]execution.InferenceTransport{}
	baseURLs := map[providers.ProviderID]string{}

	transports, urls, err := BuildProbeTransportMaps(reg, impls, baseURLs)
	if err != nil {
		t.Fatalf("BuildProbeTransportMaps() error = %v, want nil", err)
	}
	if transports == nil {
		t.Fatal("transports = nil, want non-nil empty map")
	}
	if urls == nil {
		t.Fatal("urls = nil, want non-nil empty map")
	}
}

// TestBuildProbeTransportMaps_RejectsUnregisteredProvider is mutation row 2.3: a
// provider ID that appears in baseURLs but is not registered in reg must
// return an error — fail-closed, never a silent absent-transport default.
func TestBuildProbeTransportMaps_RejectsUnregisteredProvider(t *testing.T) {
	reg := providers.NewRegistry()
	impls := map[execution.TransportType]execution.InferenceTransport{
		execution.TransportTypeOpenAICompatible: &stubInferenceTransport{},
	}
	baseURLs := map[providers.ProviderID]string{
		"not-registered": "https://example.com",
	}

	_, _, err := BuildProbeTransportMaps(reg, impls, baseURLs)
	if err == nil {
		t.Fatal("BuildProbeTransportMaps() error = nil, want rejection for unregistered provider")
	}
}

// TestBuildProbeTransportMaps_RejectsProviderWithNoImplementation is mutation row
// 2.4: a provider is registered with a valid TransportKind but the impls
// map has no entry for that kind → error. This enforces the fail-closed
// invariant: a provider never silently falls through to an absent transport.
func TestBuildProbeTransportMaps_RejectsProviderWithNoImplementation(t *testing.T) {
	reg := providers.NewRegistry()
	registerTestProvider(t, reg, "p-bifrost", providers.TransportKindBifrost)

	// Impls map does NOT include bifrost.
	impls := map[execution.TransportType]execution.InferenceTransport{
		execution.TransportTypeOpenAICompatible: &stubInferenceTransport{},
	}
	baseURLs := map[providers.ProviderID]string{
		"p-bifrost": "https://bifrost.example.com",
	}

	_, _, err := BuildProbeTransportMaps(reg, impls, baseURLs)
	if err == nil {
		t.Fatal("BuildProbeTransportMaps() error = nil, want rejection when impl is missing for transport kind")
	}
}

// TestBuildProbeTransportMaps_AbsentProviderIsNotWired is mutation row 2.5: a
// provider registered in reg but NOT in baseURLs must simply be absent
// from the returned maps — it is not an error (the caller decided not to
// wire it). Available() returns false for it, which is the correct
// probe_unsupported refusal path.
func TestBuildProbeTransportMaps_AbsentProviderIsNotWired(t *testing.T) {
	reg := providers.NewRegistry()
	registerTestProvider(t, reg, "p-wired", providers.TransportKindOpenAICompatible)
	registerTestProvider(t, reg, "p-unwired", providers.TransportKindNativeOAuth)

	impl := &stubInferenceTransport{id: "openai-compat"}
	impls := map[execution.TransportType]execution.InferenceTransport{
		execution.TransportTypeOpenAICompatible: impl,
		execution.TransportTypeNativeOAuth:      &stubInferenceTransport{id: "oauth"},
	}
	// p-unwired is NOT in baseURLs.
	baseURLs := map[providers.ProviderID]string{
		"p-wired": "https://example.com/v1",
	}

	transports, _, err := BuildProbeTransportMaps(reg, impls, baseURLs)
	if err != nil {
		t.Fatalf("BuildProbeTransportMaps() error = %v, want nil", err)
	}
	if _, ok := transports["p-unwired"]; ok {
		t.Fatal("transports[p-unwired] is present — a provider absent from baseURLs must not appear in the result")
	}
	if transports["p-wired"] != impl {
		t.Fatal("transports[p-wired] is missing or wrong — wired provider must appear in the result")
	}
}

// TestRegistryTransportResolver_ResolvesDeclaredKind proves THE
// TransportTypeResolver implementation (governor fix D1 — 01 §4.4): a
// registered provider resolves to its catalog-declared type; an
// unregistered provider is a typed fail-closed error, never a default.
func TestRegistryTransportResolver_ResolvesDeclaredKind(t *testing.T) {
	reg := providers.NewRegistry()
	registerTestProvider(t, reg, "p-oauth", providers.TransportKindNativeOAuth)
	resolver := NewRegistryTransportResolver(reg)

	tt, err := resolver.TransportTypeFor(execution.ResolvedRoute{Provider: "p-oauth"})
	if err != nil {
		t.Fatalf("TransportTypeFor(registered) error = %v, want nil", err)
	}
	if tt != execution.TransportTypeNativeOAuth {
		t.Fatalf("TransportTypeFor = %q, want %q", tt, execution.TransportTypeNativeOAuth)
	}

	if _, err := resolver.TransportTypeFor(execution.ResolvedRoute{Provider: "ghost"}); !errors.Is(err, ErrProviderTransportUnresolvable) {
		t.Fatalf("TransportTypeFor(unregistered) error = %v, want ErrProviderTransportUnresolvable", err)
	}
}

// TestBuildInferenceDispatcher_DispatchesByCatalogDeclaredType is the
// end-to-end proof the P4-EXEC-001 card asks for: a Dispatcher composed
// from the registry sends an openai_compatible provider's route to the
// OpenAI-shaped server and a native_oauth provider's route to the
// Gemini-shaped server — selection by typed capability, observed via
// which httptest server was actually hit; and a route whose provider is
// unregistered is rejected as unresolvable, never defaulted.
func TestBuildInferenceDispatcher_DispatchesByCatalogDeclaredType(t *testing.T) {
	var openaiHits, geminiHits int
	openaiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		openaiHits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(openaiSrv.Close)
	geminiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		geminiHits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`))
	}))
	t.Cleanup(geminiSrv.Close)

	reg := providers.NewRegistry()
	registerTestProvider(t, reg, "p-zen", providers.TransportKindOpenAICompatible)
	registerTestProvider(t, reg, "p-anti", providers.TransportKindNativeOAuth)

	d := BuildInferenceDispatcher(reg, map[execution.TransportType]execution.InferenceTransport{
		execution.TransportTypeOpenAICompatible: execution.NewOpenAICompatibleTransport(&http.Client{}, 5*time.Second),
		execution.TransportTypeNativeOAuth:      execution.NewNativeOAuthTransport(&http.Client{}, 5*time.Second),
	})

	req := execution.NormalizedRequest{Operation: execution.OperationChat, Messages: []execution.Message{{Role: "user", Content: "hi"}}}
	if _, err := d.Execute(context.Background(), execution.ResolvedRoute{
		Provider: "p-zen", ModelID: "m1", BaseURL: openaiSrv.URL,
		Credential: execution.StoredCredentials{Value: "k"},
	}, req); err != nil {
		t.Fatalf("Execute(p-zen) error = %v, want nil", err)
	}
	if _, err := d.Execute(context.Background(), execution.ResolvedRoute{
		Provider: "p-anti", ModelID: "m2", BaseURL: geminiSrv.URL,
		Credential: execution.StoredCredentials{Value: "tok"},
		WireSchema: execution.WireSchemaGoogleGenerateContent,
	}, req); err != nil {
		t.Fatalf("Execute(p-anti) error = %v, want nil", err)
	}
	if openaiHits != 1 || geminiHits != 1 {
		t.Fatalf("server hits = openai %d / gemini %d, want 1 / 1 (typed dispatch must pick the declared transport)", openaiHits, geminiHits)
	}

	// Fail closed: unregistered provider never dispatches anywhere.
	if _, err := d.Execute(context.Background(), execution.ResolvedRoute{
		Provider: "ghost", ModelID: "m3", BaseURL: openaiSrv.URL,
	}, req); !errors.Is(err, ErrProviderTransportUnresolvable) {
		t.Fatalf("Execute(ghost) error = %v, want ErrProviderTransportUnresolvable", err)
	}
	if openaiHits != 1 {
		t.Fatalf("openai hits after ghost dispatch = %d, want still 1 (no default transport)", openaiHits)
	}

	// Fail closed: declared type with no wired implementation.
	registerTestProvider(t, reg, "p-custom", providers.TransportKindCustom)
	if _, err := d.Execute(context.Background(), execution.ResolvedRoute{
		Provider: "p-custom", ModelID: "m4", BaseURL: openaiSrv.URL,
	}, req); !errors.Is(err, execution.ErrUnresolvableRoute) {
		t.Fatalf("Execute(p-custom) error = %v, want execution.ErrUnresolvableRoute", err)
	}
}
