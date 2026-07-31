package routing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

// ErrNoEligibleOffering is the typed sentinel returned when the attempt budget
// is exhausted with no successful attempt (05 §2 Step 8.6). It is a Go value —
// the venom_no_eligible_offering (503) wire envelope is ROUTE-015's job.
// NoEligibleOfferingError wraps this so callers can errors.Is on it while also
// reading the earliest retry_after.
var ErrNoEligibleOffering = errors.New("routing: no eligible offering")

// NoEligibleOfferingError carries the exhaustion detail: how many attempts ran
// and the earliest retry_after observed across the attempts (0 if none).
type NoEligibleOfferingError struct {
	Attempts   int
	RetryAfter time.Duration
}

func (e *NoEligibleOfferingError) Error() string {
	return fmt.Sprintf("%s: exhausted %d attempts (retry_after=%s)", ErrNoEligibleOffering.Error(), e.Attempts, e.RetryAfter)
}

// Unwrap lets errors.Is(err, ErrNoEligibleOffering) match.
func (e *NoEligibleOfferingError) Unwrap() error { return ErrNoEligibleOffering }

// DefaultCooldownWindow is the fallback cooldown length used when a scoped
// failure carries no provider Retry-After hint (05 §3: "else a capped
// default"). It is a computed eligibility input, never a sleep.
const DefaultCooldownWindow = 30 * time.Second

// FallbackInput bundles everything RunFallbackLoop needs: the tier policy and
// the ranked pool snapshot from Step 7, the request identity and selection
// inputs, the estimate input, an injected clock, the current breaker/cooldown/
// provider-evidence state (P4-WIRE-001), and the local ports.
type FallbackInput struct {
	Tier          Tier
	Policy        TierPolicy
	Pool          RoutePool // ranked in-band pool snapshot from Step 7
	Requirements  Requirements
	RequestID     string
	StickinessKey string
	Cache         *StickinessCache
	DRRState      DRRState
	Need          float64
	EstimateInput quota.EstimateInput
	Now           time.Time
	StaleAfter    time.Duration

	// Breakers, Cooldowns, and ProviderEvidence are the persisted routing state
	// (P4-WIRE-001) the loop reads as eligibility inputs and updates as it goes.
	// Nil maps are treated as empty. Their updated values are returned in
	// FallbackResult for the caller to persist (storage's job, not the loop's).
	Breakers         BreakerSet
	Cooldowns        []quota.Cooldown
	ProviderEvidence map[string]*ProviderFailureEvidence

	Recorder    AttemptRecorder
	Reserver    Reserver
	Lifecycle   Lifecycle
	Executor    Executor
	Classifier  FailureClassifier
	ReEvaluator PoolReEvaluator
	Minter      AttemptIDMinter
	// Scoper enables the ROUTE-014 scope-classified fallback (breaker trips,
	// cross-offering / skip-provider steering, bounded transient retry). When
	// nil the loop runs the ROUTE-013 verdict-only path unchanged.
	Scoper FailureScoper
}

// FallbackResult is the loop's outcome. On success Response/AccountID/ProviderID/
// ReservationID identify the served route; on any return the Breakers,
// Cooldowns, and TransientBackoffs carry the routing-state deltas the caller
// persists (P4-WIRE-001) and the computed transient backoffs (never slept).
type FallbackResult struct {
	Response          any
	AccountID         string
	ProviderID        string
	ReservationID     string
	Attempts          int
	Breakers          BreakerSet
	Cooldowns         []quota.CooldownTrigger
	TransientBackoffs []time.Duration

	// ThinkingApplied / ThinkingTierClamped / ThinkingCertifiedClamped carry the
	// NormalizeThinking decision for the SERVED attempt (P5-PAPI-004), so the
	// composition layer can report the applied level and each clamp on the
	// X-Venom-* headers and the route-decision row. They are populated ONLY on a
	// successful attempt; the zero value (ThinkingNone, false, false) reproduces
	// the pre-P5 behavior exactly and nothing in routing consumes them.
	ThinkingApplied          ThinkingLevel
	ThinkingTierClamped      bool
	ThinkingCertifiedClamped bool
}

