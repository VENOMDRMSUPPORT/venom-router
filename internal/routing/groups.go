package routing

import (
	"sort"

	accountsdomain "github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
)

// RouteGroup is the Step-4 anti-inflation aggregate (05 §2 Step 4): N
// accounts of one offering form one group scored once, preventing an offering
// with many enrolled accounts from inflating its presence in the ranked set.
type RouteGroup struct {
	ProviderID      string
	ProviderModelID string
	Funding         accountsdomain.Funding

	// Members is the full list of CandidateOfferings that share the composite
	// (ProviderID, ProviderModelID, Funding) key.
	Members []CandidateOffering

	// BestQuotaHeadroom is the maximum QuotaHeadroom across Members where
	// non-nil, nil if every member's QuotaHeadroom is nil. ScoreGroups
	// uses this group-level value instead of any individual member's field
	// (05 §2 Step 5: "use the group's headroom, not a member's").
	BestQuotaHeadroom *float64
}

// groupKey returns the composite grouping key for a CandidateOffering.
// The null byte separator ensures keys with different field boundaries cannot
// collide (e.g. "p\x00m\x00f" vs "p\x00m" + "\x00" + "f").
func groupKey(c CandidateOffering) string {
	return c.ProviderID + "\x00" + c.ProviderModelID + "\x00" + string(c.Funding)
}

// BuildRouteGroups builds the Step-4 route group set (05 §2 Step 4) from the
// eligible pool produced by ApplyHardGates. Each distinct
// (ProviderID, ProviderModelID, Funding) key produces exactly one RouteGroup.
// Output order is deterministic: groups are sorted by their composite key
// string ascending so callers and tests never depend on map-iteration order.
// The input slice is never mutated.
func BuildRouteGroups(eligible []CandidateOffering) []RouteGroup {
	type entry struct {
		key string
		idx int
	}
	seen := make(map[string]int) // key → index in groups
	var groups []RouteGroup
	var order []entry

	for _, c := range eligible {
		key := groupKey(c)
		idx, ok := seen[key]
		if !ok {
			idx = len(groups)
			seen[key] = idx
			order = append(order, entry{key: key, idx: idx})
			groups = append(groups, RouteGroup{
				ProviderID:      c.ProviderID,
				ProviderModelID: c.ProviderModelID,
				Funding:         c.Funding,
			})
		}
		groups[idx].Members = append(groups[idx].Members, c)
		if c.QuotaHeadroom != nil {
			best := groups[idx].BestQuotaHeadroom
			if best == nil || *c.QuotaHeadroom > *best {
				v := *c.QuotaHeadroom
				groups[idx].BestQuotaHeadroom = &v
			}
		}
	}

	sort.Slice(order, func(i, j int) bool {
		return order[i].key < order[j].key
	})

	out := make([]RouteGroup, len(order))
	for i, e := range order {
		out[i] = groups[e.idx]
	}
	return out
}
