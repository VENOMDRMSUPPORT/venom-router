package routing

import (
	"testing"
	"time"

	accountsdomain "github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

// drrTestNow is a fixed evaluation instant so every window's freshness is
// deterministic (no wall clock in the algorithm).
var drrTestNow = time.Unix(1_700_000_000, 0)

// availWindow builds a fresh, provider-evidence window with the given
// remaining capacity as of drrTestNow.
func availWindow(remaining float64) quota.Window {
	r := remaining
	return quota.Window{
		Remaining:  &r,
		Reserved:   0,
		Freshness:  quota.FreshnessFresh,
		ObservedAt: drrTestNow,
	}
}

// exhaustedWindow builds a fresh window with zero remaining capacity.
func exhaustedWindow() quota.Window {
	return availWindow(0)
}

// candWithWeight builds an eligible candidate whose DRR weight (min headroom
// across windows) equals headroom, with neutral P2C signals.
func candWithWeight(id string, funding accountsdomain.Funding, headroom float64) CandidateOffering {
	return CandidateOffering{
		AccountID:     id,
		ProviderID:    "prov",
		AccountHealth: accountsdomain.HealthHealthy,
		Funding:       funding,
		InFlightCount: 0,
		QuotaWindows:  []quota.Window{availWindow(headroom)},
	}
}

// --- Stage 1: saturation filter -------------------------------------------

// TestSaturationFilter_ExcludesSaturatedOnAnyWindow proves an account exhausted
// on ONE of several applicable windows is excluded even when its other windows
// are healthy — the attempt takes the most restrictive window (05 §4).
//
// Mutation row R-M1: remove the saturation filter (let saturated accounts
// through) → the excluded account reappears → this test RED.
func TestSaturationFilter_ExcludesSaturatedOnAnyWindow(t *testing.T) {
	healthy := candWithWeight("acc-healthy", accountsdomain.FundingFree, 100)
	// mixed has one healthy window and one exhausted window.
	mixed := CandidateOffering{
		AccountID:     "acc-mixed",
		AccountHealth: accountsdomain.HealthHealthy,
		Funding:       accountsdomain.FundingFree,
		QuotaWindows:  []quota.Window{availWindow(100), exhaustedWindow()},
	}

	eligible, allSaturated := SaturationFilter([]CandidateOffering{healthy, mixed}, 1, drrTestNow, quota.DefaultStalenessWindow)

	if allSaturated {
		t.Fatalf("not every account is saturated; allSaturated must be false")
	}
	if len(eligible) != 1 || eligible[0].AccountID != "acc-healthy" {
		t.Fatalf("expected only acc-healthy eligible, got %v", accountIDs(eligible))
	}
}

// TestSaturationFilter_FailOpenOnlyWhenAllSaturated proves the spec's explicit
// exception: if EVERY account is saturated the group fails OPEN (all accounts
// returned) rather than returning nothing; but with even one healthy account,
// saturated accounts are excluded.
//
// Mutation row R-M2: remove the fail-open branch (return empty when all
// saturated) → the all-saturated call returns an empty set → this test RED.
func TestSaturationFilter_FailOpenOnlyWhenAllSaturated(t *testing.T) {
	sat1 := CandidateOffering{AccountID: "s1", Funding: accountsdomain.FundingFree, QuotaWindows: []quota.Window{exhaustedWindow()}}
	sat2 := CandidateOffering{AccountID: "s2", Funding: accountsdomain.FundingPaid, QuotaWindows: []quota.Window{exhaustedWindow()}}

	eligible, allSaturated := SaturationFilter([]CandidateOffering{sat1, sat2}, 1, drrTestNow, quota.DefaultStalenessWindow)
	if !allSaturated {
		t.Fatalf("every account saturated: allSaturated must be true")
	}
	if len(eligible) != 2 {
		t.Fatalf("fail-open: expected all %d accounts returned, got %d", 2, len(eligible))
	}

	// With one healthy account added, the saturated ones must be excluded.
	healthy := candWithWeight("h", accountsdomain.FundingFree, 50)
	eligible2, allSaturated2 := SaturationFilter([]CandidateOffering{sat1, sat2, healthy}, 1, drrTestNow, quota.DefaultStalenessWindow)
	if allSaturated2 {
		t.Fatalf("one account healthy: allSaturated must be false")
	}
	if len(eligible2) != 1 || eligible2[0].AccountID != "h" {
		t.Fatalf("expected only the healthy account, got %v", accountIDs(eligible2))
	}
}

// TestSaturationFilter_NilWindowsIsSaturated proves fail-closed on unknown: an
// account with no window data (nil slice) is treated as saturated, never as
// unlimited — unless it is the fail-open all-saturated case.
func TestSaturationFilter_NilWindowsIsSaturated(t *testing.T) {
	nilData := CandidateOffering{AccountID: "nodata", Funding: accountsdomain.FundingFree, QuotaWindows: nil}
	healthy := candWithWeight("ok", accountsdomain.FundingFree, 10)

	eligible, allSaturated := SaturationFilter([]CandidateOffering{nilData, healthy}, 1, drrTestNow, quota.DefaultStalenessWindow)
	if allSaturated {
		t.Fatalf("one healthy account present: allSaturated must be false")
	}
	if len(eligible) != 1 || eligible[0].AccountID != "ok" {
		t.Fatalf("fail-closed: nil-windows account must be excluded, got %v", accountIDs(eligible))
	}
}

// --- Stage 2: quota-fair DRR ----------------------------------------------

// TestDRRRound_ConvergesToCapacityRatio proves DRR selection frequency
// approaches each account's share of total weight over many rounds — weighted,
// deterministic, not a uniform split.
//
// Mutation row R-M3: treat all accounts as equal weight (ignore capacity) →
// selection collapses toward a uniform 1/3 split → this test RED for the
// differently-weighted fixture.
func TestDRRRound_ConvergesToCapacityRatio(t *testing.T) {
	// Weights 60 : 30 : 10 → shares 0.60 / 0.30 / 0.10.
	accounts := []CandidateOffering{
		candWithWeight("big", accountsdomain.FundingFree, 60),
		candWithWeight("mid", accountsdomain.FundingFree, 30),
		candWithWeight("small", accountsdomain.FundingFree, 10),
	}
	want := map[string]float64{"big": 0.60, "mid": 0.30, "small": 0.10}
	const rounds = 3000
	const tolerance = 0.03

	counts := map[string]int{}
	state := DRRState{}
	for i := 0; i < rounds; i++ {
		var chosen CandidateOffering
		chosen, state = DRRRound(accounts, state)
		counts[chosen.AccountID]++
	}

	for id, target := range want {
		got := float64(counts[id]) / float64(rounds)
		if got < target-tolerance || got > target+tolerance {
			t.Fatalf("DRR convergence: account %q frequency %.4f not within %.2f of capacity share %.2f", id, got, tolerance, target)
		}
	}
	// Non-vacuous: "big" must be clearly ahead of a 1/3 uniform split.
	if float64(counts["big"])/float64(rounds) < 0.45 {
		t.Fatalf("DRR convergence: big-weight account not distinguishably above uniform — test may be vacuous")
	}
}

// --- Stage 3: P2C final pick ----------------------------------------------

// TestP2CPick_PrefersFewerInFlight proves the P2C tie-break: when DRR's top two
// candidates differ only in the in-flight signal, P2C picks the one with fewer
// in-flight requests regardless of DRR credit order.
func TestP2CPick_PrefersFewerInFlight(t *testing.T) {
	busy := candWithWeight("busy", accountsdomain.FundingFree, 50)
	busy.InFlightCount = 5
	idle := candWithWeight("idle", accountsdomain.FundingFree, 50)
	idle.InFlightCount = 1

	// DRR leader first (busy), challenger second (idle): P2C must still pick idle.
	if got := p2cPick([]CandidateOffering{busy, idle}); got.AccountID != "idle" {
		t.Fatalf("P2C: leader=busy → expected idle (fewer in-flight), got %q", got.AccountID)
	}
	// Order reversed: still idle.
	if got := p2cPick([]CandidateOffering{idle, busy}); got.AccountID != "idle" {
		t.Fatalf("P2C: leader=idle → expected idle, got %q", got.AccountID)
	}
	// Fully-equal signals → keep the DRR leader (top2[0]).
	a := candWithWeight("a", accountsdomain.FundingFree, 50)
	b := candWithWeight("b", accountsdomain.FundingFree, 50)
	if got := p2cPick([]CandidateOffering{a, b}); got.AccountID != "a" {
		t.Fatalf("P2C: equal signals → expected DRR leader a, got %q", got.AccountID)
	}
}

// --- No funding target ----------------------------------------------------

// TestSelectMaxAccount_NoFundingTarget proves funding never influences the
// pick: a mixed-funding fixture and a uniform-funding fixture that are
// otherwise identical (same AccountIDs, weights, health, latency, in-flight)
// produce an identical selection sequence over many rounds.
//
// Mutation row R-M4: make funding a P2C/DRR tiebreak → the mixed and uniform
// sequences diverge → this test RED.
func TestSelectMaxAccount_NoFundingTarget(t *testing.T) {
	const rounds = 400

	mixed := []CandidateOffering{
		candWithWeight("a", accountsdomain.FundingPaid, 50),
		candWithWeight("b", accountsdomain.FundingFree, 30),
		candWithWeight("c", accountsdomain.FundingPaid, 20),
	}
	uniform := []CandidateOffering{
		candWithWeight("a", accountsdomain.FundingFree, 50),
		candWithWeight("b", accountsdomain.FundingFree, 30),
		candWithWeight("c", accountsdomain.FundingFree, 20),
	}

	seqMixed := runMaxSequence(t, mixed, rounds)
	seqUniform := runMaxSequence(t, uniform, rounds)

	if len(seqMixed) != rounds || len(seqUniform) != rounds {
		t.Fatalf("expected %d selections each, got %d / %d", rounds, len(seqMixed), len(seqUniform))
	}
	for i := range seqMixed {
		if seqMixed[i] != seqUniform[i] {
			t.Fatalf("funding influenced selection at round %d: mixed=%q uniform=%q", i, seqMixed[i], seqUniform[i])
		}
	}
	// Non-vacuous: the sequence must actually distribute across accounts.
	distinct := map[string]bool{}
	for _, id := range seqMixed {
		distinct[id] = true
	}
	if len(distinct) < 2 {
		t.Fatalf("no-funding-target test may be vacuous: only %d distinct accounts selected", len(distinct))
	}
}

// TestSelectMaxAccount_ExcludesUnknownWindows proves the full pipeline honors
// fail-closed: an account with nil windows never wins while a healthy account
// exists, and the chosen account is always eligible.
func TestSelectMaxAccount_ExcludesUnknownWindows(t *testing.T) {
	nilData := CandidateOffering{AccountID: "nodata", AccountHealth: accountsdomain.HealthHealthy, Funding: accountsdomain.FundingFree, QuotaWindows: nil}
	healthy := candWithWeight("ok", accountsdomain.FundingFree, 40)

	state := DRRState{}
	for i := 0; i < 50; i++ {
		var chosen CandidateOffering
		var ok bool
		chosen, state, ok = SelectMaxAccount([]CandidateOffering{nilData, healthy}, state, 1, drrTestNow, quota.DefaultStalenessWindow)
		if !ok {
			t.Fatalf("round %d: expected a selection", i)
		}
		if chosen.AccountID != "ok" {
			t.Fatalf("round %d: unknown-windows account was selected (%q)", i, chosen.AccountID)
		}
	}
}

// accountIDs extracts AccountIDs for readable failure messages.
func accountIDs(cs []CandidateOffering) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.AccountID
	}
	return out
}

// runMaxSequence drives SelectMaxAccount for n rounds and returns the chosen
// AccountID each round.
func runMaxSequence(t *testing.T, members []CandidateOffering, n int) []string {
	t.Helper()
	seq := make([]string, 0, n)
	state := DRRState{}
	for i := 0; i < n; i++ {
		chosen, next, ok := SelectMaxAccount(members, state, 1, drrTestNow, quota.DefaultStalenessWindow)
		if !ok {
			t.Fatalf("round %d: SelectMaxAccount returned ok=false", i)
		}
		seq = append(seq, chosen.AccountID)
		state = next
	}
	return seq
}
