package routing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

// --- scripted fake driving every port from one shared harness ---------------

// scriptedAttempt is one attempt's programmed behavior.
type scriptedAttempt struct {
	reserveRejected bool             // Reserve returns ErrReservationRejected
	outcome         ExecOutcome      // Executor result (used only if reserve ok)
	verdict         ReconcileVerdict // Classify result for outcome.Err (if non-nil)
}

// fakeHarness implements every routing port and records call order + counts so
// the loop's control flow is fully observable with no real I/O.
type fakeHarness struct {
	script []scriptedAttempt
	groups []RouteGroup // fresh snapshots returned by successive ReEvaluate calls

	order []string

	recordCalls    int
	reserveCalls   int
	reserveRejects int
	execCalls      int
	settle         int
	settleEstimate int
	release        int
	markPending    int
	markDispatched int
	reEvalCalls    int

	reservationIDs   []string
	recorded         []AttemptRecord
	executedAccounts []string
	settleActuals    []map[quota.Unit]float64

	reservedForCurrent            bool
	reserveBeforeExecuteViolation bool
}

func (h *fakeHarness) cur() int { return h.recordCalls - 1 }

func (h *fakeHarness) RecordAttempt(_ context.Context, rec AttemptRecord) error {
	h.recordCalls++
	h.reservedForCurrent = false
	h.recorded = append(h.recorded, rec)
	h.order = append(h.order, "record")
	return nil
}

func (h *fakeHarness) Reserve(_ context.Context, params ReserveParams) (ReserveResult, error) {
	h.reserveCalls++
	if h.script[h.cur()].reserveRejected {
		h.reserveRejects++
		h.order = append(h.order, "reserve_rejected")
		return ReserveResult{}, fmt.Errorf("window empty: %w", ErrReservationRejected)
	}
	h.reservedForCurrent = true
	h.reservationIDs = append(h.reservationIDs, params.ReservationID)
	h.order = append(h.order, "reserve_ok")
	return ReserveResult{ReservationID: params.ReservationID}, nil
}

func (h *fakeHarness) MarkDispatched(_ context.Context, _ string) error {
	h.markDispatched++
	h.order = append(h.order, "mark_dispatched")
	return nil
}

func (h *fakeHarness) Execute(_ context.Context, attempt ResolvedAttempt) ExecOutcome {
	h.execCalls++
	if !h.reservedForCurrent {
		h.reserveBeforeExecuteViolation = true
	}
	h.executedAccounts = append(h.executedAccounts, attempt.Candidate.AccountID)
	h.order = append(h.order, "execute")
	return h.script[h.cur()].outcome
}

func (h *fakeHarness) Classify(_ error) ReconcileVerdict { return h.script[h.cur()].verdict }

func (h *fakeHarness) Settle(_ context.Context, _ string, actuals map[quota.Unit]float64) error {
	h.settle++
	h.settleActuals = append(h.settleActuals, actuals)
	h.order = append(h.order, "settle")
	return nil
}

func (h *fakeHarness) SettleEstimate(_ context.Context, _ string) error {
	h.settleEstimate++
	h.order = append(h.order, "settle_estimate")
	return nil
}

func (h *fakeHarness) Release(_ context.Context, _ string) error {
	h.release++
	h.order = append(h.order, "release")
	return nil
}

func (h *fakeHarness) MarkReconciliationPending(_ context.Context, _ string) error {
	h.markPending++
	h.order = append(h.order, "mark_pending")
	return nil
}

func (h *fakeHarness) ReEvaluate(_ context.Context) (RouteGroup, error) {
	h.reEvalCalls++
	h.order = append(h.order, "reeval")
	if h.reEvalCalls-1 < len(h.groups) {
		return h.groups[h.reEvalCalls-1], nil
	}
	return RouteGroup{}, nil
}

func (h *fakeHarness) MintAttemptID(requestID string, attemptNumber int) string {
	return fmt.Sprintf("%s#att%d", requestID, attemptNumber)
}

// --- helpers ---------------------------------------------------------------

