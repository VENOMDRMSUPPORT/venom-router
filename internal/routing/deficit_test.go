package routing

import (
	"testing"

	accountsdomain "github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
)

// deficitScore builds a minimal in-band GroupScore carrying only the fields
// PreferByDeficit reads: the group's Funding class and its scores.
func deficitScore(funding accountsdomain.Funding, quality float64) GroupScore {
	return GroupScore{
		Group: RouteGroup{
			ProviderID:      "prov",
			ProviderModelID: "model-" + string(funding),
			Funding:         funding,
		},
		QualityFactor: quality,
		Composite:     quality,
	}
}

// TestPreferByDeficit_WorkloadIsolation proves the deficit state is per
// (workload_profile_bucket) — driving one bucket hard toward all-paid must
// leave a second bucket's counters untouched.
//
// Mutation row D-M1: key the state by a shared constant instead of bucketKey
// → bucket "B"'s cell moves when only bucket "A" is driven → this test RED.
func TestPreferByDeficit_WorkloadIsolation(t *testing.T) {
	const bucketA = "standard"
	const bucketB = "vision"
	const target = 0.25

	state := DeficitState{}
	// Drive bucket A for many rounds with both classes always available.
	for i := 0; i < 500; i++ {
		var chosen GroupScore
		chosen, state = PreferByDeficit(
			[]GroupScore{
				deficitScore(accountsdomain.FundingPaid, 0.9),
				deficitScore(accountsdomain.FundingFree, 0.9),
			},
			bucketA, state, target,
		)
		_ = chosen
	}

	if cell, ok := state[bucketB]; ok && (cell.FreeCount != 0 || cell.PaidCount != 0) {
		t.Fatalf("workload isolation: bucket %q leaked counts while only %q was driven: %+v", bucketB, bucketA, cell)
	}
	// Bucket A must actually have accumulated history (guards against a vacuous pass).
	if a := state[bucketA]; a.FreeCount+a.PaidCount != 500 {
		t.Fatalf("workload isolation: bucket %q total = %d, want 500", bucketA, a.FreeCount+a.PaidCount)
	}
}

// TestPreferByDeficit_ConvergesToTarget proves that, with both a paid and a
// free candidate available every round, the realized paid share converges to
// the target — deficit-based, deterministic, no randomness.
//
// Mutation row D-M2: invert the deficit comparison (prefer the SMALLER
// deficit) → paid share drifts to ~75% instead of ~25% → this test RED.
func TestPreferByDeficit_ConvergesToTarget(t *testing.T) {
	const bucket = "standard"
	const target = 0.25
	const rounds = 2000
	const tolerance = 0.05 // ±5 percentage points (05 §8.1 convergence gate)

	state := DeficitState{}
	paidPicks := 0
	for i := 0; i < rounds; i++ {
		var chosen GroupScore
		chosen, state = PreferByDeficit(
			[]GroupScore{
				deficitScore(accountsdomain.FundingPaid, 0.9),
				deficitScore(accountsdomain.FundingFree, 0.9),
			},
			bucket, state, target,
		)
		if chosen.Group.Funding == accountsdomain.FundingPaid {
			paidPicks++
		}
	}

	share := float64(paidPicks) / float64(rounds)
	if share < target-tolerance || share > target+tolerance {
		t.Fatalf("convergence: realized paid share %.4f not within %.2f of target %.2f", share, tolerance, target)
	}
	// Non-vacuous: the share must be meaningfully below an even 50/50 split.
	if share > 0.40 {
		t.Fatalf("convergence: paid share %.4f is not distinguishably below 50/50 — test may be vacuous", share)
	}
}

