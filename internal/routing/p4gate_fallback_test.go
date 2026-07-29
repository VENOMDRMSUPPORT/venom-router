package routing

import (
	"context"
	"errors"
	"testing"

	accountsdomain "github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

// P4-TEST-002 — fallback / stickiness safety gate (routing half).
//
// These TestP4Gate_* tests assert the selection + fallback SAFETY invariants of
// 05 §2/§3 and 01 §4.5 over the REAL exported functions (RunFallbackLoop,
// SelectAccount, quota.Estimate). The "Bifrost never re-selects" invariant lives
// in internal/execution/p4gate_noreselect_test.go, because internal/routing is
// staticgate-pure and can never import internal/execution.
//
// Determinism: the single fixed instant is drrTestNow; every fixture window is
// observed at that instant, so quota staleness never silently drives an outcome.

// gateVisionCand builds a healthy free candidate that is (or is not) certified
// for vision, with the given DRR capacity.
func gateVisionCand(account string, visionCertified bool, headroom float64) CandidateOffering {
	c := CandidateOffering{
		AccountID:       account,
		ProviderID:      "P1",
		ProviderModelID: "M1",
		Funding:         accountsdomain.FundingFree,
		AccountHealth:   accountsdomain.HealthHealthy,
		QuotaWindows:    []quota.Window{availWindow(headroom)},
	}
	if visionCertified {
		c.Certifications = map[models.Operation]models.Certification{
			models.OperationVision: {State: models.CertCertified, Truth: models.TruthSupported},
		}
	}
	return c
}

// TestP4Gate_FallbackNeverCrossesFundingBoundary proves the tier funding rule is
// a hard wall even under fallback (05 §3): under Lite (free-only) a paid
// candidate — here the one with the LARGER capacity that would otherwise win
// capacity-fairness — is never attempted.
//
// Mutation M3-F1: relax fundingAllowedByRule to admit paid under free-only → the
// high-capacity paid account is selected → this test RED.
func TestP4Gate_FallbackNeverCrossesFundingBoundary(t *testing.T) {
	paidHigh := CandidateOffering{
		AccountID: "paidHi", ProviderID: "P1", ProviderModelID: "M1",
		Funding: accountsdomain.FundingPaid, AccountHealth: accountsdomain.HealthHealthy,
		QuotaWindows: []quota.Window{availWindow(10_000)},
	}
	freeLow := CandidateOffering{
		AccountID: "freeLo", ProviderID: "P1", ProviderModelID: "M1",
		Funding: accountsdomain.FundingFree, AccountHealth: accountsdomain.HealthHealthy,
		QuotaWindows: []quota.Window{availWindow(100)},
	}
	group := RouteGroup{ProviderID: "P1", ProviderModelID: "M1", Members: []CandidateOffering{paidHigh, freeLow}}
	h := &fakeHarness{script: []scriptedAttempt{{outcome: successOutcome(), verdict: VerdictSuccess}}}
	in := baseInput(t, TierLite, group, h)

	res, err := RunFallbackLoop(context.Background(), in)
	if err != nil {
		t.Fatalf("expected success on the free route, got %v", err)
	}
	if res.AccountID != "freeLo" {
		t.Fatalf("Lite must serve the free account; served %q", res.AccountID)
	}
	for _, a := range h.executedAccounts {
		if a == "paidHi" {
			t.Fatalf("a paid account was attempted under Lite: %v", h.executedAccounts)
		}
	}
}

// TestP4Gate_FallbackNeverCrossesCapabilityBoundary proves a required-capability
// gate is a hard wall even under fallback (05 §3): a candidate not certified for
// a required capability — again the higher-capacity one — is never attempted.
//
// Mutation M3-C1: make capabilitiesCertified always return true → the
// uncertified high-capacity account is selected → this test RED.
func TestP4Gate_FallbackNeverCrossesCapabilityBoundary(t *testing.T) {
	uncertHi := gateVisionCand("uncertHi", false, 10_000)
	certLo := gateVisionCand("certLo", true, 100)
	group := RouteGroup{ProviderID: "P1", ProviderModelID: "M1", Members: []CandidateOffering{uncertHi, certLo}}
	h := &fakeHarness{script: []scriptedAttempt{{outcome: successOutcome(), verdict: VerdictSuccess}}}
	in := baseInput(t, TierPro, group, h)
	in.Requirements = Requirements{TextModality: true, ContextTokens: 100, Capabilities: []Capability{CapabilityVision}}

	res, err := RunFallbackLoop(context.Background(), in)
	if err != nil {
		t.Fatalf("expected success on the vision-certified route, got %v", err)
	}
	if res.AccountID != "certLo" {
		t.Fatalf("must serve the vision-certified account; served %q", res.AccountID)
	}
	for _, a := range h.executedAccounts {
		if a == "uncertHi" {
			t.Fatalf("a vision-uncertified account was attempted: %v", h.executedAccounts)
		}
	}
}

// TestP4Gate_StickinessNeverViolatesEligibility proves a sticky pin is honored
// only when the pinned account is genuinely eligible right now (05 §2 Step 7:
// "stickiness may never violate ... eligibility"). A pin to a saturated account
// (a stale window — nonzero headroom, so it clears the 15% threshold and only
// the saturation check can drop it) falls through to normal selection; an
// eligible pin is honored (positive control).
//
// Mutation M3-S1: drop the isSaturated check in stickyEligible → the saturated
// pin is honored → the negative assertion RED.
func TestP4Gate_StickinessNeverViolatesEligibility(t *testing.T) {
	const key = "sess-1"

	// Negative: the pin is saturated (stale) → not honored; the eligible "other"
	// account is chosen instead.
	pinnedSat := fairCand("pinned", staleWin(100))
	other := fairCand("other", availWindow(100))
	group := RouteGroup{Members: []CandidateOffering{pinnedSat, other}}
	cache := NewStickinessCache(8)
	cache.Pin(key, "pinned", drrTestNow)

	chosen, _, ok := SelectAccount(TierPro, group, key, cache, DRRState{}, 1, drrTestNow, testStale)
	if !ok {
		t.Fatalf("expected a selection")
	}
	if chosen.AccountID != "other" {
		t.Fatalf("a saturated pin must not be honored; got %q", chosen.AccountID)
	}

	// Positive control: an eligible pin IS honored, proving the negative case was
	// about eligibility, not a broken sticky path.
	pinnedOK := fairCand("pinnedOK", availWindow(1_000))
	group2 := RouteGroup{Members: []CandidateOffering{pinnedOK, other}}
	cache2 := NewStickinessCache(8)
	cache2.Pin(key, "pinnedOK", drrTestNow)

	chosen2, _, _ := SelectAccount(TierPro, group2, key, cache2, DRRState{}, 1, drrTestNow, testStale)
	if chosen2.AccountID != "pinnedOK" {
		t.Fatalf("an eligible pin must be honored; got %q", chosen2.AccountID)
	}
}

// TestP4Gate_StickinessNeverViolatesReservation proves a sticky preference can
// never force a route that cannot obtain a reservation (05 §2 Step 7:
// "stickiness may never violate quota reservations"). The pinned account is
// selected first but its reservation is REJECTED; the loop re-evaluates and
// serves an eligible alternative, never clinging to the pin.
//
// Mutation M3-S2: remove the ErrReservationRejected re-evaluate branch in
// RunFallbackLoop → the rejected reservation aborts instead of falling back →
// this test RED.
func TestP4Gate_StickinessNeverViolatesReservation(t *testing.T) {
	const key = "sess-2"
	h := &fakeHarness{
		script: []scriptedAttempt{
			{reserveRejected: true},                              // attempt 1: the pinned account cannot reserve
			{outcome: successOutcome(), verdict: VerdictSuccess}, // attempt 2: the fresh alternative
		},
		groups: []RouteGroup{loopGroup("other")}, // fresh snapshot after the rejection
	}
	in := baseInput(t, TierPro, loopGroup("sticky"), h)
	cache := NewStickinessCache(8)
	cache.Pin(key, "sticky", drrTestNow)
	in.StickinessKey = key
	in.Cache = cache

	res, err := RunFallbackLoop(context.Background(), in)
	if err != nil {
		t.Fatalf("stickiness must yield to reservation reality and fall back; got %v", err)
	}
	if res.AccountID != "other" {
		t.Fatalf("a pin that cannot reserve must not be served; served %q", res.AccountID)
	}
	if h.release != 0 {
		t.Fatalf("a rejected reservation debited nothing; release must be 0, got %d", h.release)
	}
}

// TestP4Gate_StreamingFallbackOnlyBeforeFirstByte is the gate assertion for the
// streaming boundary (05 §3; docs/11 R-12), enabled by P4-WIRE-002: a failure
// after the first byte streamed stops the loop — no second attempt, no second
// response — even with a healthy fallback group available.
//
// Mutation M3-B1: delete the StreamStarted stop branch in RunFallbackLoop → the
// loop falls back after a started stream → this test RED.
func TestP4Gate_StreamingFallbackOnlyBeforeFirstByte(t *testing.T) {
	h := &fakeHarness{script: []scriptedAttempt{
		{outcome: ExecOutcome{Err: errRetryable, StreamStarted: true}, verdict: VerdictUnknownConsumption, scope: ScopeAccount},
		{outcome: successOutcome(), verdict: VerdictSuccess, scope: ScopeRequest}, // must never run
	}}
	pool := RoutePool{Groups: []RouteGroup{
		wireGroup("P1", "M1", wireCand("a1", "P1", "M1")),
		wireGroup("P2", "M2", wireCand("a2", "P2", "M2")),
	}}
	in := wireInput(t, TierMax, pool, h)

	res, err := RunFallbackLoop(context.Background(), in)
	if !errors.Is(err, errRetryable) {
		t.Fatalf("a mid-stream failure must return its error; got %v", err)
	}
	if h.execCalls != 1 {
		t.Fatalf("no fallback after the first byte: want exactly 1 execute, got %d", h.execCalls)
	}
	if res.Response != nil {
		t.Fatalf("no second response may be produced after streaming began; got %v", res.Response)
	}
}

// TestP4Gate_UnknownQuotaStillReservesLocalSafety proves that with unknown
// provider quota a reservation is STILL made against the mandatory local-safety
// windows (02 §3, 05 §2 Step 8): quota.Estimate always emits the requests,
// concurrency, and output-token dimensions even for a bare input, and the loop
// reserves for a candidate carrying no provider quota windows at all.
//
// Mutation M3-Q1: drop the always-present UnitConcurrency (local-safety)
// allocation in quota.Estimate → the local-safety assertion RED.
func TestP4Gate_UnknownQuotaStillReservesLocalSafety(t *testing.T) {
	// (a) An unknown input (no token counts, no conversion) still yields the
	// mandatory local-safety dimensions.
	allocs, err := quota.Estimate(quota.EstimateInput{}, quota.DefaultEstimatePolicy())
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	units := map[quota.Unit]bool{}
	for _, a := range allocs {
		units[a.Unit] = true
	}
	for _, want := range []quota.Unit{quota.UnitRequests, quota.UnitConcurrency, quota.UnitOutputTokens} {
		if !units[want] {
			t.Fatalf("unknown-quota estimate is missing the local-safety dimension %q; got %v", want, units)
		}
	}

	// (b) A candidate with NO provider quota windows (unknown provider quota) is
	// still reserved — the loop always estimates+reserves against local-safety.
	windowless := CandidateOffering{
		AccountID: "noquota", ProviderID: "P1", ProviderModelID: "M1",
		Funding: accountsdomain.FundingFree, AccountHealth: accountsdomain.HealthHealthy,
	}
	group := RouteGroup{ProviderID: "P1", ProviderModelID: "M1", Members: []CandidateOffering{windowless}}
	h := &fakeHarness{script: []scriptedAttempt{{outcome: successOutcome(), verdict: VerdictSuccess}}}
	in := baseInput(t, TierPro, group, h)

	if _, err := RunFallbackLoop(context.Background(), in); err != nil {
		t.Fatalf("a windowless candidate must still reserve and succeed; got %v", err)
	}
	if h.reserveCalls < 1 {
		t.Fatalf("the loop must reserve against local-safety even with unknown provider quota; reserveCalls=%d", h.reserveCalls)
	}
}
