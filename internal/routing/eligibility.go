package routing

import (
	"time"

	accountsdomain "github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

// BreakerSet holds the current circuit-breaker state for each scope, keyed by
// the identity a candidate exposes: account breakers by AccountID, offering
// breakers by ProviderModelID, provider breakers by ProviderID. A missing key
// means "no breaker" (implicitly closed).
type BreakerSet struct {
	Account  map[string]Breaker
	Offering map[string]Breaker
	Provider map[string]Breaker
}

// Blocks reports whether any applicable breaker denies the candidate as of now
// (an open, or unknown-state, breaker at any scope blocks the route). A
// half-open breaker with no probe in flight does NOT block — it admits the one
// trial request.
func (s BreakerSet) Blocks(c CandidateOffering, now time.Time) bool {
	if b, ok := s.Account[c.AccountID]; ok && !b.Admits(now) {
		return true
	}
	if b, ok := s.Offering[c.ProviderModelID]; ok && !b.Admits(now) {
		return true
	}
	if b, ok := s.Provider[c.ProviderID]; ok && !b.Admits(now) {
		return true
	}
	return false
}

// EligibilityInput carries the boundaries FilterEligible enforces: the tier's
// funding rule and the request's required capabilities (hard boundaries fallback
// may never cross), plus the current cooldowns and breakers (eligibility inputs,
// never sleeps).
type EligibilityInput struct {
	Funding              FundingRule
	RequiredCapabilities []Capability
	Cooldowns            []quota.Cooldown
	Breakers             BreakerSet
}

// FilterEligible removes candidates that fallback must not use as of now
// (05 §3): those on an active cooldown or blocked by an open breaker at any
// scope, AND — as a hard boundary that even total exhaustion may not cross —
// any candidate that violates the tier's funding rule (Lite never reaches paid)
// or lacks a required capability certification. It is pure and NEVER sleeps;
// cooldown is purely an eligibility input. The input slice is not mutated.
func FilterEligible(candidates []CandidateOffering, in EligibilityInput, now time.Time) []CandidateOffering {
	out := make([]CandidateOffering, 0, len(candidates))
	for _, c := range candidates {
		if !fundingAllowedByRule(in.Funding, c.Funding) {
			continue
		}
		if !capabilitiesCertified(c, in.RequiredCapabilities) {
			continue
		}
		if in.Breakers.Blocks(c, now) {
			continue
		}
		if cooledDown(in.Cooldowns, c, now) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// fundingAllowedByRule mirrors ApplyHardGates' Step-3 funding gate exactly:
// free-only admits only Free; free-and-paid admits Free or Paid (never Unknown).
func fundingAllowedByRule(rule FundingRule, f accountsdomain.Funding) bool {
	switch rule {
	case FundingFreeOnly:
		return f == accountsdomain.FundingFree
	case FundingFreeAndPaid:
		return f == accountsdomain.FundingFree || f == accountsdomain.FundingPaid
	default:
		return false
	}
}

// capabilitiesCertified mirrors ApplyHardGates' Step-3 capability gate exactly:
// every required capability must be certified+supported (via the Certifications
// map for mappable capabilities, or ReasoningCertified for CapabilityReasoning).
func capabilitiesCertified(c CandidateOffering, required []Capability) bool {
	for _, capability := range required {
		if op, ok := capability.OperationMapping(); ok {
			cert, certOk := c.Certifications[op]
			if !certOk || cert.State != models.CertCertified || cert.Truth != models.TruthSupported {
				return false
			}
		} else if !c.ReasoningCertified {
			return false
		}
	}
	return true
}

// cooledDown reports whether any active cooldown covers the candidate at its
// matching scope as of now.
func cooledDown(cooldowns []quota.Cooldown, c CandidateOffering, now time.Time) bool {
	for _, cd := range cooldowns {
		if !quota.IsOnCooldown(cd, now) {
			continue
		}
		switch cd.Scope {
		case quota.CooldownScopeAccount:
			if cd.AccountID != nil && *cd.AccountID == c.AccountID {
				return true
			}
		case quota.CooldownScopeOffering:
			if cd.OfferingOperationID != nil && *cd.OfferingOperationID == c.ProviderModelID {
				return true
			}
		case quota.CooldownScopeProvider:
			if cd.ProviderID != nil && *cd.ProviderID == c.ProviderID {
				return true
			}
		}
	}
	return false
}
