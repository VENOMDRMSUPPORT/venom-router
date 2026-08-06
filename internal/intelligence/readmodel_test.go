package intelligence

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

func trueFact() ResolvedCostFact {
	v := true
	return ResolvedCostFact{IsFree: &v, Source: CostSourceProviderPrice, ExactIdentityMatch: true, Confidence: 1}
}

func falseFact() ResolvedCostFact {
	v := false
	return ResolvedCostFact{IsFree: &v, Source: CostSourceProviderPrice, ExactIdentityMatch: true, Confidence: 1}
}

func unknownFact() ResolvedCostFact {
	return ResolvedCostFact{}
}

func staleFreeFact() ResolvedCostFact {
	v := true
	return ResolvedCostFact{IsFree: &v, Source: CostSourceModelsDev, ExactIdentityMatch: true, Confidence: 1, Stale: true}
}

func conflictFact() ResolvedCostFact {
	v := true
	return ResolvedCostFact{IsFree: &v, Source: CostSourceProviderPrice, ExactIdentityMatch: true, Confidence: 1, Conflict: true}
}

func baseOffering(id string, availability models.Availability) models.Offering {
	return models.Offering{
		Identity:     models.OfferingIdentity{AccountID: "acct1", ProviderModelID: id},
		Availability: availability,
	}
}

func baseInput() ProjectionInput {
	return ProjectionInput{
		ProviderID: "prov1",
		Canonical: models.CanonicalModel{
			ID: "model-1", CanonicalKey: "key-1", DisplayName: "Model One",
			NativeContextTokens: effIntPtr2(128000),
		},
		NativeCapabilities:  []models.Operation{models.OperationChat},
		Offering:            baseOfferingWithContext("model-a", models.AvailabilityAvailable, 100000),
		TransportOperations: []models.Operation{models.OperationChat},
		Certifications:      map[models.Operation]models.Certification{},
		Cost:                trueFact(),
		Classification:      ClassificationRoutableCandidate,
	}
}

func baseOfferingWithContext(id string, availability models.Availability, contextLength int) models.Offering {
	o := baseOffering(id, availability)
	o.ContextLength = effIntPtr2(contextLength)
	o.Capabilities = []models.Operation{models.OperationChat}
	return o
}

func effIntPtr2(v int) *int { return &v }

// TestProject_UnknownContextIneligibleForEveryTier proves 04 §3's rule
// applies at the projection layer regardless of an otherwise-eligible cost
// fact. MUTATION: dropping the context_unknown gate turns this RED.
func TestProject_UnknownContextIneligibleForEveryTier(t *testing.T) {
	in := baseInput()
	in.Canonical.NativeContextTokens = nil
	in.Offering.ContextLength = nil

	out := Project(in)

	for _, tier := range []Tier{TierLite, TierPro, TierMax} {
		te := out.Tiers[tier]
		if te.Eligible {
			t.Fatalf("tier %q eligible = true with unknown context, want false", tier)
		}
		if len(te.Reasons) != 1 || te.Reasons[0] != ReasonContextUnknown {
			t.Fatalf("tier %q reasons = %v, want [%s]", tier, te.Reasons, ReasonContextUnknown)
		}
	}
}

// TestProject_TierEligibilityMatchesEligible proves Project never
// re-derives 04 §2b's table — it must equal calling Eligible directly.
// MUTATION: replacing the Eligible(...) call with a hand-rolled rule turns
// this RED.
func TestProject_TierEligibilityMatchesEligible(t *testing.T) {
	facts := map[string]ResolvedCostFact{
		"free":      trueFact(),
		"paid":      falseFact(),
		"unknown":   unknownFact(),
		"conflict":  conflictFact(),
		"staleFree": staleFreeFact(),
	}
	for name, fact := range facts {
		for _, tier := range []Tier{TierLite, TierPro, TierMax} {
			t.Run(name+"/"+string(tier), func(t *testing.T) {
				in := baseInput()
				in.Cost = fact

				out := Project(in)
				want := Eligible(fact, tier)
				got := out.Tiers[tier]

				if got.Eligible != want.Eligible {
					t.Fatalf("Tiers[%s].Eligible = %v, want %v (Eligible(fact,tier))", tier, got.Eligible, want.Eligible)
				}
				if got.Stale != want.Stale {
					t.Fatalf("Tiers[%s].Stale = %v, want %v", tier, got.Stale, want.Stale)
				}
				if got.Penalty != want.Penalty {
					t.Fatalf("Tiers[%s].Penalty = %v, want %v", tier, got.Penalty, want.Penalty)
				}
			})
		}
	}
}

