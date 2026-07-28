package routing

import "github.com/VENOMDRMSUPPORT/venom-router/internal/models"

// GroupScore carries a RouteGroup and its Step-5 scoring results (05 §2
// Step 5). QualityFactor is kept separate from Composite because Step 6's
// competitive band operates on quality alone, never the weighted composite.
type GroupScore struct {
	Group         RouteGroup
	QualityFactor float64 // the quality dimension only (0–1); used by ApplyCompetitiveBand
	Composite     float64 // weighted sum of all six factors; 0 for Lite (unscored)
}

// neutralOr returns 0.5 when v is nil (the "missing factor = neutral" rule
// from 05 §2 Step 5) and *v otherwise.
func neutralOr(v *float64) float64 {
	if v == nil {
		return 0.5
	}
	return *v
}

// ScoreGroups computes Step-5 scores for each route group (05 §2 Step 5).
// Output order matches the input groups order exactly; no re-sorting is done
// here — ApplyCompetitiveBand and later distribution steps do their own ordering.
//
// Lite (policy.Scored == false): every GroupScore carries QualityFactor=0 and
// Composite=0 regardless of factor values on the group's members. The spec
// is explicit: Lite is pure hard-eligibility with latency as the only
// tie-break; fabricating a quality-derived score for Lite groups is a defect.
//
// Pro/Max: the representative candidate is the group's first Member (grouping
// already collapsed N accounts of one offering; any member's QualityRating,
// Reliability, EvidenceConfidence, CostClass, and LatencyScore are offering
// facts, not account facts). QuotaHeadroom uses the group's own
// BestQuotaHeadroom field (the whole point of Step 4's aggregation).
func ScoreGroups(groups []RouteGroup, policy TierPolicy) []GroupScore {
	out := make([]GroupScore, len(groups))

	if !policy.Scored {
		for i, g := range groups {
			out[i] = GroupScore{Group: g, QualityFactor: 0, Composite: 0}
		}
		return out
	}

	for i, g := range groups {
		if len(g.Members) == 0 {
			out[i] = GroupScore{Group: g}
			continue
		}
		rep := g.Members[0]
		w := policy.Weights

		quality := models.QualityScore(rep.QualityRating)
		reliability := neutralOr(rep.Reliability)
		quotaHeadroom := neutralOr(g.BestQuotaHeadroom)
		evidenceConfidence := neutralOr(rep.EvidenceConfidence)
		costClass := neutralOr(rep.CostClass)
		latency := neutralOr(rep.LatencyScore)

		composite := quality*w.Quality +
			reliability*w.Reliability +
			quotaHeadroom*w.QuotaHeadroom +
			evidenceConfidence*w.EvidenceConfidence +
			costClass*w.CostClass +
			latency*w.Latency

		out[i] = GroupScore{
			Group:         g,
			QualityFactor: quality,
			Composite:     composite,
		}
	}

	return out
}