// RunFallbackLoop runs Step 8's per-attempt reserve→execute→reconcile cycle
// (05 §2 Step 8) for up to Policy.AttemptBudget attempts.
//
// THE CROWN INVARIANT: no attempt executes before its reservation succeeds, and
// every executed attempt takes exactly one terminal reconcile branch — settle /
// settle-estimate / release / mark-reconciliation-pending — never zero, never
// two. Fail closed: an unknown/network-cut outcome marks reconciliation_pending
// (headroom stays debited), never releases.
//
// Per attempt:
//  1. select a candidate over the current snapshot via SelectAccount;
//  2. record the attempt identity (content-free) before reserving;
//  3. estimate + reserve atomically. On ErrReservationRejected NOTHING was
//     debited, so DO NOT release — re-evaluate the pool from a fresh snapshot
//     and continue (this attempt consumed one budget slot, never executed);
//  4. mark dispatched, THEN execute (nothing reservation-mutating in flight);
//  5. reconcile on exactly one branch: success iff outcome.Err == nil (never the
//     classifier's word, whose zero value is VerdictSuccess), otherwise by
//     verdict, with any unrecognized verdict failing closed to pending.
//
// On success the stickiness pin is recorded (05 §2 Step 7: "recorded only on a
// successful response") — this loop is the caller P4-ROUTE-012 delegated that to.
//
// On request-scope the loop stops and returns the failure. On exhaustion it
// returns a *NoEligibleOfferingError (wrapping ErrNoEligibleOffering) carrying
// the earliest retry_after observed. Each attempt's reservation id comes from
// quota.ReservationID(requestID, attemptID); none is ever inherited.
//
// Streaming boundary (05 §3; docs/11 risk R-12): a FAILED attempt whose
// outcome.StreamStarted is true stops the loop after its terminal reconcile op
// and its scope-classified steering have run — fallback may happen only before
// the first byte reaches the client, so once streaming has begun no further
// attempt runs and no second response is ever produced.
func RunFallbackLoop(ctx context.Context, in FallbackInput) (FallbackResult, error) {
	st := loopState{
		pool:             in.Pool,
		drrState:         in.DRRState,
		breakers:         cloneBreakerSet(in.Breakers),
		activeCooldowns:  append([]quota.Cooldown(nil), in.Cooldowns...),
		evidence:         in.ProviderEvidence,
		dropped:          map[string]bool{},
		excluded:         map[string]bool{},
		transientRetries: map[string]int{},
	}
	if st.evidence == nil {
		st.evidence = map[string]*ProviderFailureEvidence{}
	}

	for i := 1; i <= in.Policy.AttemptBudget; i++ {
		elig := EligibilityInput{
			Funding:              in.Policy.Funding,
			RequiredCapabilities: in.Requirements.Capabilities,
			Cooldowns:            st.activeCooldowns,
			Breakers:             st.breakers,
		}

		// 1. Narrow the pool with ROUTE-014's eligibility filter, then select a
		//    candidate within the top eligible group. A filtered-out candidate
		//    (funding/capability boundary, active cooldown, open breaker) is
		//    never attempted.
		group, members, gok := currentEligibleGroup(st.pool, st.dropped, st.excluded, elig, in.Now)
		if !gok {
			break
		}
		chosen, nextDRR, ok := SelectAccount(in.Tier, RouteGroup{
			ProviderID:      group.ProviderID,
			ProviderModelID: group.ProviderModelID,
			Funding:         group.Funding,
			Members:         members,
		}, in.StickinessKey, in.Cache, st.drrState, in.Need, in.Now, in.StaleAfter)
		st.drrState = nextDRR
		if !ok {
			break
		}
		st.attempts = i

		attemptID := in.Minter.MintAttemptID(in.RequestID, i)
		reservationID, err := quota.ReservationID(in.RequestID, attemptID)
		if err != nil {
			return st.result(), err
		}

		// 2. Record the attempt identity (no content) BEFORE reserving.
		if err := in.Recorder.RecordAttempt(ctx, AttemptRecord{
			RequestID:       in.RequestID,
			AttemptID:       attemptID,
			AccountID:       chosen.AccountID,
			ProviderModelID: chosen.ProviderModelID,
		}); err != nil {
			return st.result(), err
		}

		// 3. Estimate + reserve atomically across every applicable window.
		allocations, err := quota.Estimate(in.EstimateInput, quota.DefaultEstimatePolicy())
		if err != nil {
			return st.result(), err
		}
		if _, err := in.Reserver.Reserve(ctx, ReserveParams{
			ReservationID: reservationID,
			AccountID:     chosen.AccountID,
			RequestID:     in.RequestID,
			AttemptID:     attemptID,
			Allocations:   allocations,
		}); err != nil {
			if errors.Is(err, ErrReservationRejected) {
				// Nothing debited → do NOT release. Re-evaluate a fresh pool.
				fresh, rerr := in.ReEvaluator.ReEvaluate(ctx)
				if rerr != nil {
					return st.result(), rerr
				}
				st.pool = fresh
				continue
			}
			return st.result(), err
		}

		// 4. Mark dispatched, THEN execute (no reservation-mutating call in flight).
		if err := in.Lifecycle.MarkDispatched(ctx, reservationID); err != nil {
			return st.result(), err
		}
		// This attempt is the half-open trial for every applicable scope that is
		// currently half-open, so mark those probes in flight BEFORE executing.
		// Without this the persisted breaker keeps ProbeInFlight=false and
		// Admits() returns true for every concurrent caller — a recovering scope
		// would be hammered instead of probed once (05 §3).
		if in.Scoper != nil {
			st.markHalfOpenProbes(chosen, in.Now)
		}
		outcome := in.Executor.Execute(ctx, ResolvedAttempt{
			RequestID:     in.RequestID,
			AttemptID:     attemptID,
			ReservationID: reservationID,
			Candidate:     chosen,
			Requirements:  in.Requirements,
		})
		st.observeRetryAfter(outcome.RetryAfter)

		// 5. Reconcile on exactly one branch. Success is decided SOLELY by
		//    outcome.Err == nil, never by the classifier (whose zero value is
		//    VerdictSuccess — trusting it would settle a FAILED attempt).
		if outcome.Err == nil {
			if err := settleOutcome(ctx, in.Lifecycle, reservationID, outcome); err != nil {
				return st.result(), err
			}
			if in.Scoper != nil {
				// A success closes — and resets the adaptive backoff of — the
				// breaker for EVERY scope this route just proved healthy: the
				// account, the offering, AND the provider. Closing only the
				// account would leave a tripped offering/provider breaker stuck
				// half-open forever with its inflated backoff never reset
				// (05 §3: "success closes the breaker and resets the backoff").
				st.recordScopeSuccess(chosen)
			}
			// The stickiness pin is recorded ONLY on a successful response
			// (05 §2 Step 7) — this loop is the caller ROUTE-012 delegated it to.
			if in.Cache != nil && in.StickinessKey != "" {
				in.Cache.Pin(in.StickinessKey, chosen.AccountID, in.Now)
			}
			res := st.result()
			res.Response = outcome.Response
			res.AccountID = chosen.AccountID
			res.ProviderID = chosen.ProviderID
			res.ReservationID = reservationID
			// The thinking decision for the SERVED candidate (05 §1a), re-clamped
			// against THIS offering's certified maximum — so the header/decision
			// report the level actually driven, not the requested one.
			thinking := NormalizeThinking(
				in.Requirements.RequestedThinking,
				hasCapability(in.Requirements.Capabilities, CapabilityReasoning),
				in.Policy,
				ThinkingCandidate{ReasoningCertified: chosen.ReasoningCertified, CertifiedMax: chosen.ReasoningCertifiedMax},
			)
			res.ThinkingApplied = thinking.Applied
			res.ThinkingTierClamped = thinking.TierClamped
			res.ThinkingCertifiedClamped = thinking.CertifiedClamped
			return res, nil
		}

		verdict := in.Classifier.Classify(outcome.Err)
		switch verdict {
		case VerdictPreConsumptionFailure:
			if err := in.Lifecycle.Release(ctx, reservationID); err != nil {
				return st.result(), err
			}
		case VerdictPartialConsumption:
			if err := settleOutcome(ctx, in.Lifecycle, reservationID, outcome); err != nil {
				return st.result(), err
			}
		case VerdictUnknownConsumption:
			if err := in.Lifecycle.MarkReconciliationPending(ctx, reservationID); err != nil {
				return st.result(), err
			}
		case VerdictRequestScope:
			if err := in.Lifecycle.Release(ctx, reservationID); err != nil {
				return st.result(), err
			}
			return st.result(), outcome.Err
		default:
			// Unrecognized verdict — INCLUDING VerdictSuccess — fails closed to
			// reconciliation_pending (headroom stays debited), never charging or
			// freeing on a guess.
			if err := in.Lifecycle.MarkReconciliationPending(ctx, reservationID); err != nil {
				return st.result(), err
			}
		}

		// 6. Scope-classified steering (ROUTE-014), only when a scoper is wired.
		//    The reconcile op above already ran exactly once; this only decides
		//    what to try NEXT and which breaker/cooldown to record.
		if in.Scoper != nil {
			if stop := st.applyScopeAction(in.Scoper.ScopeOf(outcome.Err), chosen, in.Now, outcome.RetryAfter); stop {
				return st.result(), outcome.Err
			}
		}

		// 7. Streaming first-byte boundary (05 §3: "fallback only before the first
		//    byte reaches the client; never emit a second response after streaming
		//    has begun"; docs/11 risk R-12). A mid-stream failure is real failure
		//    evidence, so the terminal reconcile op (step 5) and the scope-steering
		//    (step 6) above have already recorded the settlement, breaker trip, and
		//    cooldown for this scope. But NO further attempt may run — a second
		//    route would produce a second response after the client already began
		//    receiving one. Stop regardless of the scope action the scoper produced.
		if outcome.StreamStarted {
			return st.result(), outcome.Err
		}
	}

	return st.result(), &NoEligibleOfferingError{Attempts: st.attempts, RetryAfter: st.earliestRetry}
}

