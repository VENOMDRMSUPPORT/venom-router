package routing

import (
	"errors"
	"fmt"
	"math"
)

// FundingRule is a tier's closed funding policy (05 §1): Lite is
// free-only; Pro and Max admit free and paid accounts — never unknown
// funding, which is ineligible everywhere (05 §2 Step 3).
type FundingRule string

const (
	FundingFreeOnly    FundingRule = "free_only"
	FundingFreeAndPaid FundingRule = "free_and_paid"
)

// ScoreWeights are the Step-5 scoring factor weights (05 §2), each in
// [0, 1]; a scored tier's weights sum to exactly 1. A "—" cell in the
// 05 §2 table is a literal 0 here (Pro evidence-confidence, Max latency —
// the latter is tie-break only, carried by LatencyTieBreakOnly).
type ScoreWeights struct {
	Quality            float64
	Reliability        float64
	QuotaHeadroom      float64
	EvidenceConfidence float64
	CostClass          float64
	Latency            float64
}

// sum returns the total of all six weights.
func (w ScoreWeights) sum() float64 {
	return w.Quality + w.Reliability + w.QuotaHeadroom + w.EvidenceConfidence + w.CostClass + w.Latency
}

// each returns the six weights for per-field bounds checks.
func (w ScoreWeights) each() []float64 {
	return []float64{w.Quality, w.Reliability, w.QuotaHeadroom, w.EvidenceConfidence, w.CostClass, w.Latency}
}

// isZero reports whether every weight is exactly zero (the required shape
// for an unscored tier).
func (w ScoreWeights) isZero() bool {
	return w == ScoreWeights{}
}

// TierPolicy is one tier's complete, typed V1 policy (05 §1, §2, §8.4,
// §8.5). Policies are keyed by Tier only — no model-name or slug input
// exists anywhere in this package.
type TierPolicy struct {
	Tier    Tier
	Funding FundingRule

	// ContextCeilingTokens is the tier's hard context ceiling (05 §1):
	// a request larger than this is rejected, never clamped or promoted.
	ContextCeilingTokens int64

	// ThinkingCeiling is both the tier's default and its ceiling for the
	// thinking level (05 §1a).
	ThinkingCeiling ThinkingLevel

	// AttemptBudget bounds the Step-8 fallback loop (Lite 3 / Pro 4 / Max 5).
	AttemptBudget int

	// Scored is false for Lite: pure hard-eligibility, no quality/speed
	// scoring (05 §2 Step 5).
	Scored  bool
	Weights ScoreWeights

	// BandWidth is the Step-6 competitive band on the normalized quality
	// score (Pro 0.08 / Max 0.03; 05 §8.5). 0 means the tier has no band
	// (Lite, unscored). The band is never widened automatically.
	BandWidth float64

	// LatencyTieBreakOnly marks latency as a tie-break rather than a
	// scored factor (Lite and Max; 05 §1).
	LatencyTieBreakOnly bool
}

// ErrInvalidTierPolicy is returned by validate for any out-of-bound or
// invariant-violating policy value.
var ErrInvalidTierPolicy = errors.New("routing: tier policy out of bounds")

// weightSumTolerance bounds float accumulation error when checking that a
// scored tier's weights sum to exactly 1.
const weightSumTolerance = 1e-9

// Policies returns the three fixed V1 tier policies (05 §8.4: fixed, not
// owner-tunable in V1), freshly built per call and passed through
// validate so an out-of-bound value can never reach a caller.
func Policies() (map[Tier]TierPolicy, error) {
	policies := shippedPolicies()
	if err := validate(policies); err != nil {
		return nil, err
	}
	return policies, nil
}

