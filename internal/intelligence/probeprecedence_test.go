package intelligence

import (
	"reflect"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

func TestResolveFact_ProbePositiveBeatsMetadata(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	field := CapabilityField(models.OperationTools)
	scope := Scope{AccountID: "acct-1", ProviderModelID: "model-1"}

	probe := Evidence{Field: field, Scope: scope, Source: SourceVerifiedProbe, Verification: VerificationVerified, Confidence: 1.0, ObservedAt: now, Value: true}
	metadata := Evidence{Field: field, Scope: scope, Source: SourceProviderMetadata, Verification: VerificationDeclared, Confidence: 0.9, ObservedAt: now, Value: true}
	discovery := Evidence{Field: field, Scope: scope, Source: SourceProviderDiscovery, Verification: VerificationDeclared, Confidence: 0.9, ObservedAt: now, Value: true}
	external := Evidence{Field: field, Scope: scope, Source: SourceExternalRegistry, Verification: VerificationDeclared, Confidence: 0.9, ObservedAt: now, Value: true}

	res := ResolveFact(field, []Evidence{metadata, discovery, external, probe}, now)
	if res.Kind != ResolutionKnown || res.Winner.Source != SourceVerifiedProbe {
		t.Fatalf("res = %+v, want verified_probe winner", res)
	}

	// Positive control: without the probe evidence, the next-highest rung
	// (provider_metadata) wins instead.
	res2 := ResolveFact(field, []Evidence{metadata, discovery, external}, now)
	if res2.Kind != ResolutionKnown || res2.Winner.Source != SourceProviderMetadata {
		t.Fatalf("res2 = %+v, want provider_metadata winner", res2)
	}
}

func TestResolveFact_ProbedCapBeatsBroaderExternalClaim(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	field := FieldNativeContextTokens
	scope := Scope{AccountID: "acct-1", ProviderModelID: "model-1"}

	probed := Evidence{Field: field, Scope: scope, Source: SourceVerifiedProbe, Verification: VerificationVerified, Confidence: 1.0, ObservedAt: now, Value: 128000}
	external := Evidence{Field: field, Scope: scope, Source: SourceExternalRegistry, Verification: VerificationDeclared, Confidence: 0.5, ObservedAt: now, Value: 1000000}

	res := ResolveFact(field, []Evidence{external, probed}, now)
	if res.Kind != ResolutionKnown || res.Value != 128000 {
		t.Fatalf("res = %+v, want 128000 (probed cap beats the broader external claim)", res)
	}
}

// TestResolveFact_NarrowingOnlyAppliesAtWinnersRank proves the narrowing
// rule is scoped to candidates sharing the WINNER's source rank — a
// smaller numeric value from a strictly lower-ranked source must never
// override a higher-ranked winner. TestResolveFact_ProbePositiveBeatsMetadata
// cannot catch a defect here because its evidence values are all bool
// (non-numeric), so the narrowing rule never engages there regardless.
func TestResolveFact_NarrowingOnlyAppliesAtWinnersRank(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	field := FieldNativeContextTokens
	scope := Scope{AccountID: "acct-1", ProviderModelID: "model-1"}

	winner := Evidence{Field: field, Scope: scope, Source: SourceVerifiedProbe, Verification: VerificationVerified, Confidence: 1.0, ObservedAt: now, Value: 200000}
	lowerRankSmaller := Evidence{Field: field, Scope: scope, Source: SourceExternalRegistry, Verification: VerificationDeclared, Confidence: 0.5, ObservedAt: now, Value: 50000}

	res := ResolveFact(field, []Evidence{winner, lowerRankSmaller}, now)
	if res.Kind != ResolutionKnown || res.Value != 200000 {
		t.Fatalf("res = %+v, want 200000 (a lower-ranked source's smaller value must never override the winner)", res)
	}
}

func TestResolveFact_NarrowerRestrictionWinsAtEqualRank(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	field := FieldNativeContextTokens
	scope := Scope{AccountID: "acct-1", ProviderModelID: "model-1"}

	// broader is made more attractive to every one of Resolve's own
	// tie-breaks (higher confidence, fresher) than narrower, so Resolve
	// alone would pick broader as the winner.
	broader := Evidence{
		Field: field, Scope: scope, Source: SourceVerifiedProbe, Verification: VerificationVerified,
		Confidence: 1.0, ObservedAt: now.Add(time.Hour), Value: 200000,
	}
	narrower := Evidence{
		Field: field, Scope: scope, Source: SourceVerifiedProbe, Verification: VerificationVerified,
		Confidence: 0.5, ObservedAt: now, Value: 128000,
	}

	res := ResolveFact(field, []Evidence{broader, narrower}, now.Add(2*time.Hour))
	if res.Kind != ResolutionKnown || res.Value != 128000 {
		t.Fatalf("res = %+v, want 128000 (the narrower claim wins despite broader's stronger tie-break)", res)
	}

	// Positive control: with a single claim present, that claim wins
	// unchanged.
	res2 := ResolveFact(field, []Evidence{narrower}, now.Add(2*time.Hour))
	if res2.Kind != ResolutionKnown || res2.Value != 128000 {
		t.Fatalf("res2 = %+v, want 128000 unchanged", res2)
	}
}

func TestResolveFact_OwnerOverrideStillSupreme(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	field := FieldNativeContextTokens
	scope := Scope{AccountID: "acct-1", ProviderModelID: "model-1"}

	t.Run("owner override beats a narrower probed claim", func(t *testing.T) {
		owner := Evidence{Field: field, Scope: scope, Source: SourceOwnerOverride, Verification: VerificationDeclared, Confidence: 1.0, ObservedAt: now, Value: 1000000}
		probed := Evidence{Field: field, Scope: scope, Source: SourceVerifiedProbe, Verification: VerificationVerified, Confidence: 1.0, ObservedAt: now, Value: 128000}

		res := ResolveFact(field, []Evidence{owner, probed}, now)
		if res.Kind != ResolutionKnown || res.Value != 1000000 || res.Winner.Source != SourceOwnerOverride {
			t.Fatalf("res = %+v, want owner override 1000000", res)
		}
	})

	t.Run("the narrowing rule never applies among owner overrides themselves", func(t *testing.T) {
		// owner1 is Resolve's own natural winner among the two owner
		// overrides (fresher, same confidence) despite carrying the
		// LARGER value. If the narrowing rule were mistakenly applied to
		// owner-override winners, it would flip this to owner2 (smaller).
		owner1 := Evidence{Field: field, Scope: scope, Source: SourceOwnerOverride, Verification: VerificationDeclared, Confidence: 1.0, ObservedAt: now.Add(time.Hour), Value: 1000000}
		owner2 := Evidence{Field: field, Scope: scope, Source: SourceOwnerOverride, Verification: VerificationDeclared, Confidence: 1.0, ObservedAt: now, Value: 128000}

		res := ResolveFact(field, []Evidence{owner1, owner2}, now.Add(2*time.Hour))
		if res.Kind != ResolutionKnown || res.Value != 1000000 {
			t.Fatalf("res = %+v, want 1000000 (Resolve's own owner-override tie-break, not narrowed)", res)
		}
	})
}

func TestResolveFact_ProvenNegativePersists(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	field := CapabilityField(models.OperationTools)
	scope := Scope{AccountID: "acct-1", ProviderModelID: "model-1"}

	negative := Evidence{
		Field: field, Scope: scope, Source: SourceVerifiedProbe, Verification: VerificationObserved,
		Confidence: 1.0, ObservedAt: now, ProvenNegative: true, Value: false,
	}
	weakerPositive := Evidence{
		Field: field, Scope: scope, Source: SourceExternalRegistry, Verification: VerificationDeclared,
		Confidence: 0.9, ObservedAt: now.Add(time.Hour), Value: true,
	}
	res := ResolveFact(field, []Evidence{negative, weakerPositive}, now.Add(2*time.Hour))
	if res.Kind != ResolutionKnown || res.Value != false {
		t.Fatalf("res = %+v, want the proven negative (false) to persist against a weaker positive", res)
	}

	strongerPositive := Evidence{
		Field: field, Scope: scope, Source: SourceVerifiedProbe, Verification: VerificationVerified,
		Confidence: 1.0, ObservedAt: now.Add(time.Hour), Value: true,
	}
	res2 := ResolveFact(field, []Evidence{negative, strongerPositive}, now.Add(2*time.Hour))
	if res2.Kind != ResolutionKnown || res2.Value != true {
		t.Fatalf("res2 = %+v, want the strictly-higher-verification positive to revalidate", res2)
	}
}

// TestResolveFact_NarrowingNeverConsidersProvenNegativeCandidates uses a
// NUMERIC field, unlike TestResolveFact_ProvenNegativePersists (bool
// values) — a proven-negative candidate whose value merely happens to be
// numeric and smaller must never be treated as a "narrower restriction"
// by ResolveFact's own loop. The winner here is a genuine (non-negative)
// positive that already revalidated past the proven negative inside
// Resolve itself (strictly higher verification), so this exercises
// ResolveFact's separate, additional filter over the full evidence list.
func TestResolveFact_NarrowingNeverConsidersProvenNegativeCandidates(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	field := FieldNativeContextTokens
	scope := Scope{AccountID: "acct-1", ProviderModelID: "model-1"}

	negative := Evidence{
		Field: field, Scope: scope, Source: SourceVerifiedProbe, Verification: VerificationObserved,
		Confidence: 1.0, ObservedAt: now, ProvenNegative: true, Value: 50000,
	}
	positive := Evidence{
		Field: field, Scope: scope, Source: SourceVerifiedProbe, Verification: VerificationVerified,
		Confidence: 1.0, ObservedAt: now, Value: 200000,
	}

	res := ResolveFact(field, []Evidence{negative, positive}, now)
	if res.Kind != ResolutionKnown || res.Value != 200000 {
		t.Fatalf("res = %+v, want 200000 (a proven-negative candidate must never narrow the winner)", res)
	}
}

func TestResolveFact_MatchesResolveForNonNumericFields(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	scope := Scope{AccountID: "acct-1"}

	tests := []struct {
		name     string
		field    string
		evidence []Evidence
	}{
		{
			name:  "string family field",
			field: FieldFamily,
			evidence: []Evidence{
				{Field: FieldFamily, Scope: scope, Source: SourceProviderDiscovery, Verification: VerificationDeclared, Confidence: 0.8, ObservedAt: now, Value: "gpt"},
				{Field: FieldFamily, Scope: scope, Source: SourceExternalRegistry, Verification: VerificationDeclared, Confidence: 0.5, ObservedAt: now, Value: "gpt-family"},
			},
		},
		{
			// Confidence and source rank deliberately point different
			// ways: the lower-ranked candidate has the HIGHER confidence.
			// Resolve picks by rank first (provider_discovery wins); a
			// ResolveFact that re-derived its own winner by "highest
			// confidence" instead of delegating to Resolve would pick the
			// external_registry candidate here, diverging from Resolve.
			name:  "confidence and rank diverge",
			field: FieldFamily,
			evidence: []Evidence{
				{Field: FieldFamily, Scope: scope, Source: SourceProviderDiscovery, Verification: VerificationDeclared, Confidence: 0.3, ObservedAt: now, Value: "gpt"},
				{Field: FieldFamily, Scope: scope, Source: SourceExternalRegistry, Verification: VerificationDeclared, Confidence: 0.9, ObservedAt: now, Value: "gpt-family"},
			},
		},
		{
			name:  "bool capability field",
			field: CapabilityField(models.OperationVision),
			evidence: []Evidence{
				{Field: CapabilityField(models.OperationVision), Scope: scope, Source: SourceProviderMetadata, Verification: VerificationDeclared, Confidence: 0.9, ObservedAt: now, Value: true},
			},
		},
		{
			name:     "no evidence at all",
			field:    FieldFamily,
			evidence: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := Resolve(tt.field, tt.evidence, now)
			got := ResolveFact(tt.field, tt.evidence, now)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("ResolveFact = %+v, want identical to Resolve %+v", got, want)
			}
		})
	}
}

