package routing

import (
	"time"

	accountsdomain "github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
)

// stickyHeadroomThreshold is the minimum per-window headroom fraction a pinned
// account must retain to keep its pin (05 §2 Step 7: "dropped on ... quota
// headroom ≤ 15% on any applicable window"). At or below this fraction on ANY
// window, the pin is dropped and the request falls through to normal selection.
const stickyHeadroomThreshold = 0.15

// SelectFairAccount is the capacity-fairness account selection for the Lite and
// Pro tiers (05 §2 Step 7, "Account selection (within the group)"). Max instead
// uses SelectMaxAccount (DRR + P2C); this function must not be used for Max.
//
// It first excludes accounts saturated on any applicable window via
// SaturationFilter (fail-closed on unknown/nil window data; fail-open only when
// every account in the group is saturated), then returns the single
// best-ranked eligible account. ok is false only for an empty group.
//
// Ranking is a deterministic tie-break ladder over the spec's capacity-fairness
// signals, in priority order:
//  1. available capacity — the binding window's headroom (accountWeight, the
//     same min-across-windows measure DRR uses), higher is better;
//  2. inverse recent load — fewer InFlightCount is better;
//  3. reliability — higher Reliability (neutral 0.5 when nil) is better;
//  4. latency — higher LatencyScore (neutral 0.5 when nil) is better, matching
//     p2cBetter's convention in drr.go.
//
// Ties fall through to input order (a candidate replaces the incumbent only when
// STRICTLY better), so the result is fully deterministic.
func SelectFairAccount(members []CandidateOffering, need float64, now time.Time, staleAfter time.Duration) (CandidateOffering, bool) {
	if len(members) == 0 {
		return CandidateOffering{}, false
	}
	eligible, _ := SaturationFilter(members, need, now, staleAfter)

	best := eligible[0]
	for _, c := range eligible[1:] {
		if fairBetter(c, best) {
			best = c
		}
	}
	return best, true
}

// fairBetter reports whether x is strictly better than y on the capacity-
// fairness ladder described on SelectFairAccount. A full tie returns false.
func fairBetter(x, y CandidateOffering) bool {
	if hx, hy := accountWeight(x), accountWeight(y); hx != hy {
		return hx > hy
	}
	if x.InFlightCount != y.InFlightCount {
		return x.InFlightCount < y.InFlightCount
	}
	if rx, ry := neutralOr(x.Reliability), neutralOr(y.Reliability); rx != ry {
		return rx > ry
	}
	if lx, ly := neutralOr(x.LatencyScore), neutralOr(y.LatencyScore); lx != ly {
		return lx > ly
	}
	return false
}

// SelectAccount is the Step-7 account-selection orchestrator (05 §2 Step 7). It
// layers session stickiness IN FRONT OF the tier-appropriate distribution:
//
//  1. If a valid pin exists for stickinessKey and its account is present in the
//     group AND genuinely eligible right now — not saturated (SaturationFilter's
//     eligibility, the authoritative check), above the 15% per-window headroom
//     threshold, healthy, and not cooling — that account is PREFERRED and
//     returned directly, bypassing DRR / fairness. drrState is returned
//     unchanged (stickiness runs instead of the distribution logic).
//  2. Otherwise it dispatches on tier: Max → SelectMaxAccount (DRR + P2C);
//     Lite/Pro → SelectFairAccount.
//
// Stickiness is fail-open: any missing/expired/ineligible pin simply falls
// through to normal selection — it never blocks a request. This function never
// calls cache.Pin; pinning happens only on a genuinely successful response,
// which this unit cannot observe (P4-ROUTE-013's job).
func SelectAccount(tier Tier, group RouteGroup, stickinessKey string, cache *StickinessCache, drrState DRRState, need float64, now time.Time, staleAfter time.Duration) (CandidateOffering, DRRState, bool) {
	if cache != nil && stickinessKey != "" {
		if accountID, ok := cache.Lookup(stickinessKey, now); ok {
			if member, found := findMember(group.Members, accountID); found {
				if stickyEligible(member, need, now, staleAfter) {
					return member, drrState, true
				}
			}
		}
	}

	if tier == TierMax {
		return SelectMaxAccount(group.Members, drrState, need, now, staleAfter)
	}
	chosen, ok := SelectFairAccount(group.Members, need, now, staleAfter)
	return chosen, drrState, ok
}

// findMember returns the member with the given AccountID, if present.
func findMember(members []CandidateOffering, accountID string) (CandidateOffering, bool) {
	for _, m := range members {
		if m.AccountID == accountID {
			return m, true
		}
	}
	return CandidateOffering{}, false
}

// stickyEligible reports whether a pinned account may still be preferred
// (05 §2 Step 7 drop conditions). A pin is kept ONLY when all hold: the account
// is healthy, not cooling, not saturated on any applicable window (the
// authoritative eligibility check — a sticky pick may never violate eligibility,
// even when its headroom fraction is nominally fine), and its headroom is above
// the 15% threshold on every applicable window. Any failure drops the pin.
func stickyEligible(c CandidateOffering, need float64, now time.Time, staleAfter time.Duration) bool {
	if c.AccountHealth != accountsdomain.HealthHealthy {
		return false
	}
	if c.Cooling {
		return false
	}
	if isSaturated(c, need, now, staleAfter) {
		return false
	}
	return headroomAboveThreshold(c, stickyHeadroomThreshold)
}

// headroomAboveThreshold reports whether every applicable window's headroom is
// STRICTLY above `frac` of its capacity. It fails closed: an account with no
// windows, or any window whose capacity or headroom is unknown or non-positive,
// cannot prove it is above the threshold and returns false.
func headroomAboveThreshold(c CandidateOffering, frac float64) bool {
	if len(c.QuotaWindows) == 0 {
		return false
	}
	for _, w := range c.QuotaWindows {
		capacity, ok := w.Capacity()
		if !ok || capacity <= 0 {
			return false
		}
		headroom, ok := w.Headroom()
		if !ok {
			return false
		}
		if headroom <= frac*capacity {
			return false
		}
	}
	return true
}