// loopState is RunFallbackLoop's mutable per-request state.
type loopState struct {
	pool             RoutePool
	drrState         DRRState
	breakers         BreakerSet
	activeCooldowns  []quota.Cooldown
	evidence         map[string]*ProviderFailureEvidence
	dropped          map[string]bool // provider ids skipped for this request
	excluded         map[string]bool // candidate keys spent (transient-exhausted)
	transientRetries map[string]int
	emitted          []quota.CooldownTrigger
	backoffs         []time.Duration
	earliestRetry    time.Duration
	haveRetry        bool
	attempts         int
}

func (st *loopState) observeRetryAfter(d time.Duration) {
	if d > 0 && (!st.haveRetry || d < st.earliestRetry) {
		st.earliestRetry = d
		st.haveRetry = true
	}
}

// result snapshots the routing-state deltas returned on every exit path. The
// exhaustion retry_after travels on NoEligibleOfferingError, not here.
func (st *loopState) result() FallbackResult {
	return FallbackResult{
		Attempts:          st.attempts,
		Breakers:          st.breakers,
		Cooldowns:         st.emitted,
		TransientBackoffs: st.backoffs,
	}
}

// markHalfOpenProbes marks the in-flight half-open probe for each of the chosen
// candidate's three scopes that is currently half-open. The attempt about to run
// IS that scope's single trial request (05 §3: "half-open probes"), so the
// breaker must carry ProbeInFlight=true from here on — otherwise Admits() keeps
// returning true and a recovering scope admits unlimited concurrent probes.
// Trip and RecordSuccess both clear the flag, so it never leaks past the attempt.
func (st *loopState) markHalfOpenProbes(chosen CandidateOffering, now time.Time) {
	if b := lookupBreaker(st.breakers.Account, chosen.AccountID); b.EffectiveState(now) == BreakerHalfOpen {
		st.breakers.Account[chosen.AccountID] = b.MarkProbe()
	}
	if b := lookupBreaker(st.breakers.Offering, chosen.ProviderModelID); b.EffectiveState(now) == BreakerHalfOpen {
		st.breakers.Offering[chosen.ProviderModelID] = b.MarkProbe()
	}
	if b := lookupBreaker(st.breakers.Provider, chosen.ProviderID); b.EffectiveState(now) == BreakerHalfOpen {
		st.breakers.Provider[chosen.ProviderID] = b.MarkProbe()
	}
}

