package httpapi

import (
	"net/http"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/execution"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
)

// liveAPIKeyRegistry builds the registry exactly as ControlMux/publicMux do for
// the API-key providers they register UNCONDITIONALLY. Kept next to the wiring
// assertions below so the two can never drift apart.
func liveAPIKeyRegistry(t *testing.T) *providers.Registry {
	t.Helper()
	reg := providers.NewRegistry()
	for name, register := range map[string]func(*providers.Registry) error{
		"opencode-zen": registerOpenCodeZen,
		"ollama-cloud": registerOllamaCloud,
		"agnes-ai":     registerAgnesAI,
		"nvidia-nim":   registerNvidiaNIM,
		"gemini-cli":   registerGeminiCLI,
	} {
		if err := register(reg); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}
	return reg
}

// liveRegistry builds the registry the composition roots build UNCONDITIONALLY:
// the API-key providers plus the public-client OAuth adapters (claude-code, and
// clinepass once it ships). antigravity is env-gated and NOT included.
func liveRegistry(t *testing.T) *providers.Registry {
	t.Helper()
	reg := liveAPIKeyRegistry(t)
	if err := registerClaudeCode(reg); err != nil {
		t.Fatalf("register claude-code: %v", err)
	}
	return reg
}

// TestLiveProviderWiring_EveryRegisteredOAuthEntryIsWired extends the totality
// guard to oauth2 catalog entries (P7-EXEC-001 part 2 / P7-PROV-001): any oauth2
// provider the live composition REGISTERS must have a base URL, an implemented
// transport, and — when native_oauth — a valid declared WireSchema, else its
// route cannot be dispatched. Env-gated antigravity is skipped until configured.
func TestLiveProviderWiring_EveryRegisteredOAuthEntryIsWired(t *testing.T) {
	reg := liveRegistry(t)
	impls := liveTransportImpls(&http.Client{}, reg)
	bases := liveProviderBaseURLs()

	oauthChecked := 0
	for _, entry := range providers.BuiltinCatalog() {
		if entry.AuthMode != providers.CatalogAuthOAuth {
			continue
		}
		def, registered := reg.Definition(entry.ID)
		if !registered {
			continue
		}
		oauthChecked++
		if bases[entry.ID] == "" {
			t.Errorf("oauth2 provider %q is registered but has no base URL in liveProviderBaseURLs", entry.ID)
		}
		if _, wired := impls[execution.TransportType(def.Transport)]; !wired {
			t.Errorf("oauth2 provider %q declares transport %q but liveTransportImpls has no implementation for it", entry.ID, def.Transport)
		}
		if def.Transport == providers.TransportKindNativeOAuth {
			if _, err := providers.ParseWireSchema(string(def.WireSchema)); err != nil {
				t.Errorf("native_oauth provider %q has no valid declared WireSchema: %v", entry.ID, err)
			}
		}
	}
	if oauthChecked == 0 {
		t.Fatal("no registered oauth2 entries checked — the OAuth wiring assertions ran against nothing")
	}
}

// TestLiveProviderWiring_EveryAPIKeyCatalogEntryIsWired is the TOTALITY guard on
// the composition roots, and it exists because its absence was a real hole: the
// per-provider suites each build their OWN impls/baseURL maps, so every one of
// them stayed green with the native_api entry deleted from the PRODUCTION table
// — gemini-cli would have shipped unable to certify (409 probe_unsupported)
// with a fully green gate.
//
// The expected set is DERIVED, never listed here: every BuiltinCatalog entry
// whose AuthMode is api_key must (a) be registered by this composition, (b)
// have a fully-resolved base URL in liveProviderBaseURLs, and (c) declare a
// transport kind that liveTransportImpls actually implements. Add an API-key
// provider to the catalog without wiring it and this test fails — which is the
// point.
func TestLiveProviderWiring_EveryAPIKeyCatalogEntryIsWired(t *testing.T) {
	reg := liveAPIKeyRegistry(t)
	impls := liveTransportImpls(&http.Client{}, reg)
	bases := liveProviderBaseURLs()

	apiKeyEntries := 0
	for _, entry := range providers.BuiltinCatalog() {
		if entry.AuthMode != providers.CatalogAuthAPIKey {
			continue
		}
		apiKeyEntries++

		def, registered := reg.Definition(entry.ID)
		if !registered {
			t.Errorf("catalog api_key provider %q has no adapter registered in the live composition", entry.ID)
			continue
		}
		if bases[entry.ID] == "" {
			t.Errorf("provider %q is registered but has no base URL in liveProviderBaseURLs — its offerings can never be probed or served", entry.ID)
		}
		if _, wired := impls[execution.TransportType(def.Transport)]; !wired {
			t.Errorf("provider %q declares transport %q but liveTransportImpls has no implementation for it", entry.ID, def.Transport)
		}
	}

	// A count guard: if the catalog's api_key set ever became empty (or the
	// AuthMode value drifted), the loop above would pass vacuously.
	if apiKeyEntries == 0 {
		t.Fatal("no api_key entries found in BuiltinCatalog — the assertions above ran against nothing")
	}
}

