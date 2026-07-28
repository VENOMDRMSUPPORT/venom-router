package routing

import (
	"math"
	"testing"
)

const scoreTolerance = 1e-9

func approxEq(a, b float64) bool {
	return math.Abs(a-b) <= scoreTolerance
}

// proPolicy and maxPolicy retrieve the real tier policies for golden-value
// tests so that a future policy change cannot silently desync the test.
func proPolicy(t *testing.T) TierPolicy {
	t.Helper()
	ps, err := Policies()
	if err != nil {
		t.Fatalf("Policies: %v", err)
	}
	return ps[TierPro]
}

func maxPolicy(t *testing.T) TierPolicy {
	t.Helper()
	ps, err := Policies()
	if err != nil {
		t.Fatalf("Policies: %v", err)
	}
	return ps[TierMax]
}

// oneGroupFrom builds a single RouteGroup from one representative candidate
// with the supplied BestQuotaHeadroom.
func oneGroupFrom(rep CandidateOffering, bestQuota *float64) RouteGroup {
	return RouteGroup{
		ProviderID:        rep.ProviderID,
		ProviderModelID:   rep.ProviderModelID,
		Funding:           rep.Funding,
		Members:           []CandidateOffering{rep},
		BestQuotaHeadroom: bestQuota,
	}
}

// TestScoreGroups_Lite verifies that Lite produces QualityFactor=0 and
// Composite=0 for EVERY group regardless of factor values on members — the
// values are deliberately set non-zero to prove they are ignored, not
// coincidentally zero.
//
// Mutation row S-M1: make Lite fall through to the weighted-sum branch →
// QualityFactor != 0 for a group with a non-nil QualityRating → restore.
func TestScoreGroups_Lite(t *testing.T) {
	ps, err := Policies()
	if err != nil {
		t.Fatalf("Policies: %v", err)
	}
	policy := ps[TierLite]

	q := 80.0
	rel := 0.70
	quota := 0.60
	ev := 0.90
	cost := 0.40
	lat := 0.50
	rep := CandidateOffering{
		QualityRating:      &q,
		Reliability:        &rel,
		QuotaHeadroom:      &quota,
		EvidenceConfidence: &ev,
		CostClass:          &cost,
		LatencyScore:       &lat,
	}
	groups := []RouteGroup{oneGroupFrom(rep, &quota)}
	scores := ScoreGroups(groups, policy)

	if len(scores) != 1 {
		t.Fatalf("got %d scores, want 1", len(scores))
	}
	if scores[0].QualityFactor != 0 {
		t.Fatalf("Lite QualityFactor = %v, want 0 (must ignore factor values)", scores[0].QualityFactor)
	}
	if scores[0].Composite != 0 {
		t.Fatalf("Lite Composite = %v, want 0 (must ignore factor values)", scores[0].Composite)
	}
}

// TestScoreGroups_ProWeightedSum_Golden verifies the exact weighted-sum
// composite for Pro against a hand-computed golden value.
//
// Input:  QualityRating=80, Reliability=0.70, BestQuotaHeadroom=0.60,
//
//	EvidenceConfidence=0.90, CostClass=0.40, LatencyScore=0.50.
//
// Pro weights: Q=0.40, R=0.25, QH=0.15, EC=0, CC=0.15, L=0.05.
// Expected composite = 0.80×0.40 + 0.70×0.25 + 0.60×0.15 + 0.90×0
//
//   - 0.40×0.15 + 0.50×0.05 = 0.670.
func TestScoreGroups_ProWeightedSum_Golden(t *testing.T) {
	policy := proPolicy(t)

	q := 80.0
	rel := 0.70
	quota := 0.60
	ev := 0.90
	cost := 0.40
	lat := 0.50
	rep := CandidateOffering{
		QualityRating:      &q,
		Reliability:        &rel,
		QuotaHeadroom:      &quota,
		EvidenceConfidence: &ev,
		CostClass:          &cost,
		LatencyScore:       &lat,
	}
	bestQuota := 0.60
	groups := []RouteGroup{oneGroupFrom(rep, &bestQuota)}
	scores := ScoreGroups(groups, policy)

	if len(scores) != 1 {
		t.Fatalf("got %d scores, want 1", len(scores))
	}
	wantQuality := 0.80
	wantComposite := 0.670
	if !approxEq(scores[0].QualityFactor, wantQuality) {
		t.Fatalf("QualityFactor = %v, want %v", scores[0].QualityFactor, wantQuality)
	}
	if !approxEq(scores[0].Composite, wantComposite) {
		t.Fatalf("Composite = %v, want %v", scores[0].Composite, wantComposite)
	}
}

