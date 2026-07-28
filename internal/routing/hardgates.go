package routing

import (
	"errors"

	accountsdomain "github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

// Exclusion reason constants are diagnostic codes carried in the excluded
// map for each failing candidate (05 §2 Step 3). They are routing-internal
// identifiers, not the public API error codes that ROUTE-015 owns.
const (
	ReasonFundingIneligible     = "funding_ineligible"
	ReasonContextUnverified     = "context_unverified"
	ReasonCapabilityUncertified = "capability_uncertified"
)

// ErrContextExceedsTier is a request-level sentinel: the request's total
// context need S exceeds the tier's hard ceiling (05 §2 Step 3). It is
// checked once before any candidate is evaluated and short-circuits the
// entire gate pass — no per-candidate exclusion map is produced.
var ErrContextExceedsTier = errors.New("routing: request context exceeds tier ceiling")

// ApplyHardGates runs the Step-3 eligibility gates (05 §2 Step 3) against
// the candidate pool produced by BuildCandidatePool:
//
//  1. Request-level ceiling check: reqs.ContextTokens > policy ceiling →
//     return (nil, nil, ErrContextExceedsTier) immediately.
//  2. Funding gate: Lite (FundingFreeOnly) → only FundingFree passes;
//     Pro/Max (FundingFreeAndPaid) → Free and Paid pass, Unknown always
//     excluded.
//  3. Context gate (per candidate): VerifiedContextTokens nil, or
//     *VerifiedContextTokens < reqs.ContextTokens → excluded.
//  4. Capability gate: every capability in reqs must be certified+supported,
//     using the Certifications map for the five mappable capabilities and
//     ReasoningCertified for CapabilityReasoning (which has no operation
//     mapping — 05 §2 Step 3, 04 vocabulary gap).
//
// A candidate excluded for ANY reason does not appear in eligible. excluded
// maps each excluded candidate's index in the INPUT slice to one or more
// reason constants; a single candidate may carry reasons from multiple gates.
// The input slice is never mutated.
func ApplyHardGates(candidates []CandidateOffering, reqs Requirements, policy TierPolicy) (eligible []CandidateOffering, excluded map[int][]string, err error) {
	if reqs.ContextTokens > policy.ContextCeilingTokens {
		return nil, nil, ErrContextExceedsTier
	}

	eligible = make([]CandidateOffering, 0, len(candidates))
	excluded = make(map[int][]string)

	for i, c := range candidates {
		var reasons []string

		// Gate 2: funding.
		switch policy.Funding {
		case FundingFreeOnly:
			if c.Funding != accountsdomain.FundingFree {
				reasons = append(reasons, ReasonFundingIneligible)
			}
		case FundingFreeAndPaid:
			if c.Funding != accountsdomain.FundingFree && c.Funding != accountsdomain.FundingPaid {
				reasons = append(reasons, ReasonFundingIneligible)
			}
		}

		// Gate 3: verified context ceiling ≥ S.
		if c.VerifiedContextTokens == nil || *c.VerifiedContextTokens < reqs.ContextTokens {
			reasons = append(reasons, ReasonContextUnverified)
		}

		// Gate 4: required capabilities all certified.
		capabilityFailed := false
		for _, cap := range reqs.Capabilities {
			if op, ok := cap.OperationMapping(); ok {
				cert, certOk := c.Certifications[op]
				if !certOk || cert.State != models.CertCertified || cert.Truth != models.TruthSupported {
					capabilityFailed = true
				}
			} else {
				// CapabilityReasoning: no operation mapping; gate on ReasoningCertified.
				if !c.ReasoningCertified {
					capabilityFailed = true
				}
			}
		}
		if capabilityFailed {
			reasons = append(reasons, ReasonCapabilityUncertified)
		}

		if len(reasons) > 0 {
			excluded[i] = reasons
		} else {
			eligible = append(eligible, c)
		}
	}

	return eligible, excluded, nil
}
