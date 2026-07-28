package routing

import (
	"errors"
	"testing"
)

// mustPolicies returns the shipped, validated V1 policies or fails the
// test.
func mustPolicies(t *testing.T) map[Tier]TierPolicy {
	t.Helper()
	policies, err := Policies()
	if err != nil {
		t.Fatalf("Policies() error = %v, want nil (shipped V1 policies must validate)", err)
	}
	return policies
}

// TestPolicies_ExactSpecValues asserts every field of all three shipped
// policies equals the 05 §1 / §2 / §8.5 values exactly — the policy table
// is product policy, so any drift is a defect, not tuning.
func TestPolicies_ExactSpecValues(t *testing.T) {
	want := map[Tier]TierPolicy{
		TierLite: {
			Tier:                 TierLite,
			Funding:              FundingFreeOnly,
			ContextCeilingTokens: 262144,
			ThinkingCeiling:      ThinkingNone,
			AttemptBudget:        3,
			Scored:               false,
			Weights:              ScoreWeights{},
			BandWidth:            0,
			LatencyTieBreakOnly:  true,
		},
		TierPro: {
			Tier:                 TierPro,
			Funding:              FundingFreeAndPaid,
			ContextCeilingTokens: 524288,
			ThinkingCeiling:      ThinkingExtended,
			AttemptBudget:        4,
			Scored:               true,
			Weights: ScoreWeights{
				Quality:            0.40,
				Reliability:        0.25,
				QuotaHeadroom:      0.15,
				EvidenceConfidence: 0,
				CostClass:          0.15,
				Latency:            0.05,
			},
			BandWidth:           0.08,
			LatencyTieBreakOnly: false,
		},
		TierMax: {
			Tier:                 TierMax,
			Funding:              FundingFreeAndPaid,
			ContextCeilingTokens: 1048576,
			ThinkingCeiling:      ThinkingUltra,
			AttemptBudget:        5,
			Scored:               true,
			Weights: ScoreWeights{
				Quality:            0.60,
				Reliability:        0.20,
				QuotaHeadroom:      0.05,
				EvidenceConfidence: 0.10,
				CostClass:          0.05,
				Latency:            0,
			},
			BandWidth:           0.03,
			LatencyTieBreakOnly: true,
		},
	}

	got := mustPolicies(t)
	if len(got) != len(want) {
		t.Fatalf("Policies() returned %d tiers, want %d", len(got), len(want))
	}
	for tier, wantPolicy := range want {
		gotPolicy, ok := got[tier]
		if !ok {
			t.Fatalf("Policies() missing tier %q", tier)
		}
		if gotPolicy != wantPolicy {
			t.Errorf("Policies()[%q] = %+v, want %+v", tier, gotPolicy, wantPolicy)
		}
	}
}

// TestValidate_ShippedPoliciesPass is the happy direction of validate():
// the exact shipped set must be accepted.
func TestValidate_ShippedPoliciesPass(t *testing.T) {
	if err := validate(shippedPolicies()); err != nil {
		t.Fatalf("validate(shippedPolicies()) error = %v, want nil", err)
	}
}

// corrupt returns the shipped policies with one tier's policy replaced by
// mutate's output.
func corrupt(tier Tier, mutate func(TierPolicy) TierPolicy) map[Tier]TierPolicy {
	policies := shippedPolicies()
	policies[tier] = mutate(policies[tier])
	return policies
}

// TestValidate_RejectsOutOfBoundPolicies proves validate() fails closed on
// each class of out-of-bound value with the typed ErrInvalidTierPolicy.
func TestValidate_RejectsOutOfBoundPolicies(t *testing.T) {
	cases := []struct {
		name     string
		policies map[Tier]TierPolicy
	}{
		{
			name: "weights sum 0.99",
			policies: corrupt(TierPro, func(p TierPolicy) TierPolicy {
				p.Weights.Quality = 0.39
				return p
			}),
		},
		{
			name: "band 0 on a scored tier",
			policies: corrupt(TierPro, func(p TierPolicy) TierPolicy {
				p.BandWidth = 0
				return p
			}),
		},
		{
			name: "band 1.2",
			policies: corrupt(TierMax, func(p TierPolicy) TierPolicy {
				p.BandWidth = 1.2
				return p
			}),
		},
		{
			name: "descending ceilings",
			policies: corrupt(TierMax, func(p TierPolicy) TierPolicy {
				p.ContextCeilingTokens = 131072
				return p
			}),
		},
		{
			name: "Lite scored",
			policies: corrupt(TierLite, func(p TierPolicy) TierPolicy {
				p.Scored = true
				p.Weights = ScoreWeights{Quality: 1.0}
				p.BandWidth = 0.05
				return p
			}),
		},
		{
			name: "Lite paid-allowed",
			policies: corrupt(TierLite, func(p TierPolicy) TierPolicy {
				p.Funding = FundingFreeAndPaid
				return p
			}),
		},
		{
			name: "Lite thinking above none",
			policies: corrupt(TierLite, func(p TierPolicy) TierPolicy {
				p.ThinkingCeiling = ThinkingExtended
				return p
			}),
		},
		{
			name: "attempt budget 0",
			policies: corrupt(TierPro, func(p TierPolicy) TierPolicy {
				p.AttemptBudget = 0
				return p
			}),
		},
		{
			name: "attempt budget 11",
			policies: corrupt(TierMax, func(p TierPolicy) TierPolicy {
				p.AttemptBudget = 11
				return p
			}),
		},
		{
			name: "negative weight",
			policies: corrupt(TierPro, func(p TierPolicy) TierPolicy {
				p.Weights.Latency = -0.05
				p.Weights.Quality = 0.50
				return p
			}),
		},
		{
			name: "non-positive ceiling",
			policies: corrupt(TierLite, func(p TierPolicy) TierPolicy {
				p.ContextCeilingTokens = 0
				return p
			}),
		},
		{
			name: "unknown funding rule",
			policies: corrupt(TierPro, func(p TierPolicy) TierPolicy {
				p.Funding = FundingRule("donations")
				return p
			}),
		},
		{
			name: "unknown thinking ceiling",
			policies: corrupt(TierMax, func(p TierPolicy) TierPolicy {
				p.ThinkingCeiling = ThinkingLevel("hyper")
				return p
			}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validate(tc.policies)
			if !errors.Is(err, ErrInvalidTierPolicy) {
				t.Fatalf("validate() error = %v, want ErrInvalidTierPolicy", err)
			}
		})
	}
}

// TestPolicies_ReturnsFreshCopy proves a caller mutating the returned map
// cannot poison a later caller's view of the fixed V1 policy (05 §8.4).
func TestPolicies_ReturnsFreshCopy(t *testing.T) {
	first := mustPolicies(t)
	tampered := first[TierLite]
	tampered.Funding = FundingFreeAndPaid
	first[TierLite] = tampered

	second := mustPolicies(t)
	if second[TierLite].Funding != FundingFreeOnly {
		t.Fatalf("Policies() second call Lite funding = %q, want %q (fresh copy per call)", second[TierLite].Funding, FundingFreeOnly)
	}
}