// recordScopeSuccess closes and resets the breaker for every scope a successful
// route just proved healthy — account, offering, and provider. A success on this
// route is positive evidence for all three, and closing only one would leave the
// others permanently half-open with an inflated, never-reset backoff.
func (st *loopState) recordScopeSuccess(chosen CandidateOffering) {
	st.breakers.Account[chosen.AccountID] = lookupBreaker(st.breakers.Account, chosen.AccountID).RecordSuccess()
	st.breakers.Offering[chosen.ProviderModelID] = lookupBreaker(st.breakers.Offering, chosen.ProviderModelID).RecordSuccess()
	st.breakers.Provider[chosen.ProviderID] = lookupBreaker(st.breakers.Provider, chosen.ProviderID).RecordSuccess()
}

// applyScopeAction honors ROUTE-014's ResolveScope verdict for a failed attempt.
// It returns stop=true only for the request-scope action. Breaker trips and
// cooldown emissions are recorded into the loop state (returned as values).
func (st *loopState) applyScopeAction(scope FallbackScope, chosen CandidateOffering, now time.Time, retryAfter time.Duration) (stop bool) {
	crossAccount := false
	if scope == ScopeProvider {
		ev := st.evidence[chosen.ProviderID]
		if ev == nil {
			ev = NewProviderFailureEvidence()
			st.evidence[chosen.ProviderID] = ev
		}
		ev.Observe(chosen.AccountID)
		crossAccount = ev.CrossAccount()
	}

	res := ResolveScope(scope, crossAccount)
	switch res.Action {
	case ActionStop:
		return true
	case ActionBoundedRetry:
		// Bounded retry of the SAME candidate: at most TransientMaxRetries total
		// tries. Count this try; if more are allowed, record the (computed, never
		// slept) backoff for the next one; otherwise give up on this candidate
		// for the rest of the request.
		key := candKey(chosen)
		st.transientRetries[key]++
		if st.transientRetries[key] < TransientMaxRetries {
			st.backoffs = append(st.backoffs, TransientBackoff(st.transientRetries[key]))
		} else {
			st.excluded[key] = true
		}
	case ActionNextAccount:
		st.breakers.Account[chosen.AccountID] = lookupBreaker(st.breakers.Account, chosen.AccountID).Trip(now)
		st.emitCooldown(res, chosen, now, retryAfter)
	case ActionNextOffering:
		st.breakers.Offering[chosen.ProviderModelID] = lookupBreaker(st.breakers.Offering, chosen.ProviderModelID).Trip(now)
		st.emitCooldown(res, chosen, now, retryAfter)
	case ActionSkipProvider:
		// Skip every route for this provider for the REST of the request,
		// regardless of cross-account evidence.
		st.dropped[chosen.ProviderID] = true
		// The persisted provider breaker + cooldown trip only on cross-account
		// evidence (05 §3's load-bearing rule).
		if crossAccount {
			st.breakers.Provider[chosen.ProviderID] = lookupBreaker(st.breakers.Provider, chosen.ProviderID).Trip(now)
			st.emitCooldown(res, chosen, now, retryAfter)
		}
	}
	return false
}

