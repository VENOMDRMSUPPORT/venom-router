package routing

import (
	"testing"
)

// testPolicies returns the shipped validated policies for thinking tests.
func testPolicies(t *testing.T) map[Tier]TierPolicy {
	t.Helper()
	policies, err := Policies()
	if err != nil {
		t.Fatalf("Policies() error = %v, want nil", err)
	}
	return policies
}

// fullyCertified is a candidate that can honor every level.
func fullyCertified() ThinkingCandidate {
	return ThinkingCandidate{ReasoningCertified: true, CertifiedMax: ThinkingUltra}
}

// uncertified is a candidate with no certified reasoning at all.
func uncertified() ThinkingCandidate {
	return ThinkingCandidate{}
}

// level returns a pointer to l for requested-level literals.
func level(l ThinkingLevel) *ThinkingLevel {
	return &l
}

// TestNormalizeThinking_TierDefaults proves a nil requested level takes
// the tier default (= the tier ceiling, 05 §1a) with no clamp flags:
// lite → none, pro → extended, max → ultra.
func TestNormalizeThinking_TierDefaults(t *testing.T) {
	policies := testPolicies(t)
	want := map[Tier]ThinkingLevel{
		TierLite: ThinkingNone,
		TierPro:  ThinkingExtended,
		TierMax:  ThinkingUltra,
	}
	for tier, wantLevel := range want {
		decision := NormalizeThinking(nil, false, policies[tier], fullyCertified())
		if decision.Applied != wantLevel {
			t.Errorf("nil requested on %s applied = %q, want %q", tier, decision.Applied, wantLevel)
		}
		if decision.TierClamped || decision.CertifiedClamped || decision.Degraded || decision.Ineligible {
			t.Errorf("nil requested on %s set flags %+v, want none (a default is not a clamp)", tier, decision)
		}
	}
}

// TestNormalizeThinking_DownwardOverrideHonored proves a request below
// the tier ceiling passes untouched: none on Pro stays none, no flags.
func TestNormalizeThinking_DownwardOverrideHonored(t *testing.T) {
	policies := testPolicies(t)
	decision := NormalizeThinking(level(ThinkingNone), false, policies[TierPro], fullyCertified())
	if decision.Applied != ThinkingNone {
		t.Fatalf("none requested on pro applied = %q, want %q", decision.Applied, ThinkingNone)
	}
	if decision.TierClamped || decision.CertifiedClamped || decision.Degraded || decision.Ineligible {
		t.Fatalf("downward override set flags %+v, want none", decision)
	}
}

// TestNormalizeThinking_TierCeilingClamp proves a request above the tier
// ceiling clamps down to the ceiling and reports TierClamped (05 §1a):
// ultra on Pro → extended; ultra on Lite → none.
func TestNormalizeThinking_TierCeilingClamp(t *testing.T) {
	policies := testPolicies(t)
	cases := []struct {
		tier Tier
		want ThinkingLevel
	}{
		{TierPro, ThinkingExtended},
		{TierLite, ThinkingNone},
	}
	for _, tc := range cases {
		decision := NormalizeThinking(level(ThinkingUltra), false, policies[tc.tier], fullyCertified())
		if decision.Applied != tc.want {
			t.Errorf("ultra on %s applied = %q, want %q", tc.tier, decision.Applied, tc.want)
		}
		if !decision.TierClamped {
			t.Errorf("ultra on %s TierClamped = false, want true", tc.tier)
		}
		if decision.CertifiedClamped || decision.Degraded || decision.Ineligible {
			t.Errorf("ultra on %s (fully certified candidate) = %+v, want tier clamp only", tc.tier, decision)
		}
	}
}

// TestNormalizeThinking_TierClampRunsBeforeCertifiedClamp is the ordering
// proof: requested ultra, tier Pro (ceiling extended), certified max
// standard must apply standard with BOTH TierClamped (ultra → extended)
// and CertifiedClamped (extended → standard). That flag pair is only
// producible when the tier clamp runs first: had the certified clamp run
// first (ultra → standard), the tier check would then see standard ≤
// extended and never set TierClamped — the intermediate is never compared
// as ultra-vs-certified.
func TestNormalizeThinking_TierClampRunsBeforeCertifiedClamp(t *testing.T) {
	policies := testPolicies(t)
	candidate := ThinkingCandidate{ReasoningCertified: true, CertifiedMax: ThinkingStandard}

	decision := NormalizeThinking(level(ThinkingUltra), false, policies[TierPro], candidate)
	if decision.Applied != ThinkingStandard {
		t.Fatalf("applied = %q, want %q", decision.Applied, ThinkingStandard)
	}
	if !decision.TierClamped || !decision.CertifiedClamped {
		t.Fatalf("flags = %+v, want BOTH TierClamped and CertifiedClamped (tier clamp must run first)", decision)
	}
}