// TestProject_CatalogOnlyNeverEligible and TestProject_UnclassifiedNeverEligible
// prove both non-routable classifications produce the catalog_only reason
// on every tier, even with a verified-free fact. MUTATION: dropping the
// classification gate turns both RED.
func TestProject_CatalogOnlyNeverEligible(t *testing.T) {
	in := baseInput()
	in.Classification = ClassificationCatalogOnly

	out := Project(in)
	for _, tier := range []Tier{TierLite, TierPro, TierMax} {
		te := out.Tiers[tier]
		if te.Eligible {
			t.Fatalf("tier %q eligible = true for catalog_only, want false", tier)
		}
		if !containsReason(te.Reasons, ReasonCatalogOnly) {
			t.Fatalf("tier %q reasons = %v, want to include %s", tier, te.Reasons, ReasonCatalogOnly)
		}
	}
}

func TestProject_UnclassifiedNeverEligible(t *testing.T) {
	in := baseInput()
	in.Classification = ClassificationUnclassified

	out := Project(in)
	for _, tier := range []Tier{TierLite, TierPro, TierMax} {
		te := out.Tiers[tier]
		if te.Eligible {
			t.Fatalf("tier %q eligible = true for unclassified, want false", tier)
		}
		if !containsReason(te.Reasons, ReasonCatalogOnly) {
			t.Fatalf("tier %q reasons = %v, want to include %s", tier, te.Reasons, ReasonCatalogOnly)
		}
	}
}

// TestProject_WithdrawnNeverEligible proves both withdrawn and unknown
// availability exclude every tier, while available does not. MUTATION:
// dropping the availability gate turns this RED.
func TestProject_WithdrawnNeverEligible(t *testing.T) {
	for _, availability := range []models.Availability{models.AvailabilityWithdrawn, models.AvailabilityUnknown} {
		t.Run(string(availability), func(t *testing.T) {
			in := baseInput()
			in.Offering.Availability = availability

			out := Project(in)
			for _, tier := range []Tier{TierLite, TierPro, TierMax} {
				te := out.Tiers[tier]
				if te.Eligible {
					t.Fatalf("tier %q eligible = true with availability=%s, want false", tier, availability)
				}
				if !containsReason(te.Reasons, ReasonNotAvailable) {
					t.Fatalf("tier %q reasons = %v, want to include %s", tier, te.Reasons, ReasonNotAvailable)
				}
			}
		})
	}

	in := baseInput()
	in.Offering.Availability = models.AvailabilityAvailable
	out := Project(in)
	if !out.Tiers[TierLite].Eligible {
		t.Fatalf("lite eligible = false with AvailabilityAvailable on an otherwise-eligible input, want true")
	}
}

