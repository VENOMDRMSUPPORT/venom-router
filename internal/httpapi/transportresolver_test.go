package httpapi

import (
	"context"
	"errors"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/execution"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
)

// Vocabulary sync test: every providers.TransportKind constant must have an
// identical string value to the corresponding execution.TransportType
// constant. These two types are intentionally duplicated across separate
// packages (providers may never import execution); this test is the compile-
// time+runtime guard that catches any drift before it reaches the
// BuildDispatcher cast.
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

// stubInferenceTransport satisfies execution.InferenceTransport for wiring in
// resolver tests; none of its methods should be called (this stub is only used
// to prove that BuildDispatcher places the correct instance in the output maps).
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
	err := reg.Register(providers.Definition{
		ID:        id,
		AuthMode:  providers.AuthModeAPIKey,
		Transport: kind,
		APIKey:    resolverFakeAPIKeyAdapter{},
	})
	if err != nil {
		t.Fatalf("Register(%q): %v", id, err)
	}
}

// TestBuildDispatcher_HappyPath is mutation row 2.1: two providers with
// different transport kinds, both wired, both present in baseURLs →
// returns two-element maps with the correct transport instances and URLs.
func TestBuildDispatcher_HappyPath(t *testing.T) {
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

	transports, urls, err := BuildDispatcher(reg, impls, baseURLs)
	if err != nil {
		t.Fatalf("BuildDispatcher() error = %v, want nil", err)
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

// TestBuildDispatcher_EmptyBaseURLs is mutation row 2.2: passing an empty
// baseURLs map returns two empty (non-nil) maps with no error — the caller
// simply wires no providers, which is a valid state for ControlMux before
// any transports are ready.
func TestBuildDispatcher_EmptyBaseURLs(t *testing.T) {
	reg := providers.NewRegistry()
	impls := map[execution.TransportType]execution.InferenceTransport{}
	baseURLs := map[providers.ProviderID]string{}

	transports, urls, err := BuildDispatcher(reg, impls, baseURLs)
	if err != nil {
		t.Fatalf("BuildDispatcher() error = %v, want nil", err)
	}
	if transports == nil {
		t.Fatal("transports = nil, want non-nil empty map")
	}
	if urls == nil {
		t.Fatal("urls = nil, want non-nil empty map")
	}
}

// TestBuildDispatcher_RejectsUnregisteredProvider is mutation row 2.3: a
// provider ID that appears in baseURLs but is not registered in reg must
// return an error — fail-closed, never a silent absent-transport default.
func TestBuildDispatcher_RejectsUnregisteredProvider(t *testing.T) {
	reg := providers.NewRegistry()
	impls := map[execution.TransportType]execution.InferenceTransport{
		execution.TransportTypeOpenAICompatible: &stubInferenceTransport{},
	}
	baseURLs := map[providers.ProviderID]string{
		"not-registered": "https://example.com",
	}

	_, _, err := BuildDispatcher(reg, impls, baseURLs)
	if err == nil {
		t.Fatal("BuildDispatcher() error = nil, want rejection for unregistered provider")
	}
}

// TestBuildDispatcher_RejectsProviderWithNoImplementation is mutation row
// 2.4: a provider is registered with a valid TransportKind but the impls
// map has no entry for that kind → error. This enforces the fail-closed
// invariant: a provider never silently falls through to an absent transport.
func TestBuildDispatcher_RejectsProviderWithNoImplementation(t *testing.T) {
	reg := providers.NewRegistry()
	registerTestProvider(t, reg, "p-bifrost", providers.TransportKindBifrost)

	// Impls map does NOT include bifrost.
	impls := map[execution.TransportType]execution.InferenceTransport{
		execution.TransportTypeOpenAICompatible: &stubInferenceTransport{},
	}
	baseURLs := map[providers.ProviderID]string{
		"p-bifrost": "https://bifrost.example.com",
	}

	_, _, err := BuildDispatcher(reg, impls, baseURLs)
	if err == nil {
		t.Fatal("BuildDispatcher() error = nil, want rejection when impl is missing for transport kind")
	}
}

// TestBuildDispatcher_AbsentProviderIsNotWired is mutation row 2.5: a
// provider registered in reg but NOT in baseURLs must simply be absent
// from the returned maps — it is not an error (the caller decided not to
// wire it). Available() returns false for it, which is the correct
// probe_unsupported refusal path.
func TestBuildDispatcher_AbsentProviderIsNotWired(t *testing.T) {
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

	transports, _, err := BuildDispatcher(reg, impls, baseURLs)
	if err != nil {
		t.Fatalf("BuildDispatcher() error = %v, want nil", err)
	}
	if _, ok := transports["p-unwired"]; ok {
		t.Fatal("transports[p-unwired] is present — a provider absent from baseURLs must not appear in the result")
	}
	if transports["p-wired"] != impl {
		t.Fatal("transports[p-wired] is missing or wrong — wired provider must appear in the result")
	}
}