// TestPreferByDeficit_NeverPromotesOutsideBand proves the controller can only
// ever return one of its own inputs: even when the deficit "wants" the paid
// pool, if no paid group is in-band the single in-band free group is returned.
//
// Mutation row D-M3: remove the in-band fallback (`if len(pool)==0 { pool =
// other }`) so a preferred-but-absent class yields an empty, non-input result
// → chosen no longer equals the present free candidate → this test RED.
func TestPreferByDeficit_NeverPromotesOutsideBand(t *testing.T) {
	const bucket = "standard"
	const target = 0.25

	// Empty state ⇒ deficit favors paid. But only a free group is in-band.
	only := deficitScore(accountsdomain.FundingFree, 0.7)
	chosen, updated := PreferByDeficit([]GroupScore{only}, bucket, DeficitState{}, target)

	if chosen.Group.Funding != accountsdomain.FundingFree {
		t.Fatalf("never-promote: deficit wanted paid but chosen funding = %q, want free", chosen.Group.Funding)
	}
	if chosen.Group.ProviderModelID != only.Group.ProviderModelID {
		t.Fatalf("never-promote: chosen is not the sole in-band input (%q != %q)", chosen.Group.ProviderModelID, only.Group.ProviderModelID)
	}
	// The recorded pick must be the free one, not a fabricated paid one.
	if cell := updated[bucket]; cell.FreeCount != 1 || cell.PaidCount != 0 {
		t.Fatalf("never-promote: recorded cell %+v, want FreeCount=1 PaidCount=0", cell)
	}
}

// TestPreferByDeficit_Deterministic proves the function is pure: identical
// inputs yield identical output on repeated calls (no clock, no rand, no
// global mutable state).
func TestPreferByDeficit_Deterministic(t *testing.T) {
	const bucket = "tool_use"
	const target = 0.25

	state := DeficitState{bucket: DeficitCell{FreeCount: 3, PaidCount: 1}}
	inBand := []GroupScore{
		deficitScore(accountsdomain.FundingPaid, 0.8),
		deficitScore(accountsdomain.FundingFree, 0.9),
	}

	c1, s1 := PreferByDeficit(inBand, bucket, state, target)
	c2, s2 := PreferByDeficit(inBand, bucket, state, target)

	if c1.Group.Funding != c2.Group.Funding || c1.Group.ProviderModelID != c2.Group.ProviderModelID {
		t.Fatalf("determinism: two identical calls chose differently: %q vs %q", c1.Group.Funding, c2.Group.Funding)
	}
	if s1[bucket] != s2[bucket] {
		t.Fatalf("determinism: updated state differs: %+v vs %+v", s1[bucket], s2[bucket])
	}
}

// TestPreferByDeficit_DoesNotMutateInput proves purity: the caller's state map
// is never mutated in place — the update is returned as a fresh map so the
// caller alone decides when to commit it.
func TestPreferByDeficit_DoesNotMutateInput(t *testing.T) {
	const bucket = "standard"
	const target = 0.25

	state := DeficitState{bucket: DeficitCell{FreeCount: 2, PaidCount: 2}}
	before := state[bucket]

	_, updated := PreferByDeficit(
		[]GroupScore{
			deficitScore(accountsdomain.FundingPaid, 0.9),
			deficitScore(accountsdomain.FundingFree, 0.9),
		},
		bucket, state, target,
	)

	if state[bucket] != before {
		t.Fatalf("purity: input state mutated in place: %+v != %+v", state[bucket], before)
	}
	if updated[bucket] == before {
		t.Fatalf("purity: returned state was not updated (still %+v)", before)
	}
}

// TestPreferByDeficit_EmptyInput proves an empty in-band slice returns a zero
// GroupScore without panicking, and records nothing.
func TestPreferByDeficit_EmptyInput(t *testing.T) {
	chosen, updated := PreferByDeficit(nil, "standard", DeficitState{}, 0.25)
	if chosen.Group.ProviderModelID != "" || chosen.Group.Funding != "" {
		t.Fatalf("empty input: expected zero GroupScore, got %+v", chosen.Group)
	}
	if len(updated) != 0 {
		t.Fatalf("empty input: expected no recorded cells, got %+v", updated)
	}
}