func loopGroup(accountIDs ...string) RouteGroup {
	members := make([]CandidateOffering, len(accountIDs))
	for i, id := range accountIDs {
		members[i] = fairCand(id, freshWin(10_000, 0)) // healthy, available, not saturated
	}
	return RouteGroup{Members: members}
}

func iptr(v int) *int { return &v }

func mustPolicy(t *testing.T, tier Tier) TierPolicy {
	t.Helper()
	ps, err := Policies()
	if err != nil {
		t.Fatalf("Policies: %v", err)
	}
	return ps[tier]
}

// baseInput wires a harness into a FallbackInput for the given tier/group.
func baseInput(t *testing.T, tier Tier, group RouteGroup, h *fakeHarness) FallbackInput {
	t.Helper()
	return FallbackInput{
		Tier:          tier,
		Policy:        mustPolicy(t, tier),
		Group:         group,
		Requirements:  Requirements{TextModality: true, ContextTokens: 100},
		RequestID:     "req-1",
		StickinessKey: "",
		Cache:         nil,
		DRRState:      DRRState{},
		Need:          1,
		EstimateInput: quota.EstimateInput{InputTokens: iptr(10), MaxOutputTokens: iptr(20)},
		Now:           drrTestNow,
		StaleAfter:    testStale,
		Recorder:      h,
		Reserver:      h,
		Lifecycle:     h,
		Executor:      h,
		Classifier:    h,
		ReEvaluator:   h,
		Minter:        h,
	}
}

func successOutcome() ExecOutcome {
	return ExecOutcome{Response: "ok", ActualCost: map[quota.Unit]float64{quota.UnitOutputTokens: 12}}
}

var errRetryable = errors.New("retryable provider failure")

func retryableUnknown() scriptedAttempt {
	return scriptedAttempt{outcome: ExecOutcome{Err: errRetryable}, verdict: VerdictUnknownConsumption}
}

// --- TESTS -----------------------------------------------------------------

// TestRunFallbackLoop_ReserveBeforeExecute proves the executor never fires when
// the reservation is rejected — no attempt executes before a successful reserve.
//
// Mutation row F-M1: execute before reserving → executor fires on the rejected
// path → this test RED (execCalls != 0 / reserveBeforeExecuteViolation).
func TestRunFallbackLoop_ReserveBeforeExecute(t *testing.T) {
	h := &fakeHarness{
		script: []scriptedAttempt{
			{reserveRejected: true}, {reserveRejected: true},
			{reserveRejected: true}, {reserveRejected: true},
		},
		groups: []RouteGroup{loopGroup("B"), loopGroup("C"), loopGroup("D"), loopGroup("E")},
	}
	in := baseInput(t, TierPro, loopGroup("A"), h) // Pro budget = 4

	_, err := RunFallbackLoop(context.Background(), in)
	if !errors.Is(err, ErrNoEligibleOffering) {
		t.Fatalf("expected exhaustion sentinel, got %v", err)
	}
	if h.execCalls != 0 {
		t.Fatalf("executor fired %d times on rejected-reservation path, want 0", h.execCalls)
	}
	if h.reserveBeforeExecuteViolation {
		t.Fatalf("executor ran before a successful reserve")
	}
	if h.release != 0 {
		t.Fatalf("nothing was debited on rejection; release must be 0, got %d", h.release)
	}
}

// TestRunFallbackLoop_RejectedReservationReEvaluates proves that on a typed
// rejection the pool is re-evaluated and the next attempt targets the FRESH
// snapshot's account, with no release (nothing was debited).
//
// Mutation row F-M7: reuse the stale snapshot instead of re-evaluating → the
// second attempt targets "A" again → this test RED.
// Mutation row F-M2: call release on the rejected path → release != 0 → RED.
func TestRunFallbackLoop_RejectedReservationReEvaluates(t *testing.T) {
	h := &fakeHarness{
		script: []scriptedAttempt{
			{reserveRejected: true},                              // attempt 1: rejected
			{outcome: successOutcome(), verdict: VerdictSuccess}, // attempt 2: ok → success
		},
		groups: []RouteGroup{loopGroup("B")}, // fresh snapshot after the rejection
	}
	in := baseInput(t, TierPro, loopGroup("A"), h)

	_, err := RunFallbackLoop(context.Background(), in)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if h.reEvalCalls != 1 {
		t.Fatalf("expected exactly 1 re-evaluation, got %d", h.reEvalCalls)
	}
	if len(h.executedAccounts) != 1 || h.executedAccounts[0] != "B" {
		t.Fatalf("second attempt must target fresh snapshot account 'B', executed=%v", h.executedAccounts)
	}
	if h.release != 0 {
		t.Fatalf("rejected reservation debited nothing; release must be 0, got %d", h.release)
	}
}

