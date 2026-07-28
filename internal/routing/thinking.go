package routing

// ThinkingCandidate is the per-candidate certification truth the thinking
// normalization consumes (05 §1a). The caller feeds these from the read
// model (ROUTE-004 wiring); an unknown or uncertified candidate carries a
// certified maximum of none.
type ThinkingCandidate struct {
	// ReasoningCertified reports whether the offering-operation has
	// certified the reasoning capability at all.
	ReasoningCertified bool
	// CertifiedMax is the highest thinking level the offering certified.
	// It is consulted only when ReasoningCertified is true; a missing or
	// out-of-vocabulary value is treated as none (fail closed).
	CertifiedMax ThinkingLevel
}

// ThinkingDecision is everything the route-decision record and the
// X-Venom-* diagnostics headers need about one candidate's thinking
// normalization (05 §1a Diagnostics).
type ThinkingDecision struct {
	// Applied is the level the candidate would actually be driven at.
	Applied ThinkingLevel
	// TierClamped reports the tier-ceiling clamp (requested above the
	// tier ceiling); it is applied before any certified-max clamp.
	TierClamped bool
	// CertifiedClamped reports the per-offering certified-maximum clamp.
	CertifiedClamped bool
	// Degraded reports graceful degradation: the candidate cannot honor
	// the tier-clamped level and was dropped to its highest supported
	// level, down to none — never eliminated.
	Degraded bool
	// Ineligible is the one exception to degradation (05 §1a): reasoning
	// was explicitly required and this candidate lacks certified
	// reasoning. The Step-3 gate consumes this; the function itself
	// never errors.
	Ineligible bool
}

// NormalizeThinking resolves the thinking level for one candidate
// (05 §1a). It is a PURE function, re-invoked per fallback candidate so
// the level is re-clamped independently each time. Resolution order:
// (1) a nil requested level takes the tier default (= the tier ceiling);
// (2) the TIER-CEILING clamp runs first — a request above the ceiling
// clamps down and sets TierClamped, a downward request passes untouched;
// (3) then the CERTIFIED-MAX clamp degrades to the highest level the
// candidate supports, down to none, setting CertifiedClamped and
// Degraded — never eliminating the candidate. The only exception:
// reasoning explicitly required + no certified reasoning ⇒ Ineligible.
//
// A requested level outside the vocabulary is treated as nil (tier
// default): Normalize is the only producer of requested levels and
// already fails closed, so this is a defensive fallback, never a bypass.
func NormalizeThinking(requested *ThinkingLevel, reasoningRequired bool, policy TierPolicy, candidate ThinkingCandidate) ThinkingDecision {
	var decision ThinkingDecision

	// (1) + (2): tier default, then the tier-ceiling clamp.
	effective := policy.ThinkingCeiling
	if requested != nil {
		if _, ok := requested.rank(); ok {
			if requested.atMost(policy.ThinkingCeiling) {
				effective = *requested
			} else {
				effective = policy.ThinkingCeiling
				decision.TierClamped = true
			}
		}
	}

	// (3): the certified-max clamp — degradation, never elimination.
	certifiedMax := ThinkingNone
	if candidate.ReasoningCertified {
		if _, ok := candidate.CertifiedMax.rank(); ok {
			certifiedMax = candidate.CertifiedMax
		}
	}
	if effective.atMost(certifiedMax) {
		decision.Applied = effective
	} else {
		decision.Applied = certifiedMax
		decision.CertifiedClamped = true
		decision.Degraded = true
	}

	if reasoningRequired && !candidate.ReasoningCertified {
		decision.Ineligible = true
	}

	return decision
}

// MechanismKind is the closed vocabulary of certified reasoning
// mechanisms an offering-operation can carry (05 §1a): a provider
// effort/mode flag, a bounded reasoning token budget, or a distinct
// reasoning variant exposed as its own offering-operation.
type MechanismKind string

const (
	MechanismEffortFlag       MechanismKind = "effort_flag"
	MechanismTokenBudget      MechanismKind = "token_budget"
	MechanismReasoningVariant MechanismKind = "reasoning_variant"
)

// ThinkingMechanismDescriptor describes the reasoning mechanism
// DISCOVERED/CERTIFIED for one offering-operation. It deliberately
// carries no model or provider identifier, so a ThinkingMapper
// implementation structurally cannot select a mapping by model name
// (05 §1a: "never inferred from the model name").
type ThinkingMechanismDescriptor struct {
	Kind MechanismKind
}

// ThinkingMapping is the provider-neutral shape of a mapped thinking
// level: exactly one field is meaningful, selected by the descriptor's
// kind.
type ThinkingMapping struct {
	// EffortFlag is the provider effort/mode value (effort_flag).
	EffortFlag string
	// TokenBudget is the bounded reasoning token count (token_budget).
	TokenBudget int64
	// VariantOfferingOperationID names the distinct reasoning-variant
	// offering-operation (reasoning_variant).
	VariantOfferingOperationID string
}

// ThinkingMapper is the adapter PORT that maps an applied level to the
// provider mechanism the offering certified. Implementations live behind
// the dispatcher seam (P4-EXEC-001 and per-provider P7 work) — there is
// deliberately no implementation in this package.
type ThinkingMapper interface {
	MapThinking(applied ThinkingLevel, mechanism ThinkingMechanismDescriptor) (ThinkingMapping, error)
}
