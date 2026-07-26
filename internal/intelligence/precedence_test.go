package intelligence

import (
	"errors"
	"testing"
	"time"
)

var precFixedNow = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func ev(source EvidenceSource, verification VerificationStatus, confidence float64, observedAt time.Time, value any) Evidence {
	return Evidence{
		Field:        "context_length",
		Source:       source,
		Verification: verification,
		Confidence:   confidence,
		ObservedAt:   observedAt,
		Value:        value,
	}
}

func TestParseEvidenceSource_AcceptsAllSeven(t *testing.T) {
	for _, s := range []string{
		"owner_override", "verified_probe", "provider_metadata",
		"provider_discovery", "external_registry", "heuristic", "unknown",
	} {
		if _, err := ParseEvidenceSource(s); err != nil {
			t.Fatalf("ParseEvidenceSource(%q): unexpected error: %v", s, err)
		}
	}
}

func TestParseEvidenceSource_RejectsUnrecognized(t *testing.T) {
	if _, err := ParseEvidenceSource("provider_default"); !errors.Is(err, ErrUnknownEvidenceSource) {
		t.Fatalf("error = %v, want ErrUnknownEvidenceSource", err)
	}
}

func TestEvidenceSourceRank_LadderOrder(t *testing.T) {
	ladder := []EvidenceSource{
		SourceOwnerOverride, SourceVerifiedProbe, SourceProviderMetadata,
		SourceProviderDiscovery, SourceExternalRegistry, SourceHeuristic, SourceUnknown,
	}
	for i := 0; i < len(ladder)-1; i++ {
		if ladder[i].Rank() <= ladder[i+1].Rank() {
			t.Fatalf("%s.Rank()=%d must be > %s.Rank()=%d", ladder[i], ladder[i].Rank(), ladder[i+1], ladder[i+1].Rank())
		}
	}
}

// TestResolve_PairwiseAcrossWholeLadder proves every adjacent rung of the
// ladder wins head-to-head over the next, holding verification/confidence/
// freshness equal so only source rank can decide.
func TestResolve_PairwiseAcrossWholeLadder(t *testing.T) {
	ladder := []EvidenceSource{
		SourceOwnerOverride, SourceVerifiedProbe, SourceProviderMetadata,
		SourceProviderDiscovery, SourceExternalRegistry, SourceHeuristic,
	}

	for i := 0; i < len(ladder)-1; i++ {
		higher, lower := ladder[i], ladder[i+1]
		t.Run(string(higher)+">"+string(lower), func(t *testing.T) {
			evidence := []Evidence{
				ev(higher, VerificationObserved, 0.5, precFixedNow, "higher-value"),
				ev(lower, VerificationObserved, 0.9, precFixedNow, "lower-value"), // more confident, still loses
			}
			res := Resolve("context_length", evidence, precFixedNow)
			if higher == SourceHeuristic {
				// unreachable in this loop (heuristic is always `lower`), kept for clarity.
				t.Fatal("heuristic should never be `higher` in this ladder slice")
			}
			if res.Kind != ResolutionKnown {
				t.Fatalf("Resolve kind = %s, want known", res.Kind)
			}
			if res.Winner.Source != higher {
				t.Fatalf("winner source = %s, want %s (higher rank must win regardless of confidence)", res.Winner.Source, higher)
			}
		})
	}
}