// TestProject_CapabilityIntersection proves effective capability is the
// three-way intersection, fail closed on a nil native/transport set.
// MUTATION: treating a nil set as "everything supported" turns this RED.
func TestProject_CapabilityIntersection(t *testing.T) {
	cases := []struct {
		name                        string
		native, provider, transport []models.Operation
		wantEffective               map[models.Operation]bool
	}{
		{
			"present in all three",
			[]models.Operation{models.OperationChat}, []models.Operation{models.OperationChat}, []models.Operation{models.OperationChat},
			map[models.Operation]bool{models.OperationChat: true},
		},
		{
			"missing from provider exposure",
			[]models.Operation{models.OperationChat}, []models.Operation{}, []models.Operation{models.OperationChat},
			map[models.Operation]bool{models.OperationChat: false},
		},
		{
			"missing from transport",
			[]models.Operation{models.OperationChat}, []models.Operation{models.OperationChat}, []models.Operation{},
			map[models.Operation]bool{models.OperationChat: false},
		},
		{
			"nil native set",
			nil, []models.Operation{models.OperationChat}, []models.Operation{models.OperationChat},
			map[models.Operation]bool{models.OperationChat: false},
		},
		{
			"nil transport set",
			[]models.Operation{models.OperationChat}, []models.Operation{models.OperationChat}, nil,
			map[models.Operation]bool{models.OperationChat: false},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := baseInput()
			in.NativeCapabilities = tc.native
			in.Offering.Capabilities = tc.provider
			in.TransportOperations = tc.transport

			out := Project(in)
			for op, want := range tc.wantEffective {
				var found *EffectiveCapability
				for i := range out.Capabilities {
					if out.Capabilities[i].Operation == op {
						found = &out.Capabilities[i]
						break
					}
				}
				if found == nil {
					if want {
						t.Fatalf("operation %q missing from Capabilities, want present and effective", op)
					}
					continue
				}
				if found.Effective != want {
					t.Fatalf("operation %q Effective = %v, want %v", op, found.Effective, want)
				}
			}
		})
	}

	// Order must equal models.Operations() order.
	in := baseInput()
	in.NativeCapabilities = []models.Operation{models.OperationVision, models.OperationChat}
	in.Offering.Capabilities = []models.Operation{models.OperationVision, models.OperationChat}
	in.TransportOperations = []models.Operation{models.OperationVision, models.OperationChat}
	out := Project(in)
	if len(out.Capabilities) != 2 || out.Capabilities[0].Operation != models.OperationChat || out.Capabilities[1].Operation != models.OperationVision {
		t.Fatalf("Capabilities order = %+v, want [chat, vision] (models.Operations() order)", out.Capabilities)
	}
}

// TestProject_RoutableRequiresCertifiedSupportedAndEffective proves
// Routable requires BOTH the certified+supported combination AND
// Effective. MUTATION: dropping the && Effective conjunct turns this RED.
func TestProject_RoutableRequiresCertifiedSupportedAndEffective(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name         string
		cert         models.Certification
		hasCert      bool
		transport    []models.Operation
		wantRoutable bool
	}{
		{"certified+supported but not effective (no transport)", models.Certification{State: models.CertCertified, Truth: models.TruthSupported}, true, nil, false},
		{"effective but probing", models.Certification{State: models.CertProbing, Truth: models.TruthUnknown}, true, []models.Operation{models.OperationChat}, false},
		{"effective but unknown truth", models.Certification{State: models.CertDiscovered, Truth: models.TruthUnknown}, true, []models.Operation{models.OperationChat}, false},
		{"certified+supported+effective", models.Certification{State: models.CertCertified, Truth: models.TruthSupported}, true, []models.Operation{models.OperationChat}, true},
		{"missing certification entry", models.Certification{}, false, []models.Operation{models.OperationChat}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := baseInput()
			in.TransportOperations = tc.transport
			in.Certifications = map[models.Operation]models.Certification{}
			if tc.hasCert {
				cert := tc.cert
				cert.CreatedAt = now
				cert.UpdatedAt = now
				in.Certifications[models.OperationChat] = cert
			}

			out := Project(in)
			var found *EffectiveCapability
			for i := range out.Capabilities {
				if out.Capabilities[i].Operation == models.OperationChat {
					found = &out.Capabilities[i]
				}
			}
			if found == nil {
				t.Fatalf("chat capability missing from Capabilities")
			}
			if found.Routable != tc.wantRoutable {
				t.Fatalf("Routable = %v, want %v (state=%s truth=%s effective=%v)", found.Routable, tc.wantRoutable, found.State, found.Truth, found.Effective)
			}
			if !tc.hasCert {
				if found.State != models.CertDiscovered || found.Truth != models.TruthUnknown {
					t.Fatalf("missing-certification defaults = (%s,%s), want (discovered,unknown)", found.State, found.Truth)
				}
			}
		})
	}
}

