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

// FallbackInput bundles everything RunFallbackLoop needs: the tier policy and
// initial winning-group snapshot, the request identity and selection inputs,
// the estimate input, an injected clock, and the seven local ports.
type FallbackInput struct {
	Tier          Tier
	Policy        TierPolicy
	Group         RouteGroup // initial winning-group snapshot from Step 7
	Requirements  Requirements
	RequestID     string
	StickinessKey string
	Cache         *StickinessCache
	DRRState      DRRState
	Need          float64
	EstimateInput quota.EstimateInput
	Now           time.Time
	StaleAfter    time.Duration

	Recorder    AttemptRecorder
	Reserver    Reserver
	Lifecycle   Lifecycle
	Executor    Executor
	Classifier  FailureClassifier
	ReEvaluator PoolReEvaluator
	Minter      AttemptIDMinter
}

// FallbackResult is the successful-route outcome.
type FallbackResult struct {
	Response      any
	AccountID     string
	ReservationID string
	Attempts      int
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
func RunFallbackLoop(ctx context.Context, in FallbackInput) (FallbackResult, error) {
	group := in.Group
	drrState := in.DRRState

	var earliestRetry time.Duration
	haveRetry := false
	attempts := 0

	for i := 1; i <= in.Policy.AttemptBudget; i++ {
		// 1. Select a candidate over the CURRENT snapshot.
		chosen, nextDRR, ok := SelectAccount(in.Tier, group, in.StickinessKey, in.Cache, drrState, in.Need, in.Now, in.StaleAfter)
		drrState = nextDRR
		if !ok {
			// No candidate in this snapshot — treat as exhausted.
			break
		}
		attempts = i

		attemptID := in.Minter.MintAttemptID(in.RequestID, i)
		reservationID, err := quota.ReservationID(in.RequestID, attemptID)
		if err != nil {
			return FallbackResult{}, err
		}

		// 2. Record the attempt identity (no content) BEFORE reserving.
		if err := in.Recorder.RecordAttempt(ctx, AttemptRecord{
			RequestID:       in.RequestID,
			AttemptID:       attemptID,
			AccountID:       chosen.AccountID,
			ProviderModelID: chosen.ProviderModelID,
		}); err != nil {
			return FallbackResult{}, err
		}

		// 3. Estimate + reserve atomically across every applicable window.
		allocations, err := quota.Estimate(in.EstimateInput, quota.DefaultEstimatePolicy())
		if err != nil {
			return FallbackResult{}, err
		}
		if _, err := in.Reserver.Reserve(ctx, ReserveParams{
			ReservationID: reservationID,
			AccountID:     chosen.AccountID,
			RequestID:     in.RequestID,
			AttemptID:     attemptID,
			Allocations:   allocations,
		}); err != nil {
			if errors.Is(err, ErrReservationRejected) {
				// Nothing debited → do NOT release. Re-evaluate a fresh snapshot.
				fresh, rerr := in.ReEvaluator.ReEvaluate(ctx)
				if rerr != nil {
					return FallbackResult{}, rerr
				}
				group = fresh
				continue
			}
			return FallbackResult{}, err
		}

		// 4. Mark dispatched, THEN execute (no reservation-mutating call in flight).
		if err := in.Lifecycle.MarkDispatched(ctx, reservationID); err != nil {
			return FallbackResult{}, err
		}
		outcome := in.Executor.Execute(ctx, ResolvedAttempt{
			RequestID:     in.RequestID,
			AttemptID:     attemptID,
			ReservationID: reservationID,
			Candidate:     chosen,
			Requirements:  in.Requirements,
		})
		if outcome.RetryAfter > 0 && (!haveRetry || outcome.RetryAfter < earliestRetry) {
			earliestRetry = outcome.RetryAfter
			haveRetry = true
		}

		// 5. Reconcile on exactly one branch.
		//
		// Success is decided SOLELY by outcome.Err == nil, never by the
		// classifier. VerdictSuccess is ReconcileVerdict's zero value, so a
		// classifier or adapter with a forgotten switch case returns it by
		// accident — and trusting that would settle a FAILED attempt's quota as
		// consumed and hand the caller the failure body with a nil error. The
		// classifier is consulted only for a genuine error, and a VerdictSuccess
		// answer there is treated as unrecognized (fail closed, below).
		if outcome.Err == nil {
			if err := settleOutcome(ctx, in.Lifecycle, reservationID, outcome); err != nil {
				return FallbackResult{}, err
			}
			// The stickiness pin is recorded ONLY on a successful response
			// (05 §2 Step 7). This loop is the "caller" P4-ROUTE-012 delegated
			// pinning to — SelectAccount deliberately never pins, so without
			// this the LRU would never be populated and stickiness would be
			// inert in production.
			if in.Cache != nil && in.StickinessKey != "" {
				in.Cache.Pin(in.StickinessKey, chosen.AccountID, in.Now)
			}
			return FallbackResult{Response: outcome.Response, AccountID: chosen.AccountID, ReservationID: reservationID, Attempts: i}, nil
		}

		switch in.Classifier.Classify(outcome.Err) {
		case VerdictPreConsumptionFailure:
			if err := in.Lifecycle.Release(ctx, reservationID); err != nil {
				return FallbackResult{}, err
			}
		case VerdictPartialConsumption:
			// Settle the provider-reported known cost; the remainder is freed.
			if err := settleOutcome(ctx, in.Lifecycle, reservationID, outcome); err != nil {
				return FallbackResult{}, err
			}
		case VerdictUnknownConsumption:
			// Headroom stays debited — never release on an unknown outcome.
			if err := in.Lifecycle.MarkReconciliationPending(ctx, reservationID); err != nil {
				return FallbackResult{}, err
			}
		case VerdictRequestScope:
			// The request itself is bad (nothing consumed): free and stop.
			if err := in.Lifecycle.Release(ctx, reservationID); err != nil {
				return FallbackResult{}, err
			}
			return FallbackResult{}, outcome.Err
		default:
			// Unrecognized verdict — INCLUDING VerdictSuccess, which a classifier
			// must never return for a real error — fails closed to
			// reconciliation_pending: headroom stays debited and the loop
			// continues, rather than charging or freeing on a guess.
			if err := in.Lifecycle.MarkReconciliationPending(ctx, reservationID); err != nil {
				return FallbackResult{}, err
			}
		}
	}

	retry := time.Duration(0)
	if haveRetry {
		retry = earliestRetry
	}
	return FallbackResult{Attempts: attempts}, &NoEligibleOfferingError{Attempts: attempts, RetryAfter: retry}
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