func TestResolve_OwnerOverrideNeverAutoOverwritten(t *testing.T) {
	older := ev(SourceOwnerOverride, VerificationDeclared, 0.3, precFixedNow.Add(-24*time.Hour), "owner-value")
	fresherVerifiedProbe := ev(SourceVerifiedProbe, VerificationVerified, 0.99, precFixedNow, "probe-value")

	res := Resolve("context_length", []Evidence{older, fresherVerifiedProbe}, precFixedNow)
	if res.Kind != ResolutionKnown || res.Winner.Source != SourceOwnerOverride {
		t.Fatalf("owner override was displaced: kind=%s winner=%+v", res.Kind, res.Winner)
	}

	newerOwnerOverride := ev(SourceOwnerOverride, VerificationDeclared, 0.3, precFixedNow, "owner-value-2")
	res2 := Resolve("context_length", []Evidence{older, newerOwnerOverride, fresherVerifiedProbe}, precFixedNow)
	if res2.Kind != ResolutionKnown || res2.Winner.Value != "owner-value-2" {
		t.Fatalf("a second owner_override must supersede the first: got %+v", res2)
	}

	// An owner override must win outright even against a proven-negative
	// claim whose verification status is strictly higher than the
	// owner's own — the proven-negative revalidation rule (04 §4) applies
	// only among non-owner evidence; owner authority is absolute.
	const field = "supports_tools"
	provenNegative := ev(SourceProviderDiscovery, VerificationVerified, 0.9, precFixedNow.Add(-time.Hour), false)
	provenNegative.Field = field
	provenNegative.ProvenNegative = true
	ownerPositive := ev(SourceOwnerOverride, VerificationDeclared, 0.1, precFixedNow.Add(-48*time.Hour), true)
	ownerPositive.Field = field

	res3 := Resolve(field, []Evidence{provenNegative, ownerPositive}, precFixedNow)
	if res3.Kind != ResolutionKnown || res3.Winner.Source != SourceOwnerOverride || res3.Value != true {
		t.Fatalf("owner override must win outright over a proven negative regardless of verification status: got %+v", res3)
	}
}

func TestResolve_ProvenNegativeWinsUntilRevalidated(t *testing.T) {
	const field = "supports_tools"

	negative := ev(SourceProviderDiscovery, VerificationObserved, 0.5, precFixedNow.Add(-time.Hour), false)
	negative.Field = field
	negative.ProvenNegative = true

	laterLowerStatusPositive := ev(SourceProviderDiscovery, VerificationDeclared, 0.9, precFixedNow, true)
	laterLowerStatusPositive.Field = field
	res := Resolve(field, []Evidence{negative, laterLowerStatusPositive}, precFixedNow)
	if res.Kind != ResolutionKnown || res.Winner.Value != false {
		t.Fatalf("proven negative should not be displaced by a lower-verification positive: got %+v", res)
	}

	laterEqualStatusPositive := ev(SourceProviderDiscovery, VerificationObserved, 0.99, precFixedNow, true)
	laterEqualStatusPositive.Field = field
	res2 := Resolve(field, []Evidence{negative, laterEqualStatusPositive}, precFixedNow)
	if res2.Kind != ResolutionKnown || res2.Winner.Value != false {
		t.Fatalf("proven negative should not be displaced by an equal-verification positive: got %+v", res2)
	}

	higherStatusPositive := ev(SourceProviderDiscovery, VerificationVerified, 0.5, precFixedNow.Add(-time.Hour), true)
	higherStatusPositive.Field = field
	res3 := Resolve(field, []Evidence{negative, higherStatusPositive}, precFixedNow)
	if res3.Kind != ResolutionKnown || res3.Winner.Value != true {
		t.Fatalf("a strictly higher-verification positive must revalidate: got %+v", res3)
	}
}

// TestResolve_NarrowerBeatsBroader proves the engine resolves by evidence
// authority, never by taking the larger/more permissive numeric value: a
// verified probe's smaller cap beats a lower-authority source's larger
// claim.
func TestResolve_NarrowerBeatsBroader(t *testing.T) {
	narrowerProbe := ev(SourceVerifiedProbe, VerificationVerified, 0.9, precFixedNow.Add(-time.Hour), 128000)
	broaderExternal := ev(SourceExternalRegistry, VerificationDeclared, 0.99, precFixedNow, 1000000)

	res := Resolve("context_length", []Evidence{narrowerProbe, broaderExternal}, precFixedNow)
	if res.Kind != ResolutionKnown || res.Winner.Value != 128000 {
		t.Fatalf("expected the proven narrower cap (128000) to win over the broader external claim, got %+v", res)
	}
}