// TestProject_Deterministic proves identical input yields identical
// output across many calls, and Reasons is always sorted. MUTATION:
// building Capabilities from a map range (order leaks) or dropping the
// sort turns this RED.
// TestProject_CapabilityCarriesItsOfferingOperationID proves each capability
// carries the offering-operation id of ITS OWN certification row, taken from the
// Certifications map rather than synthesized.
//
// This is the identifier POST /offerings/{id}/probe is keyed by, and it is the
// one fact the projection previously dropped: the Models surface could name an
// operation but not identify the row, so its probe control had to ship disabled.
// Two operations are seeded with DIFFERENT ids precisely so returning "the first
// id" for every capability — the easy wrong implementation — is caught.
func TestProject_CapabilityCarriesItsOfferingOperationID(t *testing.T) {
	in := baseInput()
	in.NativeCapabilities = []models.Operation{models.OperationChat, models.OperationTools}
	in.TransportOperations = []models.Operation{models.OperationChat, models.OperationTools}
	in.Offering.Capabilities = []models.Operation{models.OperationChat, models.OperationTools}
	in.Certifications = map[models.Operation]models.Certification{
		models.OperationChat: {
			OfferingOperationID: "op-chat-7",
			State:               models.CertCertified,
			Truth:               models.TruthSupported,
		},
		models.OperationTools: {
			OfferingOperationID: "op-tools-42",
			State:               models.CertObserved,
			Truth:               models.TruthUnknown,
		},
	}

	got := Project(in)

	byOp := map[models.Operation]EffectiveCapability{}
	for _, c := range got.Capabilities {
		byOp[c.Operation] = c
	}

	want := map[models.Operation]string{
		models.OperationChat:  "op-chat-7",
		models.OperationTools: "op-tools-42",
	}
	for op, wantID := range want {
		c, ok := byOp[op]
		if !ok {
			t.Fatalf("capability for %q missing from the projection", op)
		}
		if c.OfferingOperationID != wantID {
			t.Errorf("%q OfferingOperationID = %q, want %q (each capability must carry ITS OWN row's id)", op, c.OfferingOperationID, wantID)
		}
	}
}

// TestProject_CapabilityWithoutCertificationHasNoOfferingOperationID proves the
// id stays EMPTY when there is no certification row for that operation.
//
// An operation reachable only through native/transport support has no
// offering_operations row, so there is no id to report and nothing is probeable.
// The empty string is the projection's "absent" and the HTTP layer omits the
// field entirely — a synthesized or borrowed id would point a probe at the wrong
// row.
func TestProject_CapabilityWithoutCertificationHasNoOfferingOperationID(t *testing.T) {
	in := baseInput()
	// Chat is natively/transport supported but carries NO certification row.
	in.Certifications = map[models.Operation]models.Certification{}

	got := Project(in)

	if len(got.Capabilities) == 0 {
		t.Fatalf("no capabilities projected — this test would be vacuous")
	}
	for _, c := range got.Capabilities {
		if c.OfferingOperationID != "" {
			t.Errorf("%q OfferingOperationID = %q, want \"\" (no certification row means no probeable id)", c.Operation, c.OfferingOperationID)
		}
		// Sanity: the rest of the capability still projects normally.
		if c.State != models.CertDiscovered || c.Truth != models.TruthUnknown {
			t.Errorf("%q = (%q,%q), want the discovered/unknown fail-closed baseline", c.Operation, c.State, c.Truth)
		}
	}
}