func TestMergeProbeEvidence_OrderAndPurity(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	e1 := Evidence{Field: "f1", Scope: Scope{AccountID: "a"}, Source: SourceProviderDiscovery, Verification: VerificationDeclared, Value: "a", ObservedAt: now}
	e2 := Evidence{Field: "f2", Scope: Scope{AccountID: "a"}, Source: SourceVerifiedProbe, Verification: VerificationVerified, Value: "b", ObservedAt: now}
	dup := e2

	// existing is built with spare capacity: an implementation that
	// appends into it in place (rather than building a fresh slice) would
	// not need to reallocate here, so a plain length/content comparison
	// after the call could miss the mutation entirely.
	existing := make([]Evidence, 1, 4)
	existing[0] = e1
	probe := []Evidence{e2, dup}

	existingCopy := append([]Evidence(nil), existing...)
	probeCopy := append([]Evidence(nil), probe...)

	merged := MergeProbeEvidence(existing, probe)

	if !reflect.DeepEqual(existing, existingCopy) {
		t.Fatalf("existing input was mutated: %+v", existing)
	}
	if !reflect.DeepEqual(probe, probeCopy) {
		t.Fatalf("probe input was mutated: %+v", probe)
	}
	// Re-slice to existing's full capacity and confirm the spare capacity
	// was never written into.
	extended := existing[:cap(existing)]
	var zero Evidence
	for i := len(existingCopy); i < len(extended); i++ {
		if extended[i] != zero {
			t.Fatalf("existing's spare capacity was written into at index %d (%+v) — MergeProbeEvidence must never append in place", i, extended[i])
		}
	}
	want := []Evidence{e1, e2}
	if !reflect.DeepEqual(merged, want) {
		t.Fatalf("merged = %+v, want %+v (existing first, then probe, exact duplicates collapsed)", merged, want)
	}
}