// TestRunFallbackLoop_PerAttemptDistinctReservationIDs proves each attempt gets
// a distinct reservation id equal to quota.ReservationID(requestID, attemptID).
//
// Mutation row F-M3: reuse attempt 1's reservation id for all attempts → ids
// collide → this test RED.
func TestRunFallbackLoop_PerAttemptDistinctReservationIDs(t *testing.T) {
	h := &fakeHarness{script: []scriptedAttempt{
		retryableUnknown(), retryableUnknown(), retryableUnknown(), retryableUnknown(),
	}}
	in := baseInput(t, TierPro, loopGroup("A"), h)

	_, _ = RunFallbackLoop(context.Background(), in)

	if len(h.reservationIDs) != 4 {
		t.Fatalf("expected 4 reservations, got %d", len(h.reservationIDs))
	}
	seen := map[string]bool{}
	for i, id := range h.reservationIDs {
		want, err := quota.ReservationID("req-1", fmt.Sprintf("req-1#att%d", i+1))
		if err != nil {
			t.Fatalf("ReservationID: %v", err)
		}
		if id != want {
			t.Fatalf("attempt %d reservation id = %q, want %q", i+1, id, want)
		}
		if seen[id] {
			t.Fatalf("reservation id %q reused across attempts", id)
		}
		seen[id] = true
	}
}

// TestRunFallbackLoop_ExactlyOneTerminalOpPerAttempt proves the crown invariant:
// every executed attempt performs exactly one terminal lifecycle op — the total
// across all four branches equals the number of executed attempts.
func TestRunFallbackLoop_ExactlyOneTerminalOpPerAttempt(t *testing.T) {
	h := &fakeHarness{script: []scriptedAttempt{
		{outcome: ExecOutcome{Err: errRetryable}, verdict: VerdictPreConsumptionFailure},                                                             // release
		{outcome: ExecOutcome{Err: errRetryable, ActualCost: map[quota.Unit]float64{quota.UnitOutputTokens: 3}}, verdict: VerdictPartialConsumption}, // settle
		{outcome: ExecOutcome{Err: errRetryable}, verdict: VerdictUnknownConsumption},                                                                // mark_pending
		{outcome: successOutcome(), verdict: VerdictSuccess},                                                                                         // settle
	}}
	in := baseInput(t, TierPro, loopGroup("A"), h)

	if _, err := RunFallbackLoop(context.Background(), in); err != nil {
		t.Fatalf("expected success on attempt 4, got %v", err)
	}
	terminal := h.settle + h.settleEstimate + h.release + h.markPending
	if terminal != h.execCalls {
		t.Fatalf("terminal ops (%d) != executed attempts (%d): some attempt did zero or two", terminal, h.execCalls)
	}
	if h.release != 1 || h.settle != 2 || h.markPending != 1 || h.settleEstimate != 0 {
		t.Fatalf("branch coverage wrong: release=%d settle=%d markPending=%d settleEstimate=%d", h.release, h.settle, h.markPending, h.settleEstimate)
	}
	if h.markDispatched != h.execCalls {
		t.Fatalf("mark-dispatched (%d) must equal executed attempts (%d)", h.markDispatched, h.execCalls)
	}
}

