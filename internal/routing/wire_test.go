package routing

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	accountsdomain "github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

// wireCand builds a healthy, free, available candidate for the pool-path tests.
func wireCand(account, provider, model string) CandidateOffering {
	return CandidateOffering{
		AccountID:       account,
		ProviderID:      provider,
		ProviderModelID: model,
		AccountHealth:   accountsdomain.HealthHealthy,
		Funding:         accountsdomain.FundingFree,
		QuotaWindows:    []quota.Window{availWindow(10_000)},
	}
}

func wireGroup(provider, model string, cands ...CandidateOffering) RouteGroup {
	return RouteGroup{ProviderID: provider, ProviderModelID: model, Funding: accountsdomain.FundingFree, Members: cands}
}

// wireInput reuses baseInput's port wiring but drives the pool path with a
// scoper enabled.
func wireInput(t *testing.T, tier Tier, pool RoutePool, h *fakeHarness) FallbackInput {
	t.Helper()
	in := baseInput(t, tier, RouteGroup{}, h)
	in.Pool = pool
	in.Scoper = h
	return in
}

func hasCooldownScope(cds []quota.CooldownTrigger, scope quota.CooldownScope) bool {
	for _, cd := range cds {
		if cd.Scope == scope {
			return true
		}
	}
	return false
}

// TestWire_BlockedAndCooledNeverAttempted proves FilterEligible narrows the pool
// before selection: an account with an open breaker or an active cooldown is
// never attempted.
//
// Mutation row W-M1: skip FilterEligible in currentEligibleGroup → the blocked
// account is selected first → this test RED.
func TestWire_BlockedAndCooledNeverAttempted(t *testing.T) {
	now := drrTestNow
	h := &fakeHarness{script: []scriptedAttempt{{outcome: successOutcome(), verdict: VerdictSuccess, scope: ScopeRequest}}}
	pool := RoutePool{Groups: []RouteGroup{wireGroup("P1", "M1",
		wireCand("blocked", "P1", "M1"),
		wireCand("cooled", "P1", "M1"),
		wireCand("ok", "P1", "M1"),
	)}}
	in := wireInput(t, TierPro, pool, h)
	in.Breakers = BreakerSet{Account: map[string]Breaker{"blocked": Breaker{}.Trip(now)}}
	in.Cooldowns = []quota.Cooldown{{Scope: quota.CooldownScopeAccount, AccountID: strptr("cooled"), Until: now.Add(time.Minute)}}

	res, err := RunFallbackLoop(context.Background(), in)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if res.AccountID != "ok" {
		t.Fatalf("expected the only eligible account 'ok', got %q", res.AccountID)
	}
	for _, a := range h.executedAccounts {
		if a == "blocked" || a == "cooled" {
			t.Fatalf("a blocked/cooled candidate was attempted: %v", h.executedAccounts)
		}
	}
}

// TestWire_SkipProviderDropsAllProviderGroups proves ActionSkipProvider removes
// EVERY group of that provider for the remainder of the request, so a later
// attempt targets a different provider.
//
// Mutation row W-M2: drop only the current group (offering) instead of the
// whole provider → the second attempt targets the other P1 account → RED.
func TestWire_SkipProviderDropsAllProviderGroups(t *testing.T) {
	h := &fakeHarness{script: []scriptedAttempt{
		{outcome: ExecOutcome{Err: errRetryable}, verdict: VerdictUnknownConsumption, scope: ScopeProvider},
		{outcome: successOutcome(), verdict: VerdictSuccess, scope: ScopeRequest},
	}}
	pool := RoutePool{Groups: []RouteGroup{
		wireGroup("P1", "M1", wireCand("a1", "P1", "M1")),
		wireGroup("P1", "M2", wireCand("a2", "P1", "M2")),
		wireGroup("P2", "M3", wireCand("a3", "P2", "M3")),
	}}
	in := wireInput(t, TierMax, pool, h)

	res, err := RunFallbackLoop(context.Background(), in)
	if err != nil {
		t.Fatalf("expected success on the P2 route, got %v", err)
	}
	if res.ProviderID != "P2" {
		t.Fatalf("expected the surviving provider P2, got %q", res.ProviderID)
	}
	want := []string{"a1", "a3"}
	if !reflect.DeepEqual(h.executedAccounts, want) {
		t.Fatalf("expected attempts %v (all of P1 skipped), got %v", want, h.executedAccounts)
	}
}

