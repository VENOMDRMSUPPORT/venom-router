package routing

import (
	accountsdomain "github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
)

// DeficitCell is the recent successful-distribution tally for one
// (workload_profile_bucket, ·) cell (05 §2 Step 7, §8.1). The two funding
// classes are tracked as separate counters within the one cell rather than as
// two map entries, so a single cell fully describes a bucket's realized mix.
type DeficitCell struct {
	FreeCount int
	PaidCount int
}

// DeficitState maps a workload_profile_bucket key (the canonical
// BucketKeyer.BucketKey serialization) to its DeficitCell. It is deliberately
// NOT global and NOT keyed by tier: keeping one independent cell per bucket is
// exactly what makes a burst of one workload never distort another's mix
// accounting (05 §8.1). The key carries no tier component so the same type can
// back Max's observability later without a hardcoded "pro".
//
// This is a pure in-memory value. Persisting it to the deficit_cells table is
// P5's job; this package never touches storage.
type DeficitState map[string]DeficitCell

// clone returns a shallow copy of the state. DeficitCell is a value type with
// no reference fields, so a shallow copy is a full deep copy — mutating the
// clone can never reach back into the caller's map.
func (s DeficitState) clone() DeficitState {
	out := make(DeficitState, len(s)+1)
	for k, v := range s {
		out[k] = v
	}
	return out
}

// PreferByDeficit is Pro's Step-7 funding-mix deficit controller, workload
// isolated (05 §2 Step 7, §8.1). It chooses one group from the ALREADY
// competitive-band-filtered set inBand, steering the realized paid share of
// this bucket toward targetPaidShare (~0.25 for Pro) WITHOUT ever promoting a
// route that is not already in the band.
//
// It is a pure function: identical (inBand, bucketKey, state, targetPaidShare)
// always yields identical output, with no clock, no randomness, and no global
// mutable state. The returned DeficitState is a fresh map reflecting the chosen
// group's funding class; the CALLER decides whether/when to commit it (e.g.
// only on a genuinely successful response — P4-ROUTE-013's reconcile step).
// The input state is never mutated in place.
//
// Algorithm:
//   - Compute the current paid share for this bucket from state[bucketKey].
//     The paid pool has a non-negative deficit exactly when actualPaidShare <=
//     targetPaidShare; otherwise the free pool does. (Because the two funding
//     shares and the two targets each sum to 1, the paid deficit is the exact
//     negation of the free deficit, so a single comparison decides which pool
//     is behind. See the §8.1 ambiguity note in the unit report for the
//     documented boundary tie-break.)
//   - Prefer the behind pool, but ONLY among groups of that class that are
//     actually present in inBand. If the preferred class is absent from the
//     band, fall back to the other (present) class — never fabricate a route.
//   - Within the selected pool, pick the strongest route deterministically by
//     Composite score, breaking ties by input order (earliest wins).
func PreferByDeficit(inBand []GroupScore, bucketKey string, state DeficitState, targetPaidShare float64) (GroupScore, DeficitState) {
	updated := state.clone()
	if len(inBand) == 0 {
		return GroupScore{}, updated
	}

	var paid, free []GroupScore
	for _, s := range inBand {
		if s.Group.Funding == accountsdomain.FundingPaid {
			paid = append(paid, s)
		} else {
			// Free (and any non-paid funding). Unknown funding never reaches
			// Step 7: ApplyHardGates rejects it at Step 3, so in practice this
			// branch is the free pool.
			free = append(free, s)
		}
	}

	preferred, other := free, paid
	if preferPaidByDeficit(updated[bucketKey], targetPaidShare) {
		preferred, other = paid, free
	}
	pool := preferred
	if len(pool) == 0 {
		pool = other
	}

	chosen := bestByComposite(pool)

	cell := updated[bucketKey]
	if chosen.Group.Funding == accountsdomain.FundingPaid {
		cell.PaidCount++
	} else {
		cell.FreeCount++
	}
	updated[bucketKey] = cell

	return chosen, updated
}

// preferPaidByDeficit reports whether the paid pool currently carries the
// larger (non-negative) deficit for this cell, i.e. whether the realized paid
// share is at or below the target. With no history (an empty cell), the paid
// pool is preferred exactly when the target admits any paid traffic at all
// (targetPaidShare > 0), letting the controller start converging immediately.
//
// The boundary (actualPaidShare == targetPaidShare) resolves to paid
// deterministically. This is a fixed, documented tie-break — not randomness —
// and its long-run effect on the realized share is O(1/N), well inside the
// §8.1 tolerance.
func preferPaidByDeficit(cell DeficitCell, targetPaidShare float64) bool {
	total := cell.FreeCount + cell.PaidCount
	if total == 0 {
		return targetPaidShare > 0
	}
	actualPaidShare := float64(cell.PaidCount) / float64(total)
	return actualPaidShare <= targetPaidShare
}

// bestByComposite returns the group with the highest Composite score, breaking
// ties by input order (the earliest entry wins) for determinism. An empty pool
// yields the zero GroupScore — a defensive path the exported function never
// reaches, since it only ever passes a non-empty pool.
func bestByComposite(pool []GroupScore) GroupScore {
	if len(pool) == 0 {
		return GroupScore{}
	}
	best := pool[0]
	for _, s := range pool[1:] {
		if s.Composite > best.Composite {
			best = s
		}
	}
	return best
}
