package httpapi

import (
	"slices"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
)

// registeredDiscoveryProviderIDs derives the totality expectation directly
// from the LIVE composition root (newProviderRegistry) and the catalog it is
// checked against — never a hardcoded provider list, so this test cannot
// silently drift from production the way a copy-pasted slice of provider IDs
// would. A provider appears here iff newProviderRegistry() actually
// registered a ModelDiscoveryAdapter for it: an env-gated provider with no
// discovery adapter yet (antigravity) or one this composition does not
// register at all (codex, github-copilot, xai — P7-EXEC-001 still open) is
// correctly excluded without this test ever naming it.
func registeredDiscoveryProviderIDs() []string {
	reg := newProviderRegistry()
	var ids []string
	for _, entry := range providers.BuiltinCatalog() {
		if _, ok := reg.ModelDiscoveryAdapter(entry.ID); ok {
			ids = append(ids, string(entry.ID))
		}
	}
	return ids
}

// usabilitySelfVersioningProbes names the providers whose usability probe
// appends its OWN full versioned path onto a BARE host, rather than a
// "/chat/completions"-only (or equivalent) suffix onto an
// already-versioned liveProviderBaseURLs() entry:
//   - opencode-zen's probe appends "/v1/chat/completions" (opencode_zen_usability.go)
//   - clinepass's probe travels through its own base convention (clinepass_usability.go)
//
// Their usabilityProviderSpecs().baseURL is therefore INTENTIONALLY the bare
// host, not the liveProviderBaseURLs() entry (which the request-path
// transports need pre-versioned instead, for the openai_compatible /
// native_api / native_oauth conventions the five newer probes follow). Diffing
// their spec.baseURL against liveProviderBaseURLs() would flag this
// intentional, pre-existing divergence as a bug. Rather than weakening the
// baseURL-consistency assertion for every provider, this map documents the
// exception explicitly and by name, so the assertion still runs at full
// strength for every provider it is meaningful for.
var usabilitySelfVersioningProbes = map[string]bool{
	string(providers.OpenCodeZenID): true,
	string(providers.ClinePassID):   true,
}

// TestUsabilitySpecs_TotalOverRegisteredDiscoveryAdapters is the totality
// guard on usabilityProviderSpecs (task-5, spec 2026-08-05): every provider
// newProviderRegistry() registers a discovery adapter for must carry a
// usability probe, or its models silently stay stuck in `probing` forever
// (usability_wiring.go's own doc comment: "A provider absent from this map is
// simply never swept"). The expected set is DERIVED from the registry via
// registeredDiscoveryProviderIDs, never listed here, so adding a discovery
// adapter without wiring its usability probe fails this test — which is the
// point — and the second loop below closes the other direction: a spec entry
// for a provider with no discovery adapter is dead weight nothing will ever
// drive.
func TestUsabilitySpecs_TotalOverRegisteredDiscoveryAdapters(t *testing.T) {
	specs := usabilityProviderSpecs()
	bases := liveProviderBaseURLs()

	discoveryIDs := registeredDiscoveryProviderIDs()
	if len(discoveryIDs) == 0 {
		t.Fatal("registeredDiscoveryProviderIDs() returned nothing — the assertions below would run vacuously")
	}

	for _, providerID := range discoveryIDs {
		spec, ok := specs[providerID]
		if !ok {
			t.Errorf("provider %q has a discovery adapter but NO usability probe — it would ship unswept", providerID)
			continue
		}
		if spec.probe == nil {
			t.Errorf("provider %q: usability spec has a nil probe", providerID)
		}
		if usabilitySelfVersioningProbes[providerID] {
			// Documented exception — see usabilitySelfVersioningProbes.
			continue
		}
		if base, ok := bases[providers.ProviderID(providerID)]; ok && spec.baseURL != base {
			t.Errorf("provider %q: usability baseURL %q != live baseURL %q", providerID, spec.baseURL, base)
		}
	}

	for providerID := range specs {
		if !slices.Contains(discoveryIDs, providerID) {
			t.Errorf("usability spec for %q has no discovery adapter — dead entry", providerID)
		}
	}
}