// TestScoreGroups_MissingFactorNeutrality verifies that a nil Reliability
// (and only Reliability) is treated as 0.5, not 0.
//
// With Reliability=nil (→0.5), same other inputs as the golden test:
// Composite = 0.80×0.40 + 0.50×0.25 + 0.60×0.15 + 0.90×0
//
//   - 0.40×0.15 + 0.50×0.05 = 0.620.
//
// Mutation row S-M2: substitute 0 for nil → Composite = 0.595 ≠ 0.620
// → restore.
func TestScoreGroups_MissingFactorNeutrality(t *testing.T) {
	policy := proPolicy(t)

	q := 80.0
	quota := 0.60
	ev := 0.90
	cost := 0.40
	lat := 0.50
	rep := CandidateOffering{
		QualityRating:      &q,
		Reliability:        nil, // explicitly absent
		QuotaHeadroom:      &quota,
		EvidenceConfidence: &ev,
		CostClass:          &cost,
		LatencyScore:       &lat,
	}
	bestQuota := 0.60
	groups := []RouteGroup{oneGroupFrom(rep, &bestQuota)}
	scores := ScoreGroups(groups, policy)

	if len(scores) != 1 {
		t.Fatalf("got %d scores, want 1", len(scores))
	}
	// Reliability neutral=0.5 contribution: 0.5×0.25=0.125 (not 0×0.25=0).
	wantComposite := 0.620
	if !approxEq(scores[0].Composite, wantComposite) {
		t.Fatalf("Composite = %v, want %v (nil Reliability must use neutral 0.5)", scores[0].Composite, wantComposite)
	}
}

// TestScoreGroups_QuotaHeadroomUsesGroupBest verifies that ScoreGroups reads
// BestQuotaHeadroom from the RouteGroup, not from the member's own
// QuotaHeadroom field.
//
// Setup: member has QuotaHeadroom=0.30, group has BestQuotaHeadroom=0.80.
// With BestQuotaHeadroom=0.80: Composite = 0.700 (see below).
// With member's raw=0.30: Composite = 0.625 — the difference catches the bug.
//
// Pro: Q=0.80×0.40=0.32, R=0.70×0.25=0.175, QH=0.80×0.15=0.12,
//
//	EC=0.90×0=0, CC=0.40×0.15=0.06, L=0.50×0.05=0.025 → 0.700.
//
// Mutation row S-M3: read member's QuotaHeadroom instead of group's
// BestQuotaHeadroom → Composite = 0.625 ≠ 0.700 → restore.
func TestScoreGroups_QuotaHeadroomUsesGroupBest(t *testing.T) {
	policy := proPolicy(t)

	memberQuota := 0.30
	q := 80.0
	rel := 0.70
	ev := 0.90
	cost := 0.40
	lat := 0.50
	rep := CandidateOffering{
		QualityRating:      &q,
		Reliability:        &rel,
		QuotaHeadroom:      &memberQuota, // intentionally low — should NOT be used
		EvidenceConfidence: &ev,
		CostClass:          &cost,
		LatencyScore:       &lat,
	}
	bestQuota := 0.80 // group-level best — the one that should be used
	g := RouteGroup{
		Members:           []CandidateOffering{rep},
		BestQuotaHeadroom: &bestQuota,
	}
	scores := ScoreGroups([]RouteGroup{g}, policy)

	if len(scores) != 1 {
		t.Fatalf("got %d scores, want 1", len(scores))
	}
	wantComposite := 0.700
	if !approxEq(scores[0].Composite, wantComposite) {
		t.Fatalf("Composite = %v, want %v (must use group BestQuotaHeadroom=0.80, not member's=0.30)",
			scores[0].Composite, wantComposite)
	}
}

// TestScoreGroups_OutputOrderMatchesInput verifies the output slice indices
// correspond to the input groups in the same order.
func TestScoreGroups_OutputOrderMatchesInput(t *testing.T) {
	policy := proPolicy(t)

	g1 := RouteGroup{ProviderID: "a", Members: []CandidateOffering{{}}}
	g2 := RouteGroup{ProviderID: "b", Members: []CandidateOffering{{}}}
	g3 := RouteGroup{ProviderID: "c", Members: []CandidateOffering{{}}}
	scores := ScoreGroups([]RouteGroup{g1, g2, g3}, policy)

	if len(scores) != 3 {
		t.Fatalf("got %d scores, want 3", len(scores))
	}
	for i, expected := range []string{"a", "b", "c"} {
		if scores[i].Group.ProviderID != expected {
			t.Fatalf("index %d: ProviderID = %q, want %q", i, scores[i].Group.ProviderID, expected)
		}
	}
}
