package routing

import (
	"testing"

	accountsdomain "github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
)

// makeCandidate builds a minimal CandidateOffering for grouping tests.
func makeCandidate(providerID, modelID string, funding accountsdomain.Funding, quota *float64) CandidateOffering {
	return CandidateOffering{
		ProviderID:      providerID,
		ProviderModelID: modelID,
		Funding:         funding,
		QuotaHeadroom:   quota,
	}
}

func f64p(v float64) *float64 { return &v }

// TestBuildRouteGroups_AntiInflation verifies that N accounts sharing one
// (provider, model, funding) key produce exactly ONE group with N members.
//
// Mutation row G-M1: group by (provider, model) only, dropping funding from
// the key → the two-funding-produces-two-groups test merges them into one
// → restore.
func TestBuildRouteGroups_AntiInflation(t *testing.T) {
	const N = 5
	var eligible []CandidateOffering
	for i := 0; i < N; i++ {
		eligible = append(eligible, makeCandidate("prov1", "model1", accountsdomain.FundingFree, nil))
	}

	groups := BuildRouteGroups(eligible)
	if len(groups) != 1 {
		t.Fatalf("anti-inflation: got %d groups, want 1", len(groups))
	}
	if len(groups[0].Members) != N {
		t.Fatalf("anti-inflation: group has %d members, want %d", len(groups[0].Members), N)
	}
}

// TestBuildRouteGroups_FundingIsPartOfKey verifies that the same
// (provider, model) but different funding values produce two distinct groups.
func TestBuildRouteGroups_FundingIsPartOfKey(t *testing.T) {
	eligible := []CandidateOffering{
		makeCandidate("prov1", "model1", accountsdomain.FundingFree, nil),
		makeCandidate("prov1", "model1", accountsdomain.FundingPaid, nil),
	}

	groups := BuildRouteGroups(eligible)
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want 2 (one per distinct funding)", len(groups))
	}
}

// TestBuildRouteGroups_BestQuotaHeadroom verifies max semantics:
//   - {0.3, nil, 0.7} → BestQuotaHeadroom = 0.7
//   - all-nil → BestQuotaHeadroom = nil
//
// Mutation row G-M2: use min instead of max → BestQuotaHeadroom = 0.3
// instead of 0.7 → test goes RED → restore.
func TestBuildRouteGroups_BestQuotaHeadroom(t *testing.T) {
	t.Run("max of non-nil values", func(t *testing.T) {
		eligible := []CandidateOffering{
			makeCandidate("p1", "m1", accountsdomain.FundingFree, f64p(0.3)),
			makeCandidate("p1", "m1", accountsdomain.FundingFree, nil),
			makeCandidate("p1", "m1", accountsdomain.FundingFree, f64p(0.7)),
		}
		groups := BuildRouteGroups(eligible)
		if len(groups) != 1 {
			t.Fatalf("got %d groups, want 1", len(groups))
		}
		best := groups[0].BestQuotaHeadroom
		if best == nil {
			t.Fatal("BestQuotaHeadroom = nil, want 0.7")
		}
		if *best != 0.7 {
			t.Fatalf("BestQuotaHeadroom = %v, want 0.7", *best)
		}
	})

	t.Run("all-nil members → nil BestQuotaHeadroom", func(t *testing.T) {
		eligible := []CandidateOffering{
			makeCandidate("p1", "m1", accountsdomain.FundingFree, nil),
			makeCandidate("p1", "m1", accountsdomain.FundingFree, nil),
		}
		groups := BuildRouteGroups(eligible)
		if len(groups) != 1 {
			t.Fatalf("got %d groups, want 1", len(groups))
		}
		if groups[0].BestQuotaHeadroom != nil {
			t.Fatalf("BestQuotaHeadroom = %v, want nil", groups[0].BestQuotaHeadroom)
		}
	})
}

// TestBuildRouteGroups_Determinism verifies the output order is stable
// regardless of input ordering.
func TestBuildRouteGroups_Determinism(t *testing.T) {
	// Three distinct keys in non-sorted input order.
	eligibleA := []CandidateOffering{
		makeCandidate("pZ", "m1", accountsdomain.FundingFree, nil),
		makeCandidate("pA", "m1", accountsdomain.FundingFree, nil),
		makeCandidate("pM", "m1", accountsdomain.FundingFree, nil),
	}
	// Same keys, different input order.
	eligibleB := []CandidateOffering{
		makeCandidate("pA", "m1", accountsdomain.FundingFree, nil),
		makeCandidate("pM", "m1", accountsdomain.FundingFree, nil),
		makeCandidate("pZ", "m1", accountsdomain.FundingFree, nil),
	}

	groupsA := BuildRouteGroups(eligibleA)
	groupsB := BuildRouteGroups(eligibleB)

	if len(groupsA) != len(groupsB) {
		t.Fatalf("non-deterministic group count: %d vs %d", len(groupsA), len(groupsB))
	}
	for i := range groupsA {
		if groupsA[i].ProviderID != groupsB[i].ProviderID {
			t.Fatalf("non-deterministic order at index %d: %q vs %q", i, groupsA[i].ProviderID, groupsB[i].ProviderID)
		}
	}
}

// TestBuildRouteGroups_InputImmutability verifies the input slice is not
// mutated.
func TestBuildRouteGroups_InputImmutability(t *testing.T) {
	eligible := []CandidateOffering{
		makeCandidate("p1", "m1", accountsdomain.FundingFree, f64p(0.5)),
		makeCandidate("p2", "m2", accountsdomain.FundingPaid, f64p(0.8)),
	}
	snapshot := make([]CandidateOffering, len(eligible))
	copy(snapshot, eligible)

	_ = BuildRouteGroups(eligible)

	for i := range eligible {
		if eligible[i].ProviderID != snapshot[i].ProviderID ||
			eligible[i].Funding != snapshot[i].Funding {
			t.Fatalf("input slice was mutated at index %d", i)
		}
	}
}