// TestWire_NextOfferingMovesToDifferentGroup proves ActionNextOffering steers to
// a different group (the tripped offering is filtered out next round).
func TestWire_NextOfferingMovesToDifferentGroup(t *testing.T) {
	h := &fakeHarness{script: []scriptedAttempt{
		{outcome: ExecOutcome{Err: errRetryable}, verdict: VerdictPreConsumptionFailure, scope: ScopeOffering},
		{outcome: successOutcome(), verdict: VerdictSuccess, scope: ScopeRequest},
	}}
	pool := RoutePool{Groups: []RouteGroup{
		wireGroup("P1", "M1", wireCand("a1", "P1", "M1")),
		wireGroup("P1", "M2", wireCand("a1b", "P1", "M2")),
	}}
	in := wireInput(t, TierMax, pool, h)

	if _, err := RunFallbackLoop(context.Background(), in); err != nil {
		t.Fatalf("expected success on the second offering, got %v", err)
	}
	want := []string{"M1", "M2"}
	if !reflect.DeepEqual(h.executedModels, want) {
		t.Fatalf("expected offerings %v, got %v", want, h.executedModels)
	}
}

// TestWire_BoundedTransientRetry proves the transient-transport path retries the
// SAME candidate at most TransientMaxRetries times with the expected COMPUTED
// backoffs — and never sleeps (the test would hang if it did).
//
// Mutation row W-M4: allow unbounded transient retries → the candidate is hit
// more than TransientMaxRetries times → this test RED.
func TestWire_BoundedTransientRetry(t *testing.T) {
	script := make([]scriptedAttempt, TransientMaxRetries)
	for i := range script {
		script[i] = scriptedAttempt{outcome: ExecOutcome{Err: errRetryable}, verdict: VerdictUnknownConsumption, scope: ScopeTransientTransport}
	}
	h := &fakeHarness{script: script}
	pool := RoutePool{Groups: []RouteGroup{wireGroup("P1", "M1", wireCand("a1", "P1", "M1"))}}
	in := wireInput(t, TierMax, pool, h) // budget 5 > 3, so the bound (not the budget) is what stops it

	res, err := RunFallbackLoop(context.Background(), in)
	var noOffer *NoEligibleOfferingError
	if !errors.As(err, &noOffer) {
		t.Fatalf("expected exhaustion after bounded retries, got %v", err)
	}
	if len(h.executedAccounts) != TransientMaxRetries {
		t.Fatalf("transient candidate hit %d times, want at most %d", len(h.executedAccounts), TransientMaxRetries)
	}
	wantBackoffs := []time.Duration{TransientBackoff(1), TransientBackoff(2)}
	if !reflect.DeepEqual(res.TransientBackoffs, wantBackoffs) {
		t.Fatalf("computed backoffs = %v, want %v", res.TransientBackoffs, wantBackoffs)
	}
}

// TestWire_ProviderCooldownRequiresCrossAccount proves a provider cooldown does
// NOT trip from a single account's failure, but DOES once a second distinct
// account of the same provider has failed (evidence carried across requests).
//
// Mutation row W-M3: ignore CrossAccount() (force cross-account true) → the
// single-account scenario trips a provider cooldown → this test RED.
func TestWire_ProviderCooldownRequiresCrossAccount(t *testing.T) {
	mkProviderFail := func() *fakeHarness {
		return &fakeHarness{script: []scriptedAttempt{{outcome: ExecOutcome{Err: errRetryable}, verdict: VerdictUnknownConsumption, scope: ScopeProvider}}}
	}

	// Scenario 1: fresh evidence, one account → no provider cooldown.
	h1 := mkProviderFail()
	in1 := wireInput(t, TierMax, RoutePool{Groups: []RouteGroup{wireGroup("P1", "M1", wireCand("a1", "P1", "M1"))}}, h1)
	res1, _ := RunFallbackLoop(context.Background(), in1)
	if hasCooldownScope(res1.Cooldowns, quota.CooldownScopeProvider) {
		t.Fatalf("single-account provider failure must NOT trip a provider cooldown")
	}
	// GOVERNOR ADDITION: the persisted provider BREAKER must not trip either.
	// Asserting only on cooldowns left the breaker half of this load-bearing rule
	// uncovered — a regression tripping the breaker without cross-account evidence
	// would take a whole provider out for every OTHER account, and passed the test.
	if b := res1.Breakers.Provider["P1"]; b.State != "" && b.State != BreakerClosed {
		t.Fatalf("single-account provider failure must NOT trip the provider breaker; got %q (cycles=%d)", b.State, b.OpenCycles)
	}

	// Scenario 2: evidence already holds a1 (a prior request); a2 fails now.
	h2 := mkProviderFail()
	in2 := wireInput(t, TierMax, RoutePool{Groups: []RouteGroup{wireGroup("P1", "M1", wireCand("a2", "P1", "M1"))}}, h2)
	ev := NewProviderFailureEvidence()
	ev.Observe("a1")
	in2.ProviderEvidence = map[string]*ProviderFailureEvidence{"P1": ev}
	res2, _ := RunFallbackLoop(context.Background(), in2)
	if !hasCooldownScope(res2.Cooldowns, quota.CooldownScopeProvider) {
		t.Fatalf("cross-account provider failure MUST trip a provider cooldown")
	}
}

