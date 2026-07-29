package routing

import (
	"context"
	"errors"
	"math"
	"testing"

	accountsdomain "github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

// P4-TEST-001 — Lite/Pro/Max distribution acceptance gate, mechanized.
//
// These TestP4Gate_* tests drive the REAL exported selection pipeline
// (BuildCandidatePool → ApplyHardGates → BuildRouteGroups → ScoreGroups →
// ApplyCompetitiveBand → PreferByDeficit / DRRRound / SelectMaxAccount); no
// step is re-implemented here. Every numeric threshold that is a tier POLICY
// value is read from Policies() (never a literal); the product-decision
// thresholds (25% paid target, ±5 pp tolerance, N = 2,000) come from
// 05 §8.1 / 06 Phase 4 and are named constants with their citation.
//
// Determinism: the single fixed instant is drrTestNow (defined in drr_test.go);
// every fixture quota window is observed AT that instant (availWindow), so quota
// staleness can never silently supply an expected outcome.

// --- product-decision thresholds (05 §8.1 / 06 Phase 4; NOT policy fields) ---

const (
	gateProPaidShareTarget    = 0.25  // 05 §8.1: Pro ~25% paid / ~75% free.
	gateProTolerancePP        = 0.05  // 06 Phase 4: realized share within ±5 pp.
	gateProConvergenceSamples = 2_000 // 05 §8.1: N = 2,000 synthetic successes.
)

// gateBucketReqs returns one Requirements value per standard workload bucket
// (05 §2 Step 7). The bucket keys these produce under a default BucketKeyer are
// exactly {standard, vision, tool_use, structured, large_context}.
func gateBucketReqs() map[string]Requirements {
	return map[string]Requirements{
		"standard":      {TextModality: true, ContextTokens: 1_000},
		"vision":        {VisionModality: true, Capabilities: []Capability{CapabilityVision}, ContextTokens: 1_000},
		"tool_use":      {Capabilities: []Capability{CapabilityTools}, ContextTokens: 1_000},
		"structured":    {Capabilities: []Capability{CapabilityStructuredOutput}, ContextTokens: 1_000},
		"large_context": {TextModality: true, ContextTokens: DefaultLargeContextThreshold + 8_000},
	}
}

// gateFullyCertified builds a candidate certified+supported for every operation
// a bucket request can require, healthy, credential-valid, with a large verified
// context ceiling and one fresh available quota window (observed at drrTestNow).
// qualityRating is the raw 0–100 canonical rating; headroom is the window's
// remaining capacity (also the account's DRR weight).
func gateFullyCertified(provider, account, model string, funding accountsdomain.Funding, qualityRating, headroom float64) CandidateOffering {
	ceiling := int64(1_048_576) // ≥ every gate request's context need and every tier ceiling input here
	q := qualityRating
	cert := models.Certification{State: models.CertCertified, Truth: models.TruthSupported}
	return CandidateOffering{
		ProviderID:            provider,
		AccountID:             account,
		ProviderModelID:       model,
		Funding:               funding,
		AccountHealth:         accountsdomain.HealthHealthy,
		CredentialValid:       true,
		VerifiedContextTokens: &ceiling,
		Certifications: map[models.Operation]models.Certification{
			models.OperationChat:             cert,
			models.OperationStreaming:        cert,
			models.OperationTools:            cert,
			models.OperationStructuredOutput: cert,
			models.OperationVision:           cert,
		},
		ReasoningCertified:    true,
		ReasoningCertifiedMax: ThinkingUltra,
		QualityRating:         &q,
		QuotaWindows:          []quota.Window{availWindow(headroom)},
	}
}

// gatePipeline runs the deterministic Step 2→6 pipeline for one request and
// returns the competitive-band-filtered group scores. It never re-implements a
// step — it calls the exported functions in order.
func gatePipeline(t *testing.T, fleet []CandidateOffering, reqs Requirements, policy TierPolicy) ([]GroupScore, []CandidateOffering) {
	t.Helper()
	pool := BuildCandidatePool(models.OperationChat, fleet)
	eligible, _, err := ApplyHardGates(pool, reqs, policy)
	if err != nil {
		t.Fatalf("ApplyHardGates: %v", err)
	}
	groups := BuildRouteGroups(eligible)
	scored := ScoreGroups(groups, policy)
	inBand := ApplyCompetitiveBand(scored, policy)
	return inBand, eligible
}

// ============================ LITE ==========================================

// TestP4Gate_LiteNeverSelectsPaid proves the Lite gate's categorical invariant:
// over a fleet that contains paid offerings, ZERO paid offerings survive the
// hard gates and the served account is always free (05 §1: Lite is free-only,
// paid is a hard rejection, never a fallback).
//
// Mutation M2-L1: relax the Lite funding gate in ApplyHardGates to admit paid →
// a paid offering appears in `eligible` → this test RED.
func TestP4Gate_LiteNeverSelectsPaid(t *testing.T) {
	lite := mustPolicy(t, TierLite)
	paidSelections := 0

	for bucket, reqs := range gateBucketReqs() {
		fleet := []CandidateOffering{
			gateFullyCertified("pFree", "aFree", "m1", accountsdomain.FundingFree, 90, 500),
			gateFullyCertified("pPaid", "aPaid", "m1", accountsdomain.FundingPaid, 99, 500),
		}
		inBand, eligible := gatePipeline(t, fleet, reqs, lite)

		for _, c := range eligible {
			if c.Funding == accountsdomain.FundingPaid {
				t.Fatalf("bucket %s: a paid offering survived Lite's hard gates: %+v", bucket, c)
			}
		}
		if len(inBand) == 0 {
			t.Fatalf("bucket %s: expected a free route to survive, got none", bucket)
		}
		chosen, ok := SelectFairAccount(inBand[0].Group.Members, 1, drrTestNow, testStale)
		if !ok {
			t.Fatalf("bucket %s: no account selected", bucket)
		}
		if chosen.Funding == accountsdomain.FundingPaid {
			paidSelections++
		}
	}
	if paidSelections != 0 {
		t.Fatalf("Lite selected a paid account %d times over the sample, want 0 (categorical)", paidSelections)
	}
}

// TestP4Gate_LiteFailsClosedOnFreeExhaustion proves Lite fails CLOSED when no
// free offering is available: a fleet of paid-only offerings yields zero
// eligible routes rather than promoting a paid one (05 §1).
//
// Mutation M2-L2: relax the Lite funding gate to admit paid → the paid-only
// fleet yields a non-empty eligible set → this test RED.
func TestP4Gate_LiteFailsClosedOnFreeExhaustion(t *testing.T) {
	lite := mustPolicy(t, TierLite)
	fleet := []CandidateOffering{
		gateFullyCertified("pPaid1", "aPaid1", "m1", accountsdomain.FundingPaid, 99, 500),
		gateFullyCertified("pPaid2", "aPaid2", "m2", accountsdomain.FundingPaid, 95, 500),
	}
	for bucket, reqs := range gateBucketReqs() {
		inBand, eligible := gatePipeline(t, fleet, reqs, lite)
		if len(eligible) != 0 {
			t.Fatalf("bucket %s: free exhaustion must fail closed; got eligible=%+v", bucket, eligible)
		}
		if len(inBand) != 0 {
			t.Fatalf("bucket %s: no route may be produced under free exhaustion; got %+v", bucket, inBand)
		}
	}
}

// TestP4Gate_LiteExhaustionIsTypedAndNeverPaid completes the fail-closed gate
// criterion at the LOOP level: when only paid routes exist, Lite's fallback loop
// refuses to execute anything at all and returns the typed exhaustion error
// (05 §1, 05 §2 Step 8.6) — it never "falls back" onto paid to avoid failing.
//
// Mutation M2-L1/L2 (relax the funding rule in FilterEligible or ApplyHardGates)
// → a paid route is executed and the loop succeeds → this test RED.
func TestP4Gate_LiteExhaustionIsTypedAndNeverPaid(t *testing.T) {
	paidOnly := RouteGroup{
		ProviderID: "P1", ProviderModelID: "M1", Funding: accountsdomain.FundingPaid,
		Members: []CandidateOffering{{
			AccountID: "aPaid", ProviderID: "P1", ProviderModelID: "M1",
			Funding: accountsdomain.FundingPaid, AccountHealth: accountsdomain.HealthHealthy,
			QuotaWindows: []quota.Window{availWindow(10_000)},
		}},
	}
	h := &fakeHarness{script: []scriptedAttempt{{outcome: successOutcome(), verdict: VerdictSuccess}}}
	in := baseInput(t, TierLite, paidOnly, h)

	res, err := RunFallbackLoop(context.Background(), in)

	if !errors.Is(err, ErrNoEligibleOffering) {
		t.Fatalf("Lite must fail closed with the typed exhaustion error; got %v", err)
	}
	var exhausted *NoEligibleOfferingError
	if !errors.As(err, &exhausted) {
		t.Fatalf("the exhaustion error must carry *NoEligibleOfferingError; got %T", err)
	}
	if h.execCalls != 0 {
		t.Fatalf("Lite must never execute a paid route; execCalls=%d accounts=%v", h.execCalls, h.executedAccounts)
	}
	if res.Response != nil {
		t.Fatalf("no response may be produced under free exhaustion; got %v", res.Response)
	}
}

// ============================ PRO ===========================================

// TestP4Gate_ProFundingMixConvergesPerBucket is the mechanized Pro convergence
// gate (05 §8.1, 06 Phase 4): over N = 2,000 synthetic successful requests
// spread across the five workload buckets, the realized paid share PER BUCKET
// lands within ±5 pp of the 25% target, the deficit controller never promotes a
// route outside the competitive band, and each bucket keeps an independent
// deficit cell.
//
// Mutation M2-P1: make preferPaidByDeficit ignore the deficit (e.g. always
// return false) → paid share collapses toward 0 → this test RED.
func TestP4Gate_ProFundingMixConvergesPerBucket(t *testing.T) {
	pro := mustPolicy(t, TierPro)
	keyer, err := NewBucketKeyer(DefaultLargeContextThreshold)
	if err != nil {
		t.Fatalf("NewBucketKeyer: %v", err)
	}
	reqsByBucket := gateBucketReqs()
	// A stable, deterministic ordering of the buckets (map order is not stable).
	bucketOrder := []string{"standard", "vision", "tool_use", "structured", "large_context"}

	// Per bucket: one free and one paid in-band group (identical quality, so the
	// ONLY differentiator is the funding deficit controller) plus a weak group
	// that the band must exclude and that must therefore never be selected.
	fleetFor := func() []CandidateOffering {
		return []CandidateOffering{
			gateFullyCertified("pFree", "aFree", "m1", accountsdomain.FundingFree, 90, 500),
			gateFullyCertified("pPaid", "aPaid", "m1", accountsdomain.FundingPaid, 90, 500),
			gateFullyCertified("pWeak", "aWeak", "m1", accountsdomain.FundingFree, 50, 500),
		}
	}

	state := DeficitState{}
	paidByBucket := map[string]int{}
	totalByBucket := map[string]int{}

	for i := 0; i < gateProConvergenceSamples; i++ {
		bucket := bucketOrder[i%len(bucketOrder)]
		reqs := reqsByBucket[bucket]
		if got := keyer.BucketKey(reqs); got != bucket {
			t.Fatalf("bucket key for %q resolved to %q", bucket, got)
		}

		inBand, _ := gatePipeline(t, fleetFor(), reqs, pro)

		var chosen GroupScore
		chosen, state = PreferByDeficit(inBand, bucket, state, gateProPaidShareTarget)

		if chosen.Group.ProviderID == "pWeak" {
			t.Fatalf("bucket %s req %d: an out-of-band weak route was promoted", bucket, i)
		}
		if topQ := gateTopQuality(inBand); topQ-chosen.QualityFactor > pro.BandWidth {
			t.Fatalf("bucket %s: winner %.3f is more than the band %.3f below top %.3f",
				bucket, chosen.QualityFactor, pro.BandWidth, topQ)
		}
		totalByBucket[bucket]++
		if chosen.Group.Funding == accountsdomain.FundingPaid {
			paidByBucket[bucket]++
		}
	}

	for _, bucket := range bucketOrder {
		total := totalByBucket[bucket]
		if total == 0 {
			t.Fatalf("bucket %s received no requests", bucket)
		}
		share := float64(paidByBucket[bucket]) / float64(total)
		if math.Abs(share-gateProPaidShareTarget) > gateProTolerancePP {
			t.Fatalf("bucket %s: realized paid share %.4f is outside ±%.2f of %.2f (paid=%d/%d)",
				bucket, share, gateProTolerancePP, gateProPaidShareTarget, paidByBucket[bucket], total)
		}
	}
}

// TestP4Gate_ProDeficitCellIsolatedPerBucket pins 05 §8.1's load-bearing rule:
// the controller keeps ONE deficit cell PER workload_profile_bucket — "not one
// global deficit". The five-bucket convergence test above cannot catch a globally
// keyed cell: every bucket there sees an identical fleet, so one shared counter
// still converges to ~25% in each of them.
//
// This fixture breaks that symmetry deterministically. Bucket A carries both
// funding classes in band; bucket B carries FREE ONLY. Interleaved A,B,A,B…, a
// GLOBAL cell would be diluted by B's free-only selections, so the controller
// would keep preferring paid in A until the GLOBAL share reached 25% — driving
// A's own realized paid share to ~50%. With correctly isolated cells, A converges
// to 25% regardless of what B does.
//
// Mutation T1-M6: key the cell globally in PreferByDeficit (e.g. `bucketKey = ""`)
// → bucket A's paid share climbs to ~0.50 → this test RED. (Verified: with that
// mutation every other TestP4Gate_* test stays GREEN.)
func TestP4Gate_ProDeficitCellIsolatedPerBucket(t *testing.T) {
	pro := mustPolicy(t, TierPro)
	reqs := Requirements{TextModality: true, ContextTokens: 1_000}

	bothClasses := []CandidateOffering{
		gateFullyCertified("pFree", "aFree", "m1", accountsdomain.FundingFree, 90, 500),
		gateFullyCertified("pPaid", "aPaid", "m1", accountsdomain.FundingPaid, 90, 500),
	}
	freeOnly := []CandidateOffering{
		gateFullyCertified("pFree", "aFree", "m1", accountsdomain.FundingFree, 90, 500),
	}

	state := DeficitState{}
	paidA, totalA := 0, 0
	for i := 0; i < gateProConvergenceSamples; i++ {
		bucket, fleet := "bucketA", bothClasses
		if i%2 == 1 {
			bucket, fleet = "bucketB", freeOnly
		}
		inBand, _ := gatePipeline(t, fleet, reqs, pro)

		var chosen GroupScore
		chosen, state = PreferByDeficit(inBand, bucket, state, gateProPaidShareTarget)

		if bucket == "bucketB" && chosen.Group.Funding == accountsdomain.FundingPaid {
			t.Fatalf("req %d: bucket B has no paid route in band; a paid group was fabricated", i)
		}
		if bucket == "bucketA" {
			totalA++
			if chosen.Group.Funding == accountsdomain.FundingPaid {
				paidA++
			}
		}
	}

	shareA := float64(paidA) / float64(totalA)
	if math.Abs(shareA-gateProPaidShareTarget) > gateProTolerancePP {
		t.Fatalf("bucket A paid share %.4f is outside ±%.2f of %.2f — a neighbouring bucket's traffic leaked into its deficit cell (paid=%d/%d)",
			shareA, gateProTolerancePP, gateProPaidShareTarget, paidA, totalA)
	}
}

// TestP4Gate_ProCompetitiveBandRespectedNoWidening proves Step 6's band is the
// fixed policy width and is never auto-widened (05 §8.5, 06 Phase 4):
//   - a candidate exactly policy.BandWidth below the top is kept (boundary
//     inclusive); one just beyond is dropped;
//   - when fewer than two candidates fall within the band, the ORIGINAL eligible
//     set is returned unchanged — the band is not widened and the runner-up is
//     not silently dropped.
//
// Mutation M2-P2a: widen the band test in ApplyCompetitiveBand (e.g. compare to
// 2*BandWidth) → the just-beyond candidate is kept → the drop assertion RED.
// Mutation M2-P2b: change the `< 2` no-widen guard to always return the kept
// slice → the sub-two-candidate case returns only the top → the no-widen
// assertion RED.
func TestP4Gate_ProCompetitiveBandRespectedNoWidening(t *testing.T) {
	pro := mustPolicy(t, TierPro)
	band := pro.BandWidth

	// (a) top=0.90; one clearly within the band (0.88, 0.02 below) kept; one
	// clearly beyond it (0.80, 0.10 below) dropped. 0.10 sits between the fixed
	// band (0.08) and twice it (0.16), so a widening mutation would wrongly keep
	// the out-of-band candidate. (Exact-boundary inclusivity is proved in
	// band_test.go; a gate must not hinge on float equality at the boundary.)
	if band >= 0.10 {
		t.Fatalf("gate fixture assumes Pro band < 0.10, policy band = %v", band)
	}
	fleet := []CandidateOffering{
		gateFullyCertified("pTop", "aTop", "m1", accountsdomain.FundingFree, 90, 500),
		gateFullyCertified("pEdge", "aEdge", "m2", accountsdomain.FundingFree, 88, 500),
		gateFullyCertified("pOut", "aOut", "m3", accountsdomain.FundingFree, 80, 500),
	}
	inBand, _ := gatePipeline(t, fleet, Requirements{TextModality: true, ContextTokens: 1_000}, pro)

	present := map[string]bool{}
	for _, s := range inBand {
		present[s.Group.ProviderID] = true
	}
	if !present["pTop"] || !present["pEdge"] {
		t.Fatalf("top and the within-band candidate must be in band; got %v", present)
	}
	if present["pOut"] {
		t.Fatalf("a candidate beyond the band must be excluded (no widening); got %v", present)
	}

	// (b) sub-two-candidate: only the top is within band → return the ORIGINAL
	// eligible set unchanged (both groups), never a widened band, never a
	// silently-dropped runner-up.
	fleet2 := []CandidateOffering{
		gateFullyCertified("pHi", "aHi", "m1", accountsdomain.FundingFree, 90, 500),
		gateFullyCertified("pLo", "aLo", "m2", accountsdomain.FundingFree, 50, 500), // 0.40 below → alone out of band
	}
	scored := ScoreGroups(BuildRouteGroups(gateEligible(t, fleet2, pro)), pro)
	got := ApplyCompetitiveBand(scored, pro)
	if len(got) != len(scored) {
		t.Fatalf("sub-two-candidate band must return the original set unchanged; got %d of %d", len(got), len(scored))
	}
}

// gateTopQuality returns the maximum QualityFactor across a scored set.
func gateTopQuality(scores []GroupScore) float64 {
	top := 0.0
	for i, s := range scores {
		if i == 0 || s.QualityFactor > top {
			top = s.QualityFactor
		}
	}
	return top
}

// gateEligible runs Steps 2–3 for a Pro request with a modest context need.
func gateEligible(t *testing.T, fleet []CandidateOffering, policy TierPolicy) []CandidateOffering {
	t.Helper()
	pool := BuildCandidatePool(models.OperationChat, fleet)
	eligible, _, err := ApplyHardGates(pool, Requirements{TextModality: true, ContextTokens: 1_000}, policy)
	if err != nil {
		t.Fatalf("ApplyHardGates: %v", err)
	}
	return eligible
}

// ============================ MAX ===========================================

// TestP4Gate_MaxDRRConvergesToCapacityRatio proves Max's strict-DRR core
// distributes across eligible accounts in proportion to their capacity weight
// (05 §2 Step 7 stage 2): with capacity weights 3:1 the long-run selection
// frequency converges to 0.75 / 0.25.
//
// Mutation M2-M1: make creditEligible credit every account uniformly (ignore
// accountWeight) → the split collapses toward 0.50 / 0.50 → this test RED.
func TestP4Gate_MaxDRRConvergesToCapacityRatio(t *testing.T) {
	big := candWithWeight("big", accountsdomain.FundingFree, 300)
	small := candWithWeight("small", accountsdomain.FundingFree, 100)
	eligible := []CandidateOffering{big, small}

	const rounds = 4_000
	state := DRRState{}
	counts := map[string]int{}
	for i := 0; i < rounds; i++ {
		var chosen CandidateOffering
		chosen, state = DRRRound(eligible, state)
		counts[chosen.AccountID]++
	}

	bigShare := float64(counts["big"]) / float64(rounds)
	if math.Abs(bigShare-0.75) > 0.01 {
		t.Fatalf("DRR big-account share %.4f, want 0.75 ±0.01 (capacity 3:1)", bigShare)
	}
	smallShare := float64(counts["small"]) / float64(rounds)
	if math.Abs(smallShare-0.25) > 0.01 {
		t.Fatalf("DRR small-account share %.4f, want 0.25 ±0.01", smallShare)
	}
}

// TestP4Gate_MaxSkipsAccountSaturatedOnAnyWindow proves an account saturated on
// ANY applicable quota window is never selected while an unsaturated account
// exists (05 §2 Step 7 stage 1, §4): SelectMaxAccount runs SaturationFilter
// first.
//
// Mutation M2-M2: remove the SaturationFilter call in SelectMaxAccount (credit
// every member) → the saturated account is selected → this test RED.
func TestP4Gate_MaxSkipsAccountSaturatedOnAnyWindow(t *testing.T) {
	healthy := candWithWeight("healthy", accountsdomain.FundingFree, 100)
	// saturated has one fresh window (headroom 100) and one STALE window (also
	// headroom 100 but StateStale). Its most-restrictive state is saturated, yet
	// its DRR weight (min headroom across windows) is a NONZERO 100 — so if the
	// SaturationFilter were skipped it would be credited equally with `healthy`
	// and picked ~half the time. A zero-headroom "exhausted" window would weigh 0
	// and never be picked regardless, which would make this test vacuous.
	saturated := CandidateOffering{
		AccountID:     "saturated",
		ProviderID:    "prov",
		AccountHealth: accountsdomain.HealthHealthy,
		Funding:       accountsdomain.FundingFree,
		QuotaWindows:  []quota.Window{availWindow(100), staleWin(100)},
	}
	members := []CandidateOffering{saturated, healthy}

	state := DRRState{}
	for i := 0; i < 50; i++ {
		var chosen CandidateOffering
		var ok bool
		chosen, state, ok = SelectMaxAccount(members, state, 1, drrTestNow, testStale)
		if !ok {
			t.Fatalf("round %d: expected a selection", i)
		}
		if chosen.AccountID == "saturated" {
			t.Fatalf("round %d: an account saturated on one window was selected", i)
		}
	}
}

// TestP4Gate_MaxCompetitiveBandRespected proves Max's tighter band (policy
// value, 0.03) filters a route more than that far below the top (05 §8.5).
//
// Mutation M2-M3: widen the band comparison in ApplyCompetitiveBand → the
// beyond-band route is kept → this test RED.
func TestP4Gate_MaxCompetitiveBandRespected(t *testing.T) {
	max := mustPolicy(t, TierMax)
	band := max.BandWidth // 0.03
	fleet := []CandidateOffering{
		gateFullyCertified("pTop", "aTop", "m1", accountsdomain.FundingFree, 90, 500), // 0.90
		gateFullyCertified("pMid", "aMid", "m2", accountsdomain.FundingFree, 88, 500), // 0.88, diff 0.02 ≤ band
		gateFullyCertified("pOut", "aOut", "m3", accountsdomain.FundingFree, 85, 500), // 0.85, diff 0.05 > band
	}
	inBand, _ := gatePipeline(t, fleet, Requirements{TextModality: true, ContextTokens: 1_000}, max)

	present := map[string]bool{}
	for _, s := range inBand {
		present[s.Group.ProviderID] = true
	}
	if !present["pTop"] || !present["pMid"] {
		t.Fatalf("candidates within %.2f of the top must be in band; got %v", band, present)
	}
	if present["pOut"] {
		t.Fatalf("a candidate %.2f below the top must be excluded by the %.2f band; got %v", 0.05, band, present)
	}
}

// TestP4Gate_MaxAppliesNoFundingTarget proves Max distributes by CAPACITY, never
// toward any fixed funding ratio (05 §8.3, 06 Phase 4). With one free and three
// paid accounts of EQUAL capacity, capacity-fair DRR selects each ~1/4 of the
// time, so the realized free share converges to ~0.25 (its share of accounts) —
// NOT the ~0.50 a 50/50 funding target would force.
//
// Mutation M2-M4: bias creditEligible by funding class (e.g. double a free
// account's credit toward a 50/50 split) → the free share climbs toward 0.50 →
// this test RED.
func TestP4Gate_MaxAppliesNoFundingTarget(t *testing.T) {
	eligible := []CandidateOffering{
		candWithWeight("free1", accountsdomain.FundingFree, 100),
		candWithWeight("paid1", accountsdomain.FundingPaid, 100),
		candWithWeight("paid2", accountsdomain.FundingPaid, 100),
		candWithWeight("paid3", accountsdomain.FundingPaid, 100),
	}

	const rounds = 4_000
	state := DRRState{}
	free := 0
	for i := 0; i < rounds; i++ {
		var chosen CandidateOffering
		chosen, state = DRRRound(eligible, state)
		if chosen.Funding == accountsdomain.FundingFree {
			free++
		}
	}
	freeShare := float64(free) / float64(rounds)
	if math.Abs(freeShare-0.25) > 0.02 {
		t.Fatalf("free share %.4f, want ~0.25 (capacity-fair 1-of-4), not a funding target", freeShare)
	}
	if freeShare > 0.40 {
		t.Fatalf("free share %.4f approaches a 50/50 funding split — Max must apply no funding target", freeShare)
	}
}
