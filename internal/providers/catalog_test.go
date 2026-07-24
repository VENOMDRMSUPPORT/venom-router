package providers

import (
	"context"
	"testing"
)

func TestBuiltinCatalog_HasExactlyElevenEntries(t *testing.T) {
	entries := BuiltinCatalog()
	if len(entries) != 11 {
		t.Fatalf("BuiltinCatalog() len = %d, want 11", len(entries))
	}
	for _, e := range entries {
		if e.ID == "" {
			t.Fatalf("entry has empty ID: %+v", e)
		}
		switch e.Funding.Mode {
		case FundingModeFixed, FundingModeOwnerPolicy, FundingModeProviderEvidence, FundingModeEvidenceRequired:
		default:
			t.Fatalf("entry %q has invalid funding mode %q", e.ID, e.Funding.Mode)
		}
		if e.Funding.Mode == FundingModeFixed && e.Funding.Fixed == "" {
			t.Fatalf("entry %q has FundingModeFixed but empty Fixed value", e.ID)
		}
		if e.Funding.Mode != FundingModeFixed && e.Funding.Fixed != "" {
			t.Fatalf("entry %q has Fixed=%q set despite Mode != fixed", e.ID, e.Funding.Fixed)
		}
	}
}

func findEntry(t *testing.T, id ProviderID) CatalogEntry {
	t.Helper()
	for _, e := range BuiltinCatalog() {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("no builtin catalog entry for %q", id)
	return CatalogEntry{}
}

func TestBuiltinCatalog_ClinePassIsFixedPaidLocked(t *testing.T) {
	e := findEntry(t, "clinepass")
	if e.Funding.Mode != FundingModeFixed || e.Funding.Fixed != FundingPaid || !e.Funding.Locked {
		t.Fatalf("clinepass funding = %+v, want {fixed paid locked=true}", e.Funding)
	}
	if e.Funding.NonExpiring {
		// Not asserted true or false by the card; just documenting this is
		// a deliberate non-assertion, not an oversight.
		t.Logf("clinepass NonExpiring = true (not required by the card, but not forbidden either)")
	}
}

func TestBuiltinCatalog_EvidenceRequiredProviders(t *testing.T) {
	for _, id := range []ProviderID{"agnes-ai", "gemini-cli", "nvidia-nim"} {
		e := findEntry(t, id)
		if e.Funding.Mode != FundingModeEvidenceRequired {
			t.Fatalf("%q funding mode = %q, want evidence_required", id, e.Funding.Mode)
		}
	}
}

func TestBuiltinCatalog_AntigravityRequiresClientEnv(t *testing.T) {
	e := findEntry(t, "antigravity")
	want := []string{"VENOM_ANTIGRAVITY_CLIENT_SECRET", "VENOM_ANTIGRAVITY_CLIENT_ID"}
	if len(e.RequiredEnv) != len(want) {
		t.Fatalf("antigravity RequiredEnv = %v, want %v", e.RequiredEnv, want)
	}
	for i, v := range want {
		if e.RequiredEnv[i] != v {
			t.Fatalf("antigravity RequiredEnv[%d] = %q, want %q", i, e.RequiredEnv[i], v)
		}
	}
}

func TestCustomPathDescriptor_IsEvidenceRequiredCustomOpenAI(t *testing.T) {
	d := CustomPathDescriptor()
	if d.AuthMode != CatalogAuthCustomOAI {
		t.Fatalf("custom descriptor AuthMode = %q, want %q", d.AuthMode, CatalogAuthCustomOAI)
	}
	if d.Funding.Mode != FundingModeEvidenceRequired {
		t.Fatalf("custom descriptor funding mode = %q, want evidence_required", d.Funding.Mode)
	}
	// The custom path is never seeded as a built-in row: confirm it is
	// excluded from BuiltinCatalog.
	for _, e := range BuiltinCatalog() {
		if e.ID == d.ID {
			t.Fatalf("custom descriptor ID %q unexpectedly present in BuiltinCatalog", d.ID)
		}
	}
}

// catalogFakeAPIKeyAdapter is a minimal test-local APIKeyAdapter so
// DerivedCapabilities can be proven against a real registered adapter
// without needing any real provider implementation.
type catalogFakeAPIKeyAdapter struct{}

func (catalogFakeAPIKeyAdapter) ConnectAPIKey(_ context.Context, _ string) (IdentityResult, StoredCredentials, error) {
	return IdentityResult{}, StoredCredentials{}, nil
}

// TestDerivedCapabilities_EmptyRegistryYieldsNoCapabilities proves
// capabilities are derived from the registry, never fabricated: with
// nothing registered, every builtin ID reports zero capabilities.
func TestDerivedCapabilities_EmptyRegistryYieldsNoCapabilities(t *testing.T) {
	reg := NewRegistry()
	for _, e := range BuiltinCatalog() {
		if caps := DerivedCapabilities(reg, e.ID); len(caps) != 0 {
			t.Fatalf("DerivedCapabilities(empty registry, %q) = %v, want empty", e.ID, caps)
		}
	}
}

// TestDerivedCapabilities_ReflectsRegisteredAdapter proves the other
// half: registering a real (fake) adapter makes the corresponding
// capability appear, mechanically, without any per-slug special case.
func TestDerivedCapabilities_ReflectsRegisteredAdapter(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(Definition{
		ID:       "opencode-zen",
		AuthMode: AuthModeAPIKey,
		APIKey:   catalogFakeAPIKeyAdapter{},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	caps := DerivedCapabilities(reg, "opencode-zen")
	if len(caps) != 1 || caps[0] != "api_key" {
		t.Fatalf("DerivedCapabilities after registering an APIKeyAdapter = %v, want [api_key]", caps)
	}

	// A different, unregistered ID is unaffected.
	if caps := DerivedCapabilities(reg, "agnes-ai"); len(caps) != 0 {
		t.Fatalf("DerivedCapabilities(unregistered id) = %v, want empty", caps)
	}
}

// TestDerivedCapabilities_MutationProof_HardCodedListWouldBeCaught
// documents the mutation this suite is designed to catch: if
// DerivedCapabilities were changed to return a hard-coded capability
// list instead of deriving from the registry, this test (which asserts
// the EMPTY-registry case yields nothing) would go red. See the unit
// report for the actual mutation performed.
func TestDerivedCapabilities_MutationProof_HardCodedListWouldBeCaught(t *testing.T) {
	reg := NewRegistry()
	if caps := DerivedCapabilities(reg, "clinepass"); caps != nil {
		t.Fatalf("DerivedCapabilities(empty registry, clinepass) = %v, want nil/empty — a hard-coded list would fail this", caps)
	}
}