// TestRunFallbackLoop_UnknownKeepsHeadroom proves the unknown/network-cut verdict
// marks reconciliation_pending and NEVER releases.
//
// Mutation row F-M4: release instead of mark-pending on the unknown verdict →
// release != 0 → this test RED.
func TestRunFallbackLoop_UnknownKeepsHeadroom(t *testing.T) {
	h := &fakeHarness{script: []scriptedAttempt{
		retryableUnknown(), retryableUnknown(), retryableUnknown(), retryableUnknown(),
	}}
	in := baseInput(t, TierPro, loopGroup("A"), h)

	_, _ = RunFallbackLoop(context.Background(), in)

	if h.markPending != 4 {
		t.Fatalf("expected 4 mark-pending, got %d", h.markPending)
	}
	if h.release != 0 {
		t.Fatalf("unknown verdict must never release; release=%d", h.release)
	}
}

// TestRunFallbackLoop_PartialSettlesKnownCost proves the partial branch settles
// the provider-reported KNOWN cost (not the estimate, not zero) and still loops.
func TestRunFallbackLoop_PartialSettlesKnownCost(t *testing.T) {
	knownCost := map[quota.Unit]float64{quota.UnitInputTokens: 7, quota.UnitOutputTokens: 4}
	h := &fakeHarness{script: []scriptedAttempt{
		{outcome: ExecOutcome{Err: errRetryable, ActualCost: knownCost}, verdict: VerdictPartialConsumption},
		{outcome: successOutcome(), verdict: VerdictSuccess},
	}}
	in := baseInput(t, TierPro, loopGroup("A"), h)

	if _, err := RunFallbackLoop(context.Background(), in); err != nil {
		t.Fatalf("expected success on attempt 2, got %v", err)
	}
	if h.execCalls != 2 {
		t.Fatalf("partial must continue the loop; execCalls=%d want 2", h.execCalls)
	}
	if len(h.settleActuals) < 1 {
		t.Fatalf("no settle recorded")
	}
	got := h.settleActuals[0] // the partial attempt's settle
	if got[quota.UnitInputTokens] != 7 || got[quota.UnitOutputTokens] != 4 {
		t.Fatalf("partial settled %v, want the known cost %v", got, knownCost)
	}
	if h.settleEstimate != 0 {
		t.Fatalf("partial with known cost must not settle-at-estimate; settleEstimate=%d", h.settleEstimate)
	}
}

// TestRunFallbackLoop_RequestScopeStops proves a request-scope verdict makes
// exactly one attempt, releases (nothing consumed), and returns the failure with
// no second selection.
func TestRunFallbackLoop_RequestScopeStops(t *testing.T) {
	planted := errors.New("bad request, do not retry")
	h := &fakeHarness{script: []scriptedAttempt{
		{outcome: ExecOutcome{Err: planted}, verdict: VerdictRequestScope},
		{outcome: successOutcome(), verdict: VerdictSuccess}, // must never run
	}}
	in := baseInput(t, TierPro, loopGroup("A"), h)

	_, err := RunFallbackLoop(context.Background(), in)
	if !errors.Is(err, planted) {
		t.Fatalf("request-scope must return the failure; got %v", err)
	}
	if h.recordCalls != 1 || h.execCalls != 1 {
		t.Fatalf("request-scope must make exactly one attempt; record=%d exec=%d", h.recordCalls, h.execCalls)
	}
	if h.release != 1 {
		t.Fatalf("request-scope (pre-consumption) must release exactly once; release=%d", h.release)
	}
}

// TestRunFallbackLoop_ExhaustionCountAndRetryAfter proves the loop runs exactly
// AttemptBudget attempts for each tier when all fail retryably, then returns the
// typed sentinel carrying the EARLIEST retry_after observed.
//
// Mutation row F-M6: hardcode the budget to 3 → Pro/Max counts wrong → RED.
func TestRunFallbackLoop_ExhaustionCountAndRetryAfter(t *testing.T) {
	cases := []struct {
		tier Tier
		want int
	}{
		{TierLite, 3}, {TierPro, 4}, {TierMax, 5},
	}
	for _, tc := range cases {
		t.Run(string(tc.tier), func(t *testing.T) {
			script := make([]scriptedAttempt, tc.want)
			retryAfters := []time.Duration{30 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second, 25 * time.Second}
			for i := range script {
				script[i] = scriptedAttempt{
					outcome: ExecOutcome{Err: errRetryable, RetryAfter: retryAfters[i]},
					verdict: VerdictUnknownConsumption,
				}
			}
			h := &fakeHarness{script: script}
			in := baseInput(t, tc.tier, loopGroup("A"), h)

			_, err := RunFallbackLoop(context.Background(), in)
			if h.execCalls != tc.want {
				t.Fatalf("tier %s: ran %d attempts, want budget %d", tc.tier, h.execCalls, tc.want)
			}
			var noOffer *NoEligibleOfferingError
			if !errors.As(err, &noOffer) {
				t.Fatalf("tier %s: expected *NoEligibleOfferingError, got %v", tc.tier, err)
			}
			if noOffer.RetryAfter != 10*time.Second {
				t.Fatalf("tier %s: earliest retry_after = %v, want 10s", tc.tier, noOffer.RetryAfter)
			}
		})
	}
}

