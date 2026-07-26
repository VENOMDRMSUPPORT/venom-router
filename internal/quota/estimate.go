package quota

import (
	"errors"
	"fmt"
)

// EstimateSource is the provenance tag for one estimated-consumption
// allocation (02 §3): whether the value came directly from the request,
// a verified provider credit-conversion rule, or a conservative policy
// default.
type EstimateSource string

const (
	EstimateSourceFromRequest        EstimateSource = "from_request"
	EstimateSourceProviderConversion EstimateSource = "provider_conversion"
	EstimateSourcePolicyDefault      EstimateSource = "policy_default"
)

// ErrUnknownEstimateSource is returned by ParseEstimateSource for any
// token outside the three canonical values.
var ErrUnknownEstimateSource = errors.New("quota: unknown estimate source")

func ParseEstimateSource(s string) (EstimateSource, error) {
	switch EstimateSource(s) {
	case EstimateSourceFromRequest, EstimateSourceProviderConversion, EstimateSourcePolicyDefault:
		return EstimateSource(s), nil
	default:
		return "", ErrUnknownEstimateSource
	}
}

// CreditConversionRule is a provider-specific token→credit conversion
// rate. Tokens are NEVER converted into provider credits without one
// (02 §3), and only when it is Verified.
type CreditConversionRule struct {
	Unit            Unit // must be UnitCredits or UnitBalance to ever apply
	CreditsPerToken float64
	Verified        bool
	RuleID          string
}

// EstimateInput is the already-counted input for one attempt's
// pre-execution consumption estimate. Token counting/normalization from
// the raw request is a later unit's concern (P4 request-normalize
// hook) — this type consumes an already-counted figure and owns the
// dimension set, provenance tagging, and the fail-closed credit rule.
type EstimateInput struct {
	// InputTokens is nil when not counted — never treated as 0.
	InputTokens *int
	// MaxOutputTokens is the request's max_tokens; nil ⇒ the policy
	// default applies.
	MaxOutputTokens *int
	// Conversion is nil ⇒ no credits/balance estimate is produced at all.
	Conversion *CreditConversionRule
}

// EstimatePolicy is the owner-policy conservative default used when a
// request omits max_tokens.
type EstimatePolicy struct {
	DefaultOutputTokens float64
}

// defaultOutputTokensFallback is the conservative owner-policy default
// output-token estimate (02 §3) when a request supplies no max_tokens.
const defaultOutputTokensFallback = 512

func DefaultEstimatePolicy() EstimatePolicy {
	return EstimatePolicy{DefaultOutputTokens: defaultOutputTokensFallback}
}

// ErrInvalidEstimatePolicy is returned by Estimate for a structurally
// invalid EstimatePolicy.
var ErrInvalidEstimatePolicy = errors.New("quota: invalid estimate policy")

// ErrInvalidEstimateInput is returned by Estimate for a negative
// InputTokens or MaxOutputTokens.
var ErrInvalidEstimateInput = errors.New("quota: invalid estimate input")

// Allocation is one canonical estimated-consumption dimension, ready to
// be reserved against its matching quota window.
type Allocation struct {
	Unit   Unit
	Cost   float64
	Source EstimateSource
}

// Estimate returns the canonical estimated-consumption allocations for
// one attempt (02 §3), in a FIXED deterministic order — built as an
// explicit slice, never by ranging over a map:
//
//  1. requests = 1, always present, from_request.
//  2. concurrency = 1, always present, from_request (against the
//     local-safety concurrency window).
//  3. input_tokens — emitted ONLY when InputTokens is counted; unknown
//     input is never coerced to a zero-cost allocation.
//  4. output_tokens — always present: the request's MaxOutputTokens when
//     supplied (from_request), else the policy default (policy_default).
//  5. credits/balance — emitted ONLY when Conversion is non-nil,
//     Verified, targets UnitCredits or UnitBalance, has a positive rate,
//     AND InputTokens is counted (a credit estimate needs both token
//     dimensions known; an unknown input token count is not silently
//     treated as 0 here either). In every other case NO credit
//     allocation is emitted and NO error is returned — 02 §3: an unsafe
//     credit estimate defers to local-safety and post-execution
//     reconciliation instead of guessing.
func Estimate(in EstimateInput, pol EstimatePolicy) ([]Allocation, error) {
	if pol.DefaultOutputTokens <= 0 {
		return nil, fmt.Errorf("%w: default output tokens must be positive, got %v", ErrInvalidEstimatePolicy, pol.DefaultOutputTokens)
	}
	if in.InputTokens != nil && *in.InputTokens < 0 {
		return nil, fmt.Errorf("%w: input tokens must not be negative, got %d", ErrInvalidEstimateInput, *in.InputTokens)
	}
	if in.MaxOutputTokens != nil && *in.MaxOutputTokens < 0 {
		return nil, fmt.Errorf("%w: max output tokens must not be negative, got %d", ErrInvalidEstimateInput, *in.MaxOutputTokens)
	}

	allocations := make([]Allocation, 0, 5)
	allocations = append(allocations, Allocation{Unit: UnitRequests, Cost: 1, Source: EstimateSourceFromRequest})
	allocations = append(allocations, Allocation{Unit: UnitConcurrency, Cost: 1, Source: EstimateSourceFromRequest})

	if in.InputTokens != nil {
		allocations = append(allocations, Allocation{Unit: UnitInputTokens, Cost: float64(*in.InputTokens), Source: EstimateSourceFromRequest})
	}

	outputTokens := pol.DefaultOutputTokens
	outputSource := EstimateSourcePolicyDefault
	if in.MaxOutputTokens != nil {
		outputTokens = float64(*in.MaxOutputTokens)
		outputSource = EstimateSourceFromRequest
	}
	allocations = append(allocations, Allocation{Unit: UnitOutputTokens, Cost: outputTokens, Source: outputSource})

	if rule := in.Conversion; rule != nil && rule.Verified && rule.CreditsPerToken > 0 &&
		(rule.Unit == UnitCredits || rule.Unit == UnitBalance) && in.InputTokens != nil {
		cost := (float64(*in.InputTokens) + outputTokens) * rule.CreditsPerToken
		allocations = append(allocations, Allocation{Unit: rule.Unit, Cost: cost, Source: EstimateSourceProviderConversion})
	}

	return allocations, nil
}