// shippedPolicies builds the V1 policy table verbatim from 05 §1 (funding,
// ceilings, thinking, latency rows), §2 Step 5 (weights, attempt budgets)
// and §8.5 (band widths).
func shippedPolicies() map[Tier]TierPolicy {
	return map[Tier]TierPolicy{
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
}

// validate rejects any out-of-bound policy set: per-policy bounds, the
// Lite-specific invariants, and the cross-tier strictly ascending context
// ceilings. Everything wraps ErrInvalidTierPolicy.
func validate(policies map[Tier]TierPolicy) error {
	for _, tier := range []Tier{TierLite, TierPro, TierMax} {
		policy, ok := policies[tier]
		if !ok {
			return fmt.Errorf("%w: tier %q missing", ErrInvalidTierPolicy, tier)
		}
		if policy.Tier != tier {
			return fmt.Errorf("%w: policy keyed %q carries tier %q", ErrInvalidTierPolicy, tier, policy.Tier)
		}
		if err := validatePolicy(policy); err != nil {
			return err
		}
	}

	if err := validateLite(policies[TierLite]); err != nil {
		return err
	}

	lite, pro, max := policies[TierLite].ContextCeilingTokens, policies[TierPro].ContextCeilingTokens, policies[TierMax].ContextCeilingTokens
	if lite >= pro || pro >= max {
		return fmt.Errorf("%w: context ceilings must be strictly ascending lite < pro < max, got %d / %d / %d", ErrInvalidTierPolicy, lite, pro, max)
	}
	return nil
}

// validatePolicy checks one policy's tier-independent bounds.
func validatePolicy(p TierPolicy) error {
	switch p.Funding {
	case FundingFreeOnly, FundingFreeAndPaid:
	default:
		return fmt.Errorf("%w: %s funding rule %q unrecognized", ErrInvalidTierPolicy, p.Tier, p.Funding)
	}
	if _, ok := p.ThinkingCeiling.rank(); !ok {
		return fmt.Errorf("%w: %s thinking ceiling %q unrecognized", ErrInvalidTierPolicy, p.Tier, p.ThinkingCeiling)
	}
	if p.ContextCeilingTokens <= 0 {
		return fmt.Errorf("%w: %s context ceiling %d must be positive", ErrInvalidTierPolicy, p.Tier, p.ContextCeilingTokens)
	}
	if p.AttemptBudget < 1 || p.AttemptBudget > 10 {
		return fmt.Errorf("%w: %s attempt budget %d outside [1, 10]", ErrInvalidTierPolicy, p.Tier, p.AttemptBudget)
	}
	if p.Scored {
		return validateScoring(p)
	}
	if !p.Weights.isZero() {
		return fmt.Errorf("%w: %s is unscored but carries non-zero scoring weights", ErrInvalidTierPolicy, p.Tier)
	}
	if p.BandWidth != 0 {
		return fmt.Errorf("%w: %s is unscored but carries a competitive band %v", ErrInvalidTierPolicy, p.Tier, p.BandWidth)
	}
	return nil
}

// validateScoring checks a scored tier's weights and competitive band.
func validateScoring(p TierPolicy) error {
	for _, weight := range p.Weights.each() {
		if weight < 0 || weight > 1 {
			return fmt.Errorf("%w: %s scoring weight %v outside [0, 1]", ErrInvalidTierPolicy, p.Tier, weight)
		}
	}
	if diff := math.Abs(p.Weights.sum() - 1.0); diff > weightSumTolerance {
		return fmt.Errorf("%w: %s scoring weights sum to %v, want 1.0", ErrInvalidTierPolicy, p.Tier, p.Weights.sum())
	}
	if p.BandWidth <= 0 || p.BandWidth >= 1 {
		return fmt.Errorf("%w: %s competitive band %v outside (0, 1)", ErrInvalidTierPolicy, p.Tier, p.BandWidth)
	}
	return nil
}

// validateLite enforces the Lite product invariants (05 §1): unscored,
// free-only, thinking ceiling none.
func validateLite(lite TierPolicy) error {
	if lite.Scored {
		return fmt.Errorf("%w: lite must be unscored (pure hard-eligibility)", ErrInvalidTierPolicy)
	}
	if lite.Funding != FundingFreeOnly {
		return fmt.Errorf("%w: lite must be free-only — a paid offering never enters lite", ErrInvalidTierPolicy)
	}
	if lite.ThinkingCeiling != ThinkingNone {
		return fmt.Errorf("%w: lite thinking ceiling must be none, got %q", ErrInvalidTierPolicy, lite.ThinkingCeiling)
	}
	return nil
}
