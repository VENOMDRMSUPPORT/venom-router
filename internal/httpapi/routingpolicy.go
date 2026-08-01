package httpapi

import (
	"net/http"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/routing"
)

// routingpolicy.go serves GET /api/control/v1/routing/policy (P6-CAPI-EXTRA,
// enables P6-UI-003): the three V1 tier policies, read straight out of
// internal/routing.
//
// THE ONE RULE THIS FILE EXISTS TO ENFORCE: not a single policy VALUE is
// declared here. Every number, funding rule, thinking ceiling and band width is
// read from routing.Policies() — the same validated table the tier engine
// itself routes with (05 §1, §2 Step 5, §8.5). A literal in this file would be
// a second, silently-drifting copy of the router's policy, and the dashboard
// would faithfully display it while the router did something else.
//
// It is READ-ONLY, and deliberately so: 05 §8.4 defers owner weight tuning past
// V1, so there is no PUT. Accepting a write would advertise a capability the
// engine does not have.

// RoutingPolicyHandler serves the tier-policy read. Owner-session + CSRF gated
// via ControlMux's `gated`; a GET emits no audit event, like every other read
// (GET /settings, GET /accounts, GET /diagnostics/*).
type RoutingPolicyHandler struct {
	// policies is routing.Policies in production. It is a field rather than a
	// direct call so a test can force the validation-failure path — the
	// fail-closed branch below is the whole reason this endpoint is not a
	// one-liner, and an unreachable branch is an untested branch.
	policies func() (map[routing.Tier]routing.TierPolicy, error)
}

// NewRoutingPolicyHandler builds the handler over the real engine policy table.
func NewRoutingPolicyHandler() *RoutingPolicyHandler {
	return &RoutingPolicyHandler{policies: routing.Policies}
}

// tierScoreWeightsJSON is one scored tier's Step-5 factor weights (05 §2).
//
// A weight of 0 here is a REAL, declared zero, not an unknown: 05 §2's table
// has an explicit "—" for Pro's evidence-confidence and Max's latency, which
// routing.ScoreWeights carries as a literal 0 (see its doc comment). The
// not-applicable case is a whole tier being unscored, and that is represented by
// a null `weights` object on the tier — never by an all-zero one.
type tierScoreWeightsJSON struct {
	Quality            float64 `json:"quality"`
	Reliability        float64 `json:"reliability"`
	QuotaHeadroom      float64 `json:"quota_headroom"`
	EvidenceConfidence float64 `json:"evidence_confidence"`
	CostClass          float64 `json:"cost_class"`
	Latency            float64 `json:"latency"`
}

// tierPolicyJSON is one tier's complete served policy.
//
// Weights and CompetitiveBand are POINTERS because an unscored tier (Lite) has
// neither — that is a not-applicable, and routing.TierPolicy's own validator
// REQUIRES both to be zero for an unscored tier precisely because they carry no
// meaning there. Serializing those zeros would read as "quality is weighted at
// zero" and "the competitive band is zero wide", two scoring claims Lite never
// makes; `scored: false` plus two nulls states the truth instead.
//
// AttemptBudget is the tier's fallback depth (05 §2 Step 8) — the bound on the
// fallback loop. The POOL that loop may fall back within is `funding`, so the
// two together are the whole "fallback on exhaustion" row of 05 §1: Lite's
// free_only pool is what makes its fallback fail closed rather than reach for a
// paid offering.
type tierPolicyJSON struct {
	Tier                 string                `json:"tier"`
	Funding              string                `json:"funding"`
	ContextCeilingTokens int64                 `json:"context_ceiling_tokens"`
	ThinkingCeiling      string                `json:"thinking_ceiling"`
	AttemptBudget        int                   `json:"attempt_budget"`
	Scored               bool                  `json:"scored"`
	Weights              *tierScoreWeightsJSON `json:"weights"`
	CompetitiveBand      *float64              `json:"competitive_band"`
	LatencyTieBreakOnly  bool                  `json:"latency_tie_break_only"`
}

// routingPolicyJSON is the response body.
type routingPolicyJSON struct {
	Tiers []tierPolicyJSON `json:"tiers"`
}

// servedTierOrder is the fixed order tiers are served in — an EXPLICIT slice,
// never a range over Policies()' map, whose iteration order Go randomizes per
// run. A tier list that reshuffled between calls would make the Routing
// surface's reading order unstable for no reason.
var servedTierOrder = []routing.Tier{routing.TierLite, routing.TierPro, routing.TierMax}

// toTierPolicyJSON maps one engine policy to its wire shape. Every assignment
// below reads from p; there is no literal on the right-hand side of any of them.
func toTierPolicyJSON(p routing.TierPolicy) tierPolicyJSON {
	out := tierPolicyJSON{
		Tier:                 string(p.Tier),
		Funding:              string(p.Funding),
		ContextCeilingTokens: p.ContextCeilingTokens,
		ThinkingCeiling:      string(p.ThinkingCeiling),
		AttemptBudget:        p.AttemptBudget,
		Scored:               p.Scored,
		LatencyTieBreakOnly:  p.LatencyTieBreakOnly,
	}
	if p.Scored {
		out.Weights = &tierScoreWeightsJSON{
			Quality:            p.Weights.Quality,
			Reliability:        p.Weights.Reliability,
			QuotaHeadroom:      p.Weights.QuotaHeadroom,
			EvidenceConfidence: p.Weights.EvidenceConfidence,
			CostClass:          p.Weights.CostClass,
			Latency:            p.Weights.Latency,
		}
		band := p.BandWidth
		out.CompetitiveBand = &band
	}
	return out
}

// ServePolicy implements GET /api/control/v1/routing/policy.
//
// On a policy error the response is a typed 500 with NO data field at all — not
// the tiers that happened to validate. routing.Policies() validates the WHOLE
// table as a unit (cross-tier invariants included: ascending context ceilings,
// Lite's product rules), so a partial answer is not a smaller truth, it is an
// unvalidated one.
func (h *RoutingPolicyHandler) ServePolicy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}

	policies, err := h.policies()
	if err != nil {
		// The engine's error text names tiers and bounds; the client gets a
		// fixed, internal-free message instead.
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}

	out := routingPolicyJSON{Tiers: make([]tierPolicyJSON, 0, len(servedTierOrder))}
	for _, tier := range servedTierOrder {
		policy, ok := policies[tier]
		if !ok {
			// Policies() already rejects a missing tier, so this is unreachable
			// through the real seam — but a validated set that somehow lacked a
			// tier must fail closed rather than serve a two-tier router as if
			// that were the product.
			writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
			return
		}
		out.Tiers = append(out.Tiers, toTierPolicyJSON(policy))
	}

	writeData(w, http.StatusOK, out)
}