// TestLiveProviderWiring_ProbeMapsResolveEveryLiveProvider proves the SAME two
// tables the composition roots use resolve every live provider through
// BuildProbeTransportMaps, whose contract is fail-closed: it errors when a
// provider is unregistered or when its declared transport kind has no
// implementation. Passing it is the strongest available statement that the
// certification probe path is wired for all of them.
func TestLiveProviderWiring_ProbeMapsResolveEveryLiveProvider(t *testing.T) {
	reg := liveRegistry(t)
	bases := liveProviderBaseURLs()

	probeTransports, probeBaseURLs, err := BuildProbeTransportMaps(reg, liveTransportImpls(&http.Client{}, reg), bases)
	if err != nil {
		t.Fatalf("BuildProbeTransportMaps() error = %v, want every live provider to resolve", err)
	}
	if len(probeTransports) != len(bases) || len(probeBaseURLs) != len(bases) {
		t.Fatalf("resolved %d transports / %d base URLs, want %d of each", len(probeTransports), len(probeBaseURLs), len(bases))
	}
	for id, base := range bases {
		if probeTransports[string(id)] == nil {
			t.Errorf("provider %q resolved to no probe transport", id)
		}
		if probeBaseURLs[string(id)] != base {
			t.Errorf("provider %q probe base URL = %q, want %q", id, probeBaseURLs[string(id)], base)
		}
	}
}

// TestLiveProviderWiring_BaseURLsCarryTheVersionSegmentEachTransportNeeds pins
// the one thing a base-URL table gets wrong silently: the version segment. The
// openai_compatible transport appends "/chat/completions" and native_api
// appends "/models/{id}:generateContent", so each base must already end at the
// version boundary. A wrong base produces a 404 at RUNTIME only.
func TestLiveProviderWiring_BaseURLsCarryTheVersionSegmentEachTransportNeeds(t *testing.T) {
	reg := liveRegistry(t)
	for id, base := range liveProviderBaseURLs() {
		def, ok := reg.Definition(id)
		if !ok {
			t.Fatalf("provider %q not registered", id)
		}
		switch def.Transport {
		case providers.TransportKindOpenAICompatible:
			if !hasVersionSuffix(base, "/v1") {
				t.Errorf("provider %q (openai_compatible) base %q must end at the /v1 segment", id, base)
			}
		case providers.TransportKindNativeAPI:
			if !hasVersionSuffix(base, "/v1beta") {
				t.Errorf("provider %q (native_api) base %q must end at the /v1beta segment", id, base)
			}
		case providers.TransportKindNativeOAuth:
			// The version boundary depends on the wire schema, since the codec
			// owns the endpoint suffix. anthropic_messages appends /v1/messages,
			// so its base must NOT already carry a /v1 (that would double it);
			// openai_chat appends /chat/completions, so its base ends at /v1.
			switch def.WireSchema {
			case providers.WireSchemaAnthropicMessages:
				if hasVersionSuffix(base, "/v1") {
					t.Errorf("provider %q (anthropic_messages) base %q must NOT carry /v1 — the codec appends /v1/messages", id, base)
				}
			case providers.WireSchemaOpenAIChat:
				if !hasVersionSuffix(base, "/v1") {
					t.Errorf("provider %q (openai_chat) base %q must end at the /v1 segment", id, base)
				}
			default:
				t.Errorf("provider %q declares native_oauth schema %q, which this table has no version convention for", id, def.WireSchema)
			}
		default:
			t.Errorf("provider %q declares transport %q, which this table has no version convention for — decide it explicitly", id, def.Transport)
		}
	}
}

func hasVersionSuffix(base, suffix string) bool {
	return len(base) >= len(suffix) && base[len(base)-len(suffix):] == suffix
}