// TestNormalizeThinking_DegradeVsReject proves graceful degradation is
// the default and elimination is the explicit-requirement exception
// (05 §1a): an uncertified candidate degrades to none — Degraded, never
// Ineligible — unless reasoning is in the explicit required set, which
// flips Ineligible (and only that flips it).
func TestNormalizeThinking_DegradeVsReject(t *testing.T) {
	policies := testPolicies(t)

	degraded := NormalizeThinking(level(ThinkingExtended), false, policies[TierPro], uncertified())
	if degraded.Applied != ThinkingNone {
		t.Fatalf("uncertified applied = %q, want %q (degrade to highest supported, down to none)", degraded.Applied, ThinkingNone)
	}
	if !degraded.Degraded || !degraded.CertifiedClamped {
		t.Fatalf("uncertified decision = %+v, want Degraded and CertifiedClamped", degraded)
	}
	if degraded.Ineligible {
		t.Fatalf("uncertified but not explicitly required: Ineligible = true, want false (degradation, not elimination)")
	}

	rejected := NormalizeThinking(level(ThinkingExtended), true, policies[TierPro], uncertified())
	if !rejected.Ineligible {
		t.Fatalf("reasoning explicitly required + uncertified candidate: Ineligible = false, want true (Step-3 gate input)")
	}

	// The exception cuts one way only: explicit requirement against a
	// candidate WITH certified reasoning stays eligible.
	eligible := NormalizeThinking(level(ThinkingExtended), true, policies[TierPro], fullyCertified())
	if eligible.Ineligible {
		t.Fatalf("reasoning required + certified candidate: Ineligible = true, want false")
	}
	if eligible.Applied != ThinkingExtended {
		t.Fatalf("reasoning required + certified candidate applied = %q, want %q", eligible.Applied, ThinkingExtended)
	}

	// No degradation flags when nothing was lowered: none requested on an
	// uncertified candidate already fits.
	fits := NormalizeThinking(level(ThinkingNone), false, policies[TierPro], uncertified())
	if fits.Degraded || fits.CertifiedClamped || fits.TierClamped || fits.Ineligible {
		t.Fatalf("none on uncertified candidate = %+v, want no flags", fits)
	}
}

// TestNormalizeThinking_PerCandidateReClamp proves fallback preservation
// (05 §1a): the same requested level evaluated against two candidates
// with different certified maxima yields independent decisions — one
// candidate's degradation never leaks into another's.
func TestNormalizeThinking_PerCandidateReClamp(t *testing.T) {
	policies := testPolicies(t)
	weak := ThinkingCandidate{ReasoningCertified: true, CertifiedMax: ThinkingStandard}

	strongDecision := NormalizeThinking(level(ThinkingUltra), false, policies[TierMax], fullyCertified())
	weakDecision := NormalizeThinking(level(ThinkingUltra), false, policies[TierMax], weak)
	strongAgain := NormalizeThinking(level(ThinkingUltra), false, policies[TierMax], fullyCertified())

	if strongDecision.Applied != ThinkingUltra || strongDecision.CertifiedClamped {
		t.Fatalf("strong candidate = %+v, want ultra applied, no certified clamp", strongDecision)
	}
	if weakDecision.Applied != ThinkingStandard || !weakDecision.CertifiedClamped || !weakDecision.Degraded {
		t.Fatalf("weak candidate = %+v, want standard applied with CertifiedClamped and Degraded", weakDecision)
	}
	if strongAgain != strongDecision {
		t.Fatalf("re-evaluating the strong candidate after the weak one changed the decision: %+v vs %+v (must be pure)", strongAgain, strongDecision)
	}
}

// TestNormalizeThinking_UncertifiedMaxIsFailClosed proves a candidate
// whose certified max is missing or outside the vocabulary is treated as
// certified max none — never trusted upward (fail closed) — and that a
// ReasoningCertified=false candidate's CertifiedMax is ignored even when
// set high.
func TestNormalizeThinking_UncertifiedMaxIsFailClosed(t *testing.T) {
	policies := testPolicies(t)

	bogusMax := NormalizeThinking(level(ThinkingExtended), false, policies[TierPro],
		ThinkingCandidate{ReasoningCertified: true, CertifiedMax: ThinkingLevel("hyper")})
	if bogusMax.Applied != ThinkingNone || !bogusMax.Degraded {
		t.Fatalf("bogus certified max = %+v, want applied none + Degraded (unknown ⇒ none)", bogusMax)
	}

	liar := NormalizeThinking(level(ThinkingExtended), false, policies[TierPro],
		ThinkingCandidate{ReasoningCertified: false, CertifiedMax: ThinkingUltra})
	if liar.Applied != ThinkingNone || !liar.Degraded {
		t.Fatalf("uncertified candidate with high CertifiedMax = %+v, want applied none + Degraded (certification flag wins)", liar)
	}
}