// TestWire_SuccessClosesBreaker proves a successful attempt closes (and resets
// the backoff of) the chosen account's breaker.
func TestWire_SuccessClosesBreaker(t *testing.T) {
	now := drrTestNow
	h := &fakeHarness{script: []scriptedAttempt{{outcome: successOutcome(), verdict: VerdictSuccess, scope: ScopeRequest}}}
	pool := RoutePool{Groups: []RouteGroup{wireGroup("P1", "M1", wireCand("a1", "P1", "M1"))}}
	in := wireInput(t, TierMax, pool, h)
	// a1's breaker is half-open: tripped a full base window ago, so it admits a probe now.
	in.Breakers = BreakerSet{Account: map[string]Breaker{"a1": Breaker{}.Trip(now.Add(-BreakerBaseTimeout))}}

	res, err := RunFallbackLoop(context.Background(), in)
	if err != nil {
		t.Fatalf("expected the half-open probe to succeed, got %v", err)
	}
	b := res.Breakers.Account["a1"]
	if b.State != BreakerClosed || b.OpenCycles != 0 {
		t.Fatalf("success must close the breaker and reset backoff; got state=%q cycles=%d", b.State, b.OpenCycles)
	}
}

// TestWire_PreservesRoute013InvariantsWithScoper proves the ROUTE-013 crown
// invariants still hold on the scope-classified pool path: reserve-before-
// execute, exactly one terminal op per executed attempt, mark-dispatched
// ordering, and distinct reservation ids.
func TestWire_PreservesRoute013InvariantsWithScoper(t *testing.T) {
	h := &fakeHarness{script: []scriptedAttempt{
		{outcome: ExecOutcome{Err: errRetryable}, verdict: VerdictPreConsumptionFailure, scope: ScopeOffering},
		{outcome: successOutcome(), verdict: VerdictSuccess, scope: ScopeRequest},
	}}
	pool := RoutePool{Groups: []RouteGroup{
		wireGroup("P1", "M1", wireCand("a1", "P1", "M1")),
		wireGroup("P1", "M2", wireCand("a2", "P1", "M2")),
	}}
	in := wireInput(t, TierMax, pool, h)

	if _, err := RunFallbackLoop(context.Background(), in); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if h.reserveBeforeExecuteViolation {
		t.Fatalf("an attempt executed before a successful reserve")
	}
	terminal := h.settle + h.settleEstimate + h.release + h.markPending
	if terminal != h.execCalls {
		t.Fatalf("terminal ops (%d) != executed attempts (%d)", terminal, h.execCalls)
	}
	if h.markDispatched != h.execCalls {
		t.Fatalf("mark-dispatched (%d) must equal executed attempts (%d)", h.markDispatched, h.execCalls)
	}
	if len(h.reservationIDs) != 2 || h.reservationIDs[0] == h.reservationIDs[1] {
		t.Fatalf("reservation ids must be distinct per attempt: %v", h.reservationIDs)
	}
}