// emitCooldown records a scoped cooldown as both an active eligibility input
// (so the just-failed scope is skipped for the rest of this request) and a
// returned CooldownTrigger value (for the caller to persist).
func (st *loopState) emitCooldown(res ScopeResolution, chosen CandidateOffering, now time.Time, retryAfter time.Duration) {
	if !res.Cooldown {
		return
	}
	ct := cooldownTrigger(res.CooldownScope, chosen, now, retryAfter)
	st.emitted = append(st.emitted, ct)
	st.activeCooldowns = append(st.activeCooldowns, cooldownFromTrigger(ct))
}

// currentEligibleGroup returns the top-ranked group that still has at least one
// eligible, non-excluded, non-dropped-provider candidate after FilterEligible.
func currentEligibleGroup(pool RoutePool, dropped, excluded map[string]bool, elig EligibilityInput, now time.Time) (RouteGroup, []CandidateOffering, bool) {
	for _, g := range pool.Groups {
		if dropped[g.ProviderID] {
			continue
		}
		var kept []CandidateOffering
		for _, c := range FilterEligible(g.Members, elig, now) {
			if excluded[candKey(c)] {
				continue
			}
			kept = append(kept, c)
		}
		if len(kept) > 0 {
			return g, kept, true
		}
	}
	return RouteGroup{}, nil, false
}

