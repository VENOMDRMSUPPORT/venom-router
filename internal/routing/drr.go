package routing

import (
	"math"
	"sort"
	"time"

	accountsdomain "github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

// SelectMaxAccount is Max's Step-7 account distribution (05 §2 Step 7 stage 2–3,
// §8.3): quota-fair Deficit Round-Robin followed by a Power-of-Two-Choices final
// pick, with NO funding-mix target. It runs the three ordered stages over one
// route group's members (the group already chosen by Step 6's competitive band):
//
//  1. SaturationFilter — drop accounts saturated on ANY applicable quota window
//     (fail closed on unknown data; fail OPEN only if every account is saturated).
//  2. DRR crediting — credit each eligible account quantum = weight/Σweight,
//     weighting by available capacity (the binding window's headroom).
//  3. P2C — from the top two accounts by accumulated credit, pick the one with
//     the better live signal, then debit the served account.
//
// It is pure given its inputs: the returned DRRState is a fresh map (the caller's
// state is never mutated), and no wall clock or randomness is used — `now` is
// injected. ok is false only when members is empty. Funding rides along on the
// chosen candidate (observable/auditable) but never influences the selection.
func SelectMaxAccount(members []CandidateOffering, state DRRState, need float64, now time.Time, staleAfter time.Duration) (CandidateOffering, DRRState, bool) {
	updated := state.clone()
	if len(members) == 0 {
		return CandidateOffering{}, updated, false
	}

	eligible, _ := SaturationFilter(members, need, now, staleAfter)
	if len(eligible) == 0 {
		// SaturationFilter fails open (returns all) rather than emptying, so
		// this is unreachable for a non-empty group; guard defensively.
		return CandidateOffering{}, updated, false
	}

	creditEligible(eligible, updated)
	top2 := topTwoByCredit(eligible, updated)
	chosen := p2cPick(top2)
	updated[chosen.AccountID]--
	return chosen, updated, true
}

// DRRState maps an AccountID to its accumulated Deficit Round-Robin credit
// (05 §2 Step 7 stage 2). It is a pure in-memory value; persisting it across
// process restarts is P5's job, not this package's.
type DRRState map[string]float64

// clone returns a fresh copy so a returned state can never alias the caller's.
func (s DRRState) clone() DRRState {
	out := make(DRRState, len(s)+1)
	for k, v := range s {
		out[k] = v
	}
	return out
}

// SaturationFilter is Max's Step-7 stage 1 (05 §2 Step 7, §4). For each account
// it evaluates every applicable quota window and combines them via the
// most-restrictive rule: an account whose combined state is anything other than
// StateAvailable — exhausted, insufficient, stale, or unknown (including an
// account with no window data at all) — is SATURATED for this attempt and
// excluded (fail closed; unknown is never treated as available).
//
// If EVERY account is saturated the group fails OPEN — all members are returned
// and allSaturated is true — so a fully-saturated group still yields candidates
// rather than nothing. Otherwise only the non-saturated accounts are returned.
func SaturationFilter(members []CandidateOffering, need float64, now time.Time, staleAfter time.Duration) (eligible []CandidateOffering, allSaturated bool) {
	eligible = make([]CandidateOffering, 0, len(members))
	for _, c := range members {
		if !isSaturated(c, need, now, staleAfter) {
			eligible = append(eligible, c)
		}
	}
	if len(eligible) == 0 {
		// Every account saturated: fail open with the full set (05 §2 Step 7).
		out := make([]CandidateOffering, len(members))
		copy(out, members)
		return out, true
	}
	return eligible, false
}

// isSaturated reports whether an account cannot prove availability across all
// its applicable windows. A nil/empty window set yields StateUnknown via
// MostRestrictive (fail closed), so a data-less account is saturated.
func isSaturated(c CandidateOffering, need float64, now time.Time, staleAfter time.Duration) bool {
	states := make([]quota.WindowState, 0, len(c.QuotaWindows))
	for _, w := range c.QuotaWindows {
		states = append(states, w.State(need, now, staleAfter))
	}
	return quota.MostRestrictive(states) != quota.StateAvailable
}

// DRRRound is one pure Deficit Round-Robin step over an already-eligible account
// set (05 §2 Step 7 stage 2): credit every account quantum = weight/Σweight,
// select the account with the greatest accumulated credit (ties broken by input
// order for determinism), and debit the selected account one unit. The returned
// DRRState is fresh; the caller's is never mutated. Over many rounds each
// account's selection frequency converges to its share of total weight.
//
// This is the strict-DRR core with no P2C refinement — the convergence gate
// drives it directly. SelectMaxAccount layers P2C on top of the same crediting.
func DRRRound(eligible []CandidateOffering, state DRRState) (CandidateOffering, DRRState) {
	updated := state.clone()
	if len(eligible) == 0 {
		return CandidateOffering{}, updated
	}
	creditEligible(eligible, updated)

	best := 0
	for i := 1; i < len(eligible); i++ {
		if updated[eligible[i].AccountID] > updated[eligible[best].AccountID] {
			best = i
		}
	}
	chosen := eligible[best]
	updated[chosen.AccountID]--
	return chosen, updated
}

// creditEligible adds this round's quantum to every eligible account's credit.
// The quantum is proportional to available capacity: quantum_i = weight_i /
// Σweight, so the round's total credit is exactly 1 and long-run selection
// frequency tracks the capacity ratio. When no account has known capacity
// (Σweight == 0 — only possible in the fail-open all-saturated case), every
// account is credited an equal 1/n share so DRR still makes deterministic
// progress instead of dividing by zero.
func creditEligible(eligible []CandidateOffering, credit DRRState) {
	weights := make([]float64, len(eligible))
	var total float64
	for i, c := range eligible {
		w := accountWeight(c)
		weights[i] = w
		total += w
	}
	n := float64(len(eligible))
	for i, c := range eligible {
		q := 1.0 / n
		if total > 0 {
			q = weights[i] / total
		}
		credit[c.AccountID] += q
	}
}

// accountWeight is an account's DRR weight: the available capacity across all
// its applicable quota windows and local-safety budget, taken as the MINIMUM
// headroom across its windows (the binding window — an account can serve no more
// than its scarcest window allows). Windows whose capacity is unknown contribute
// nothing; an account with no known capacity weighs 0 (it only reaches DRR at
// all via the fail-open exception, where creditEligible falls back to uniform).
func accountWeight(c CandidateOffering) float64 {
	minHeadroom := math.Inf(1)
	seen := false
	for _, w := range c.QuotaWindows {
		h, ok := w.Headroom()
		if !ok {
			continue
		}
		seen = true
		if h < minHeadroom {
			minHeadroom = h
		}
	}
	if !seen {
		return 0
	}
	if minHeadroom < 0 {
		minHeadroom = 0
	}
	return minHeadroom
}

// topTwoByCredit returns up to the two eligible accounts with the greatest
// accumulated credit, ties broken by input order (stable). These are the DRR
// candidates P2C chooses between.
func topTwoByCredit(eligible []CandidateOffering, credit DRRState) []CandidateOffering {
	idx := make([]int, len(eligible))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		return credit[eligible[idx[a]].AccountID] > credit[eligible[idx[b]].AccountID]
	})
	limit := 2
	if len(idx) < limit {
		limit = len(idx)
	}
	out := make([]CandidateOffering, limit)
	for i := 0; i < limit; i++ {
		out[i] = eligible[idx[i]]
	}
	return out
}