// TestRunFallbackLoop_MarkDispatchedOrdering proves mark-dispatched happens
// AFTER a successful reserve and BEFORE execute, via the call-order log.
//
// Mutation row F-M5: skip mark-dispatched → the ordering assertion RED.
func TestRunFallbackLoop_MarkDispatchedOrdering(t *testing.T) {
	h := &fakeHarness{script: []scriptedAttempt{{outcome: successOutcome(), verdict: VerdictSuccess}}}
	in := baseInput(t, TierPro, loopGroup("A"), h)

	if _, err := RunFallbackLoop(context.Background(), in); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	iReserve := indexOf(h.order, "reserve_ok")
	iDispatch := indexOf(h.order, "mark_dispatched")
	iExec := indexOf(h.order, "execute")
	if iReserve < 0 || iDispatch < 0 || iExec < 0 {
		t.Fatalf("missing ops in order log: %v", h.order)
	}
	if iReserve >= iDispatch || iDispatch >= iExec {
		t.Fatalf("order must be reserve < mark_dispatched < execute; got %v", h.order)
	}
}

// TestRunFallbackLoop_NoContentStored proves the recorder receives no prompt or
// response content — only identifiers. A canary planted in the response never
// reaches any recorded AttemptRecord field.
func TestRunFallbackLoop_NoContentStored(t *testing.T) {
	const canary = "SECRET-PROMPT-CANARY-9f3a"
	h := &fakeHarness{script: []scriptedAttempt{
		{outcome: ExecOutcome{Response: "response body containing " + canary, ActualCost: map[quota.Unit]float64{quota.UnitOutputTokens: 1}}, verdict: VerdictSuccess},
	}}
	in := baseInput(t, TierPro, loopGroup("A"), h)

	res, err := RunFallbackLoop(context.Background(), in)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	// Non-vacuity: the canary really is in the system (the response carries it).
	if resp, _ := res.Response.(string); !strings.Contains(resp, canary) {
		t.Fatalf("precondition: response should carry the canary, got %q", resp)
	}
	if len(h.recorded) != 1 {
		t.Fatalf("expected exactly one recorded attempt, got %d", len(h.recorded))
	}
	for _, rec := range h.recorded {
		for _, field := range []string{rec.RequestID, rec.AttemptID, rec.AccountID, rec.ProviderModelID} {
			if strings.Contains(field, canary) {
				t.Fatalf("content canary leaked into a recorded attempt field: %q", field)
			}
		}
	}
}

// TestRunFallbackLoop_SuccessReturnsResponse proves the happy path returns the
// response and the settled account/reservation identity.
func TestRunFallbackLoop_SuccessReturnsResponse(t *testing.T) {
	h := &fakeHarness{script: []scriptedAttempt{{outcome: successOutcome(), verdict: VerdictSuccess}}}
	in := baseInput(t, TierPro, loopGroup("A"), h)

	res, err := RunFallbackLoop(context.Background(), in)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if res.AccountID != "A" || res.Attempts != 1 {
		t.Fatalf("unexpected result: account=%q attempts=%d", res.AccountID, res.Attempts)
	}
	if h.settle != 1 {
		t.Fatalf("success with actuals must settle once, got settle=%d", h.settle)
	}
}

func indexOf(xs []string, target string) int {
	for i, x := range xs {
		if x == target {
			return i
		}
	}
	return -1
}