// cloneBreakerSet deep-copies a BreakerSet (with non-nil maps) so the loop can
// mutate breaker state without touching the caller's input.
func cloneBreakerSet(in BreakerSet) BreakerSet {
	out := BreakerSet{
		Account:  map[string]Breaker{},
		Offering: map[string]Breaker{},
		Provider: map[string]Breaker{},
	}
	for k, v := range in.Account {
		out.Account[k] = v
	}
	for k, v := range in.Offering {
		out.Offering[k] = v
	}
	for k, v := range in.Provider {
		out.Provider[k] = v
	}
	return out
}

// lookupBreaker returns m[key], or a fresh zero (closed-equivalent, cycle 0)
// breaker when absent — Trip/RecordSuccess both start correctly from zero.
func lookupBreaker(m map[string]Breaker, key string) Breaker {
	return m[key]
}

// cooldownTrigger builds a scope-correct quota.CooldownTrigger (quota's own
// vocabulary) from a scoped failure. The until is the provider Retry-After when
// present, else DefaultCooldownWindow.
func cooldownTrigger(scope quota.CooldownScope, c CandidateOffering, now time.Time, retryAfter time.Duration) quota.CooldownTrigger {
	dur := DefaultCooldownWindow
	src := quota.CooldownSourceDefaultBackoff
	if retryAfter > 0 {
		dur = retryAfter
		src = quota.CooldownSourceRetryAfter
	}
	var ref string
	switch scope {
	case quota.CooldownScopeAccount:
		ref = c.AccountID
	case quota.CooldownScopeOffering:
		ref = c.ProviderModelID
	case quota.CooldownScopeProvider:
		ref = c.ProviderID
	}
	return quota.CooldownTrigger{
		Scope:      scope,
		ScopeRef:   ref,
		Until:      now.Add(dur),
		Source:     src,
		ReasonCode: "fallback_" + string(scope),
	}
}

// cooldownFromTrigger projects a CooldownTrigger into a quota.Cooldown so the
// just-emitted cooldown feeds FilterEligible within the same request.
func cooldownFromTrigger(ct quota.CooldownTrigger) quota.Cooldown {
	ref := ct.ScopeRef
	cd := quota.Cooldown{
		Scope:      ct.Scope,
		Until:      ct.Until,
		Source:     ct.Source,
		ReasonCode: ct.ReasonCode,
	}
	switch ct.Scope {
	case quota.CooldownScopeAccount:
		cd.AccountID = &ref
	case quota.CooldownScopeOffering:
		cd.OfferingOperationID = &ref
	case quota.CooldownScopeProvider:
		cd.ProviderID = &ref
	}
	return cd
}

// settleOutcome converts a reservation from reserved to consumed: at the
// provider-reported actuals when known, otherwise at the reserved estimate. It
// performs exactly ONE lifecycle call and is used by the success and partial
// branches.
func settleOutcome(ctx context.Context, lc Lifecycle, reservationID string, o ExecOutcome) error {
	if len(o.ActualCost) > 0 {
		return lc.Settle(ctx, reservationID, o.ActualCost)
	}
	return lc.SettleEstimate(ctx, reservationID)
}