// TestWire_SuccessClosesEveryScopeBreaker is a GOVERNOR-ADDED regression test.
//
// The batch closed only the ACCOUNT breaker on success, so an offering or
// provider breaker — once tripped — could never return to closed: it stayed
// half-open with its inflated OpenCycles, and its adaptive backoff was never
// reset (verified before the fix: offering and provider both stayed
// state=half_open cycles=4 resetTimeout=4m0s after a fully successful route).
// 05 §3 requires that a success closes the breaker AND resets the backoff, and a
// successful route is positive evidence for all three of its scopes.
//
// Mutation: close only the Account breaker → this test RED on offering/provider.
func TestWire_SuccessClosesEveryScopeBreaker(t *testing.T) {
	group := wireGroup("provX", "modelX", wireCand("acc1", "provX", "modelX"))
	h := &fakeHarness{script: []scriptedAttempt{{outcome: successOutcome()}}}
	in := baseInput(t, TierPro, group, h)
	in.Pool = SingleGroupPool(group)
	in.Scoper = h
	in.Breakers = BreakerSet{
		Account:  map[string]Breaker{"acc1": {State: BreakerHalfOpen, OpenCycles: 3}},
		Offering: map[string]Breaker{"modelX": {State: BreakerHalfOpen, OpenCycles: 4}},
		Provider: map[string]Breaker{"provX": {State: BreakerHalfOpen, OpenCycles: 4}},
	}

	res, err := RunFallbackLoop(context.Background(), in)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	for name, b := range map[string]Breaker{
		"account":  res.Breakers.Account["acc1"],
		"offering": res.Breakers.Offering["modelX"],
		"provider": res.Breakers.Provider["provX"],
	} {
		if b.State != BreakerClosed {
			t.Errorf("%s breaker = %q after a successful route, want closed", name, b.State)
		}
		if b.OpenCycles != 0 {
			t.Errorf("%s breaker OpenCycles = %d after success, want 0 (backoff reset)", name, b.OpenCycles)
		}
		if b.ResetTimeout() != BreakerBaseTimeout {
			t.Errorf("%s breaker reset timeout = %s after success, want base %s", name, b.ResetTimeout(), BreakerBaseTimeout)
		}
	}
}

// TestWire_HalfOpenProbeIsMarkedInFlight is a GOVERNOR-ADDED regression test.
//
// The batch never called Breaker.MarkProbe anywhere in production, so a
// persisted half-open breaker kept ProbeInFlight=false and Admits() returned
// true for EVERY caller — the "half-open admits exactly one probe" invariant
// that ROUTE-014 built and mutation-proved (R14-M6) was inert, and a recovering
// scope would be hammered by concurrent requests instead of probed once.
//
// The attempt the loop runs through a half-open breaker IS that scope's trial,
// so the breaker must come back with the probe marked (when the attempt fails
// and does not itself Trip/close the scope).
//
// Mutation: drop the markHalfOpenProbes call → this test RED.
func TestWire_HalfOpenProbeIsMarkedInFlight(t *testing.T) {
	group := wireGroup("provX", "modelX", wireCand("acc1", "provX", "modelX"))
	// A pre-consumption failure with an OFFERING scope: it trips the offering
	// breaker, so assert on the PROVIDER breaker, which this attempt probed but
	// does not itself transition.
	h := &fakeHarness{script: []scriptedAttempt{{
		outcome: ExecOutcome{Err: errRetryable},
		verdict: VerdictPreConsumptionFailure,
		scope:   ScopeOffering,
	}}}
	in := baseInput(t, TierPro, group, h)
	in.Policy.AttemptBudget = 1
	in.Pool = SingleGroupPool(group)
	in.Scoper = h
	in.Breakers = BreakerSet{
		Account:  map[string]Breaker{},
		Offering: map[string]Breaker{},
		Provider: map[string]Breaker{"provX": {State: BreakerHalfOpen, OpenCycles: 2}},
	}

	res, _ := RunFallbackLoop(context.Background(), in)
	prov := res.Breakers.Provider["provX"]
	if !prov.ProbeInFlight {
		t.Fatalf("the half-open provider breaker must have its probe marked in flight after the loop consumed it")
	}
	if prov.Admits(drrTestNow) {
		t.Fatalf("a half-open breaker with a probe in flight must admit no further request")
	}
}