func TestResolve_HeuristicNeverCertifies(t *testing.T) {
	onlyHeuristic := ev(SourceHeuristic, VerificationDeclared, 0.9, precFixedNow, "heuristic-value")

	res := Resolve("context_length", []Evidence{onlyHeuristic}, precFixedNow)
	if res.Kind != ResolutionProbeSuggested {
		t.Fatalf("Resolve kind = %s, want probe_suggested when heuristic is the only evidence", res.Kind)
	}
	if res.Reason != ReasonHeuristicCannotCertify {
		t.Fatalf("Resolve reason = %s, want heuristic_cannot_certify", res.Reason)
	}

	// Even alongside a lower-confidence non-heuristic, heuristic never wins outright.
	weakerProviderDiscovery := ev(SourceProviderDiscovery, VerificationObserved, 0.1, precFixedNow.Add(-48*time.Hour), "provider-value")
	res2 := Resolve("context_length", []Evidence{onlyHeuristic, weakerProviderDiscovery}, precFixedNow)
	if res2.Kind != ResolutionKnown || res2.Winner.Source != SourceProviderDiscovery {
		t.Fatalf("a non-heuristic evidence, however weak, must win over heuristic: got %+v", res2)
	}
}

// TestResolve_OrderIndependence proves the same evidence set in original,
// reversed, and rotated order all produce an identical winner and reason.
// The last two entries are fully tied through source rank, verification,
// confidence, freshness, and scope specificity, so the outcome can only
// agree across orderings if the final deterministic tiebreak is itself
// order-independent.
func TestResolve_OrderIndependence(t *testing.T) {
	set := []Evidence{
		// "a" and "b" are fully tied (same source, verification, confidence,
		// freshness, and scope) — only the final deterministic tiebreak (on
		// Value) can separate them.
		ev(SourceProviderDiscovery, VerificationObserved, 0.4, precFixedNow.Add(-2*time.Hour), "a"),
		ev(SourceProviderDiscovery, VerificationObserved, 0.4, precFixedNow.Add(-2*time.Hour), "b"),
		ev(SourceHeuristic, VerificationDeclared, 0.99, precFixedNow, "c"),
		ev(SourceExternalRegistry, VerificationDeclared, 0.9, precFixedNow, "d"),
	}

	reversed := make([]Evidence, len(set))
	for i, e := range set {
		reversed[len(set)-1-i] = e
	}

	rotated := append(append([]Evidence{}, set[2:]...), set[:2]...)

	base := Resolve("context_length", set, precFixedNow)
	revRes := Resolve("context_length", reversed, precFixedNow)
	rotRes := Resolve("context_length", rotated, precFixedNow)

	if base.Kind != revRes.Kind || base.Winner.Value != revRes.Winner.Value {
		t.Fatalf("reversed order produced a different result: base=%+v reversed=%+v", base, revRes)
	}
	if base.Kind != rotRes.Kind || base.Winner.Value != rotRes.Winner.Value {
		t.Fatalf("rotated order produced a different result: base=%+v rotated=%+v", base, rotRes)
	}
}

func TestResolve_EmptyInputIsUnknown(t *testing.T) {
	res := Resolve("context_length", nil, precFixedNow)
	if res.Kind != ResolutionUnknown || res.Reason != ReasonNoEvidence {
		t.Fatalf("Resolve(empty) = %+v, want unknown/no_evidence", res)
	}
}

func TestResolve_AllDisqualifiedIsUnknown(t *testing.T) {
	futureDated := ev(SourceProviderDiscovery, VerificationObserved, 0.9, precFixedNow.Add(time.Hour), "future")
	invalidConfidence := ev(SourceExternalRegistry, VerificationDeclared, 1.5, precFixedNow, "bad-confidence")

	res := Resolve("context_length", []Evidence{futureDated, invalidConfidence}, precFixedNow)
	if res.Kind != ResolutionUnknown || res.Reason != ReasonAllDisqualified {
		t.Fatalf("Resolve(all disqualified) = %+v, want unknown/all_disqualified", res)
	}
}

func TestResolve_FiltersToRequestedFieldOnly(t *testing.T) {
	wrongField := ev(SourceOwnerOverride, VerificationVerified, 0.99, precFixedNow, "wrong-field-value")
	wrongField.Field = "quality_rating"

	res := Resolve("context_length", []Evidence{wrongField}, precFixedNow)
	if res.Kind != ResolutionUnknown || res.Reason != ReasonNoEvidence {
		t.Fatalf("evidence for a different field must be ignored: got %+v", res)
	}
}