// p2cPick is Max's Step-7 stage 3 Power-of-Two-Choices (05 §2 Step 7): between
// the top two DRR candidates, choose the one with the better live signal. The
// second candidate wins only if it is STRICTLY better than the DRR leader; on a
// full tie the DRR leader (top2[0]) is kept, so P2C never overturns DRR's
// fairness without a concrete live reason.
func p2cPick(top2 []CandidateOffering) CandidateOffering {
	if len(top2) == 0 {
		return CandidateOffering{}
	}
	if len(top2) == 1 {
		return top2[0]
	}
	leader, challenger := top2[0], top2[1]
	if p2cBetter(challenger, leader) {
		return challenger
	}
	return leader
}

// p2cBetter reports whether x is strictly better than y by the P2C live-signal
// ladder (05 §2 Step 7 stage 3), in priority order: fewer in-flight requests,
// then healthier account, then better latency score, then more reserved-capacity
// headroom. Funding is deliberately ABSENT from this ladder — Max applies no
// funding-mix target. A full tie returns false (neither strictly better).
func p2cBetter(x, y CandidateOffering) bool {
	if x.InFlightCount != y.InFlightCount {
		return x.InFlightCount < y.InFlightCount
	}
	if rx, ry := healthRank(x.AccountHealth), healthRank(y.AccountHealth); rx != ry {
		return rx > ry
	}
	if lx, ly := neutralOr(x.LatencyScore), neutralOr(y.LatencyScore); lx != ly {
		return lx > ly
	}
	if hx, hy := accountWeight(x), accountWeight(y); hx != hy {
		return hx > hy
	}
	return false
}

// healthRank orders health states for the P2C tie-break: healthy is best. Every
// candidate that reaches Step 7 is already healthy (BuildCandidatePool gates on
// it), so this only ever discriminates in defensive/edge inputs.
func healthRank(h accountsdomain.HealthState) int {
	switch h {
	case accountsdomain.HealthHealthy:
		return 4
	case accountsdomain.HealthDegraded:
		return 3
	case accountsdomain.HealthUnknown:
		return 2
	case accountsdomain.HealthUnavailable:
		return 1
	default:
		return 0
	}
}