// TestProject_CandidateOperationSurfacesUnprobedButUndeclared proves a
// CANDIDATE operation (discovery.go's DiscoveredModel.CandidateOperations:
// one the adapter did not declare, e.g. clinepass's wire returns {id, name}
// only, but that still gets an offering_operations row so it has something
// to probe — see discovery_test.go's
// TestDiscoveryRepo_Apply_CandidateOperationsCreateRowsButStayUndeclared)
// reaches the projection's output.
//
// Before this test, `union` (the set of operations Project emits) was built
// only from native ∪ providerExposed ∪ transport — never from the
// Certifications map's own keys. A candidate's certification row (real
// OfferingOperationID, state=discovered, truth=unknown) exists in
// Certifications but its operation is absent from all three of those sets,
// so it was silently dropped: the id every client needs for
// `POST /offerings/{id}/probe` never reached the API response at all,
// making every candidate operation permanently unprobeable regardless of
// what the storage layer had already made possible.
//
// The operation must surface WITHOUT being fabricated as provider-exposed:
// Effective stays false (providerExposed excludes it) and Provenance stays
// "" (state=discovered/truth=unknown is never Routable) — only its
// OfferingOperationID becomes reachable. MUTATION: reverting `union` to
// native ∪ providerExposed ∪ transport only turns this RED; unioning in a
// declared-capabilities-shaped set instead of Certifications' keys, or
// marking the candidate Effective/declared, would falsely claim the
// provider exposed it.
func TestProject_CandidateOperationSurfacesUnprobedButUndeclared(t *testing.T) {
	in := baseInput()
	// Tools is a CANDIDATE here: absent from native/providerExposed/transport
	// (baseInput's Offering.Capabilities is chat-only), present only in
	// Certifications — exactly discovery.go's candidate shape.
	in.Certifications = map[models.Operation]models.Certification{
		models.OperationTools: {
			OfferingOperationID: "oo-tools-candidate-1",
			State:               models.CertDiscovered,
			Truth:               models.TruthUnknown,
		},
	}

	out := Project(in)

	var found *EffectiveCapability
	for i := range out.Capabilities {
		if out.Capabilities[i].Operation == models.OperationTools {
			found = &out.Capabilities[i]
		}
	}
	if found == nil {
		t.Fatalf("candidate operation %q missing from Capabilities — its offering_operation_id can never reach a client, so it can never be probed", models.OperationTools)
	}
	if found.OfferingOperationID != "oo-tools-candidate-1" {
		t.Errorf("OfferingOperationID = %q, want %q", found.OfferingOperationID, "oo-tools-candidate-1")
	}
	if found.Effective {
		t.Errorf("Effective = true, want false (the provider never exposed this operation — only a candidate row exists)")
	}
	if found.Provenance != "" {
		t.Errorf("Provenance = %q, want \"\" (discovered/unknown is never Routable, so it earns no provenance)", found.Provenance)
	}
}

// TestProject_CapabilityProvenance proves the derivation rule (task-5): chat
// is ALWAYS "probed" when certified+supported (no declared path exists for
// chat by construction — it is only ever certified by the runtime usability
// sweep/fast-lane); a non-chat operation is "probed" iff ProvedOperations
// says a succeeded probe run exists for it, else "declared" (it was
// certified by certifyDeclaredCapabilities); and any operation that is not
// certified+supported carries no provenance at all — provenance only
// qualifies an earned certification. MUTATION: computing Provenance from
// Effective/Routable instead of state+truth alone, or defaulting an unproved
// non-chat op to "probed", turns this RED.
func TestProject_CapabilityProvenance(t *testing.T) {
	cases := []struct {
		name           string
		operation      models.Operation
		state          models.CertificationState
		truth          models.CapabilityTruth
		provedOps      map[models.Operation]bool
		wantProvenance string
	}{
		{
			name:      "chat certified+supported is always probed, even with no ProvedOperations fact",
			operation: models.OperationChat, state: models.CertCertified, truth: models.TruthSupported,
			provedOps: nil, wantProvenance: ProvenanceProbed,
		},
		{
			name:      "non-chat certified+supported WITH a succeeded probe run is probed",
			operation: models.OperationTools, state: models.CertCertified, truth: models.TruthSupported,
			provedOps: map[models.Operation]bool{models.OperationTools: true}, wantProvenance: ProvenanceProbed,
		},
		{
			name:      "non-chat certified+supported WITHOUT a succeeded probe run is declared",
			operation: models.OperationTools, state: models.CertCertified, truth: models.TruthSupported,
			provedOps: nil, wantProvenance: ProvenanceDeclared,
		},
		{
			name:      "not certified+supported (discovered/unknown) carries no provenance",
			operation: models.OperationVision, state: models.CertDiscovered, truth: models.TruthUnknown,
			provedOps: nil, wantProvenance: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := ProjectionInput{
				ProviderID:         "prov1",
				Canonical:          models.CanonicalModel{DisplayName: "Model"},
				NativeCapabilities: []models.Operation{tc.operation},
				Offering: models.Offering{
					Identity:     models.OfferingIdentity{AccountID: "acct1", ProviderModelID: "model-a"},
					Availability: models.AvailabilityAvailable,
					Capabilities: []models.Operation{tc.operation},
				},
				TransportOperations: []models.Operation{tc.operation},
				Certifications: map[models.Operation]models.Certification{
					tc.operation: {State: tc.state, Truth: tc.truth},
				},
				Cost:             unknownFact(),
				Classification:   ClassificationRoutableCandidate,
				ProvedOperations: tc.provedOps,
			}

			out := Project(in)
			var found *EffectiveCapability
			for i := range out.Capabilities {
				if out.Capabilities[i].Operation == tc.operation {
					found = &out.Capabilities[i]
				}
			}
			if found == nil {
				t.Fatalf("%s capability missing from Capabilities", tc.operation)
			}
			if found.Provenance != tc.wantProvenance {
				t.Fatalf("Provenance = %q, want %q (state=%s truth=%s proved=%v)", found.Provenance, tc.wantProvenance, tc.state, tc.truth, tc.provedOps)
			}
		})
	}
}

