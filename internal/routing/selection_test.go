package routing

import (
	"testing"
	"time"

	accountsdomain "github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

// freshWin builds a fresh provider-evidence window with the given remaining
// capacity and reserved amount (headroom = remaining - reserved), observed at
// drrTestNow — the instant every test below evaluates at, except the TTL test.
func freshWin(remaining, reserved float64) quota.Window {
	return freshWinAt(remaining, reserved, drrTestNow)
}

// freshWinAt is freshWin with an explicit observation instant. Needed by the
// TTL test, which evaluates at a LATER instant: a window observed at
// drrTestNow would be quota-STALE (and therefore saturated) by then, which
// would drop the pin for a reason unrelated to the stickiness TTL.
func freshWinAt(remaining, reserved float64, observedAt time.Time) quota.Window {
	r := remaining
	return quota.Window{Remaining: &r, Reserved: reserved, Freshness: quota.FreshnessFresh, ObservedAt: observedAt}
}

// staleWin builds a stale window: nominal headroom but StateStale ⇒ saturated.
func staleWin(remaining float64) quota.Window {
	r := remaining
	return quota.Window{Remaining: &r, Freshness: quota.FreshnessStale, ObservedAt: drrTestNow}
}

// fairCand builds a healthy, non-cooling free candidate with the given windows.
func fairCand(id string, wins ...quota.Window) CandidateOffering {
	return CandidateOffering{
		AccountID:     id,
		ProviderID:    "prov",
		AccountHealth: accountsdomain.HealthHealthy,
		Funding:       accountsdomain.FundingFree,
		QuotaWindows:  wins,
	}
}

const testStale = quota.DefaultStalenessWindow

// --- (c) SelectFairAccount --------------------------------------------------

// TestSelectFairAccount_ExcludesSaturated proves a saturated account is never
// selected even when it out-ranks every eligible account on the fairness
// signals. The "star" account has the largest nominal headroom but is stale
// (saturated); "plain" is a smaller but genuinely available account.
//
// Mutation row S-M2: remove the SaturationFilter call in SelectFairAccount →
// the stale "star" (highest weight) wins → this test RED.
func TestSelectFairAccount_ExcludesSaturated(t *testing.T) {
	star := fairCand("star", staleWin(10_000)) // huge headroom but STALE ⇒ saturated
	plain := fairCand("plain", freshWin(100, 0))

	chosen, ok := SelectFairAccount([]CandidateOffering{star, plain}, 1, drrTestNow, testStale)
	if !ok {
		t.Fatalf("expected a selection")
	}
	if chosen.AccountID != "plain" {
		t.Fatalf("saturated 'star' must be excluded; got %q", chosen.AccountID)
	}
}

// TestSelectFairAccount_PrefersBetterSignals constructs an account that is
// strictly better on every capacity-fairness signal (more headroom, fewer
// in-flight, higher reliability, higher latency score) and asserts it wins —
// robust to whether the ranking is a ladder or a weighted sum.
func TestSelectFairAccount_PrefersBetterSignals(t *testing.T) {
	good := fairCand("good", freshWin(500, 0))
	good.InFlightCount = 0
	good.Reliability = f64p(0.9)
	good.LatencyScore = f64p(0.9)

	meh := fairCand("meh", freshWin(50, 0))
	meh.InFlightCount = 7
	meh.Reliability = f64p(0.2)
	meh.LatencyScore = f64p(0.2)

	chosen, ok := SelectFairAccount([]CandidateOffering{meh, good}, 1, drrTestNow, testStale)
	if !ok || chosen.AccountID != "good" {
		t.Fatalf("expected 'good' to win on combined signals, got %q (ok=%v)", chosen.AccountID, ok)
	}
}

// TestSelectFairAccount_EmptyReturnsFalse proves an empty group yields ok=false
// without panicking.
func TestSelectFairAccount_EmptyReturnsFalse(t *testing.T) {
	if _, ok := SelectFairAccount(nil, 1, drrTestNow, testStale); ok {
		t.Fatalf("empty group must return ok=false")
	}
}

// TestSelectFairAccount_Deterministic proves identical inputs give identical
// output across repeated calls.
func TestSelectFairAccount_Deterministic(t *testing.T) {
	members := []CandidateOffering{
		fairCand("a", freshWin(100, 0)),
		fairCand("b", freshWin(100, 0)),
		fairCand("c", freshWin(100, 0)),
	}
	c1, _ := SelectFairAccount(members, 1, drrTestNow, testStale)
	c2, _ := SelectFairAccount(members, 1, drrTestNow, testStale)
	if c1.AccountID != c2.AccountID {
		t.Fatalf("determinism: %q != %q", c1.AccountID, c2.AccountID)
	}
}

// --- (d) SelectAccount: stickiness preference -------------------------------

// stickyGroup builds a two-member group where `sticky` is the pinned account
// and `alt` is a strictly-better fair alternative, so a dropped pin visibly
// falls through to `alt`.
func stickyGroup(sticky CandidateOffering) (RouteGroup, string) {
	alt := fairCand("alt", freshWin(10_000, 0)) // clearly the fair winner
	return RouteGroup{Members: []CandidateOffering{sticky, alt}}, "alt"
}

// pinnedCache returns a cache with `sticky.AccountID` pinned fresh at drrTestNow.
func pinnedCache(accountID string) *StickinessCache {
	c := NewStickinessCache(8)
	c.Pin("sticky-key", accountID, drrTestNow)
	return c
}

// TestSelectAccount_StickyPreferredWhenValid proves the positive path: a valid
// pin returns the pinned account, bypassing the fair alternative — otherwise
// every drop test below would be vacuous.
func TestSelectAccount_StickyPreferredWhenValid(t *testing.T) {
	sticky := fairCand("sticky", freshWin(100, 0)) // eligible, >15%, healthy, not cooling
	group, _ := stickyGroup(sticky)
	cache := pinnedCache("sticky")

	chosen, _, ok := SelectAccount(TierPro, group, "sticky-key", cache, nil, 1, drrTestNow, testStale)
	if !ok || chosen.AccountID != "sticky" {
		t.Fatalf("valid pin must be preferred; got %q (ok=%v)", chosen.AccountID, ok)
	}
}

// TestSelectAccount_StickyDroppedOnLowHeadroom proves a pin is dropped when the
// account's headroom is ≤15% on some window, falling through to fair selection.
//
// Mutation row S-M3: remove the 15%-headroom drop check → the sticky account is
// preferred despite low headroom → this test RED (chosen == "sticky").
func TestSelectAccount_StickyDroppedOnLowHeadroom(t *testing.T) {
	// remaining 100, reserved 90 ⇒ headroom 10 = 10% ≤ 15% ⇒ drop. Still
	// available for need=1 (10 ≥ 1), so ONLY the 15% rule triggers the drop.
	sticky := fairCand("sticky", freshWin(100, 90))
	group, want := stickyGroup(sticky)
	cache := pinnedCache("sticky")

	chosen, _, ok := SelectAccount(TierPro, group, "sticky-key", cache, nil, 1, drrTestNow, testStale)
	if !ok || chosen.AccountID != want {
		t.Fatalf("low-headroom pin must be dropped → %q; got %q", want, chosen.AccountID)
	}
}

// TestSelectAccount_StickyDroppedOnCooling proves a cooling pinned account is
// dropped.
func TestSelectAccount_StickyDroppedOnCooling(t *testing.T) {
	sticky := fairCand("sticky", freshWin(100, 0))
	sticky.Cooling = true
	group, want := stickyGroup(sticky)
	cache := pinnedCache("sticky")

	chosen, _, ok := SelectAccount(TierPro, group, "sticky-key", cache, nil, 1, drrTestNow, testStale)
	if !ok || chosen.AccountID != want {
		t.Fatalf("cooling pin must be dropped → %q; got %q", want, chosen.AccountID)
	}
}

// TestSelectAccount_StickyDroppedOnUnhealthy proves an unhealthy pinned account
// is dropped.
func TestSelectAccount_StickyDroppedOnUnhealthy(t *testing.T) {
	sticky := fairCand("sticky", freshWin(100, 0))
	sticky.AccountHealth = accountsdomain.HealthDegraded
	group, want := stickyGroup(sticky)
	cache := pinnedCache("sticky")

	chosen, _, ok := SelectAccount(TierPro, group, "sticky-key", cache, nil, 1, drrTestNow, testStale)
	if !ok || chosen.AccountID != want {
		t.Fatalf("unhealthy pin must be dropped → %q; got %q", want, chosen.AccountID)
	}
}

// TestSelectAccount_StickyDroppedOnLeftPool proves a pin whose account is no
// longer among the group's Members is dropped.
func TestSelectAccount_StickyDroppedOnLeftPool(t *testing.T) {
	group := RouteGroup{Members: []CandidateOffering{fairCand("alt", freshWin(10_000, 0))}}
	cache := pinnedCache("gone") // pinned account not in Members

	chosen, _, ok := SelectAccount(TierPro, group, "sticky-key", cache, nil, 1, drrTestNow, testStale)
	if !ok || chosen.AccountID != "alt" {
		t.Fatalf("pin for an absent account must be dropped → alt; got %q", chosen.AccountID)
	}
}

// TestSelectAccount_StickyDroppedOnTTL proves a pin older than the TTL is
// dropped (the cache lookup returns ok=false).
//
// GOVERNOR CORRECTION: the original version built both accounts with
// freshWin(...) — i.e. windows observed at drrTestNow — and then evaluated at
// `later` (drrTestNow + TTL + 1min). By that instant those windows are older
// than quota.DefaultStalenessWindow, so BOTH accounts were quota-STALE and the
// pinned one was already saturated: the pin was dropped by the saturation gate,
// not by the stickiness TTL. The test therefore passed with the TTL check
// entirely removed from Lookup (confirmed by hand), making it vacuous against
// its own stated invariant. Both windows are now observed AT `later`, so they
// are quota-fresh at evaluation time and the ONLY thing that can drop the pin
// is the stickiness TTL.
//
// Mutation row S-M1 (at this level): remove the TTL check in Lookup → the pin
// is honored and "sticky" is returned instead of "alt" → this test RED.
func TestSelectAccount_StickyDroppedOnTTL(t *testing.T) {
	later := drrTestNow.Add(StickinessTTL + time.Minute)

	// Both windows are observed at `later` ⇒ quota-fresh when evaluated there,
	// so neither account is saturated and staleness cannot confound the drop.
	sticky := fairCand("sticky", freshWinAt(100, 0, later))
	alt := fairCand("alt", freshWinAt(10_000, 0, later))
	group := RouteGroup{Members: []CandidateOffering{sticky, alt}}
	cache := pinnedCache("sticky")

	chosen, _, ok := SelectAccount(TierPro, group, "sticky-key", cache, nil, 1, later, testStale)
	if !ok || chosen.AccountID != "alt" {
		t.Fatalf("expired pin must be dropped → alt; got %q", chosen.AccountID)
	}
}

// TestSelectAccount_NeverViolatesEligibility proves a pin whose account is
// saturated (StateInsufficient for a large need) is never chosen via the sticky
// path, even though its headroom fraction is comfortably above 15%. This shows
// SaturationFilter eligibility — not the 15% threshold — is authoritative.
func TestSelectAccount_NeverViolatesEligibility(t *testing.T) {
	// headroom 100 = 100% of capacity (passes the 15% check) but < need=200 ⇒
	// StateInsufficient ⇒ saturated ⇒ ineligible.
	sticky := fairCand("sticky", freshWin(100, 0))
	alt := fairCand("alt", freshWin(10_000, 0)) // headroom 10000 ≥ 200 ⇒ eligible
	group := RouteGroup{Members: []CandidateOffering{sticky, alt}}
	cache := pinnedCache("sticky")

	chosen, _, ok := SelectAccount(TierPro, group, "sticky-key", cache, nil, 200, drrTestNow, testStale)
	if !ok || chosen.AccountID != "alt" {
		t.Fatalf("saturated pin must never be chosen; got %q", chosen.AccountID)
	}
}

// TestSelectAccount_MaxDefersToSelectMaxAccount proves the Max path delegates to
// SelectMaxAccount verbatim — no duplicate/divergent Max logic — when no valid
// pin exists.
func TestSelectAccount_MaxDefersToSelectMaxAccount(t *testing.T) {
	members := []CandidateOffering{
		candWithWeight("m1", accountsdomain.FundingFree, 60),
		candWithWeight("m2", accountsdomain.FundingFree, 30),
	}
	group := RouteGroup{Members: members}

	wantChosen, _, wantOK := SelectMaxAccount(members, DRRState{}, 1, drrTestNow, testStale)
	gotChosen, _, gotOK := SelectAccount(TierMax, group, "", nil, DRRState{}, 1, drrTestNow, testStale)

	if wantOK != gotOK || wantChosen.AccountID != gotChosen.AccountID {
		t.Fatalf("Max path diverged from SelectMaxAccount: want %q(%v) got %q(%v)",
			wantChosen.AccountID, wantOK, gotChosen.AccountID, gotOK)
	}
}

// TestSelectAccount_DoesNotPin proves SelectAccount never writes to the cache —
// pinning is the caller's job, only on a genuinely successful response.
//
// Mutation row S-M4: make SelectAccount call cache.Pin internally → the fresh
// key becomes present after selection → this test RED.
func TestSelectAccount_DoesNotPin(t *testing.T) {
	group := RouteGroup{Members: []CandidateOffering{fairCand("a", freshWin(100, 0))}}
	cache := NewStickinessCache(8)

	_, _, ok := SelectAccount(TierPro, group, "fresh-key", cache, nil, 1, drrTestNow, testStale)
	if !ok {
		t.Fatalf("expected a selection")
	}
	if _, present := cache.Lookup("fresh-key", drrTestNow); present {
		t.Fatalf("SelectAccount must NOT pin — 'fresh-key' should be absent from the cache")
	}
}