func TestProject_Deterministic(t *testing.T) {
	in := baseInput()
	in.NativeCapabilities = []models.Operation{models.OperationVision, models.OperationChat, models.OperationTools}
	in.Offering.Capabilities = []models.Operation{models.OperationChat}
	in.TransportOperations = []models.Operation{models.OperationChat}
	in.Offering.Availability = models.AvailabilityWithdrawn
	in.Classification = ClassificationCatalogOnly
	in.Canonical.NativeContextTokens = nil
	in.Offering.ContextLength = nil

	first := Project(in)
	for i := 0; i < 50; i++ {
		got := Project(in)
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("Project produced a different result on run %d:\n%+v\nvs\n%+v", i, got, first)
		}
	}

	for tier, te := range first.Tiers {
		if !sort.StringsAreSorted(te.Reasons) {
			t.Fatalf("Tiers[%s].Reasons = %v, not sorted ascending", tier, te.Reasons)
		}
		if len(te.Reasons) == 0 {
			t.Fatalf("Tiers[%s].Reasons is empty, want multiple ineligibility reasons for this input", tier)
		}
	}
}

func containsReason(reasons []string, target string) bool {
	for _, r := range reasons {
		if r == target {
			return true
		}
	}
	return false
}

// TestProject_CarriesTheOfferingTokenLimits proves MaxInputTokens and
// MaxOutputTokens reach EffectiveOffering straight from the offering — they
// have been persisted at discovery since the M4 schema and read by nobody
// until now. They are distinct from EffectiveContextTokens (the context
// window): a model can accept a 1M-token context while capping a single
// reply far lower, so neither field may be derived from the other.
// MUTATION: replacing either field's source with nil turns this RED.
func TestProject_CarriesTheOfferingTokenLimits(t *testing.T) {
	in := ProjectionInput{
		ProviderID: "clinepass",
		Offering: models.Offering{
			Identity:        models.OfferingIdentity{AccountID: "a", ProviderModelID: "m"},
			Availability:    models.AvailabilityAvailable,
			ContextLength:   intPtr(1048576),
			MaxInputTokens:  intPtr(900000),
			MaxOutputTokens: intPtr(131072),
		},
	}
	got := Project(in)
	if got.MaxInputTokens == nil || *got.MaxInputTokens != 900000 {
		t.Fatalf("MaxInputTokens = %v, want 900000 — the column has been written since discovery and read by nobody", got.MaxInputTokens)
	}
	if got.MaxOutputTokens == nil || *got.MaxOutputTokens != 131072 {
		t.Fatalf("MaxOutputTokens = %v, want 131072", got.MaxOutputTokens)
	}
	if got.EffectiveContextTokens == nil || *got.EffectiveContextTokens != 1048576 {
		t.Fatalf("EffectiveContextTokens = %v, want 1048576 from the offering's catalog-filled context length", got.EffectiveContextTokens)
	}
	if got.ContextProvenance != models.ContextProviderCap {
		t.Fatalf("ContextProvenance = %q, want provider_cap", got.ContextProvenance)
	}
}
