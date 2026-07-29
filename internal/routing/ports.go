package routing

import (
	"context"
	"errors"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

// This file declares the LOCAL PORTS the Step-8 fallback loop (RunFallbackLoop)
// drives (05 §2 Step 8). They are interfaces declared here and implemented
// NOWHERE in this unit: internal/routing is staticgate-pure and can never
// import internal/execution (net/http) or internal/storage (database/sql), so
// the concrete adapters that wrap those packages belong to a later wiring unit.
// This mirrors internal/intelligence's probe-transport port pattern from P3c.

// AttemptRecord is the content-free observability identity of one attempt
// (05 §2 Step 8.2: "no token content ever stored"). It carries identifiers
// only — never a prompt, a response, or any request body text.
type AttemptRecord struct {
	RequestID       string
	AttemptID       string
	AccountID       string
	ProviderModelID string
}

// AttemptRecorder persists the attempt identity BEFORE reservation (Step 8.2).
// The persisted route_attempt record itself is OBS-001's job; this is only the
// seam. It receives an AttemptRecord, which structurally cannot hold content.
type AttemptRecorder interface {
	RecordAttempt(ctx context.Context, rec AttemptRecord) error
}

// ReserveParams is the input to one atomic, all-windows reservation (Step 8.3).
// ReservationID is derived by the loop via quota.ReservationID and passed in so
// every lifecycle call keys on the same id; the adapter must not re-derive it.
type ReserveParams struct {
	ReservationID string
	AccountID     string
	RequestID     string
	AttemptID     string
	Allocations   []quota.Allocation
}

// ReserveResult reports the reservation id and whether the reserve was a
// no-op replay of an already-existing reservation (idempotency, Step 8 identity
// block). The loop echoes ReservationID for its lifecycle calls.
type ReserveResult struct {
	ReservationID string
	Idempotent    bool
}

// ErrReservationRejected is the typed rejection for the zero-rows case
// (Step 8.3: "any window affects 0 rows → ROLLBACK ... nothing left debited").
// The adapter wrapping storage.ErrReservationRejected MUST surface it such that
// errors.Is(err, ErrReservationRejected) holds. Because nothing was debited on
// this error, the loop must NOT release — it re-evaluates and loops.
var ErrReservationRejected = errors.New("routing: quota reservation rejected (no window had headroom)")

// Reserver reserves quota atomically across every applicable window for one
// candidate (Step 8.3). It returns ErrReservationRejected (matchable via
// errors.Is) when the conditional update affected zero rows on any window.
type Reserver interface {
	Reserve(ctx context.Context, params ReserveParams) (ReserveResult, error)
}

// Lifecycle performs the reservation state transitions (Step 8.4-8.5, 02 §3).
// Each operation is idempotent on reservationID. Exactly ONE terminal operation
// (Settle / SettleEstimate / Release / MarkReconciliationPending) runs per
// executed attempt — never zero, never two.
type Lifecycle interface {
	// MarkDispatched stamps dispatched_at BEFORE execution so the janitor can
	// distinguish "never dispatched" from "crashed after dispatch" (02 §3).
	MarkDispatched(ctx context.Context, reservationID string) error
	// Settle converts reserved → consumed at the provider-reported actuals.
	Settle(ctx context.Context, reservationID string, actuals map[quota.Unit]float64) error
	// SettleEstimate converts reserved → consumed at the reserved estimate,
	// used on a success/partial outcome that reported no usable actuals.
	SettleEstimate(ctx context.Context, reservationID string) error
	// Release frees the whole reservation (nothing consumed).
	Release(ctx context.Context, reservationID string) error
	// MarkReconciliationPending transitions reserved → reconciliation_pending;
	// headroom STAYS debited (never auto-released).
	MarkReconciliationPending(ctx context.Context, reservationID string) error
}

// ResolvedAttempt is everything the executor needs to run one attempt. It
// carries the typed Requirements (request shape/context), never raw content —
// content handling lives entirely behind the executor adapter.
type ResolvedAttempt struct {
	RequestID     string
	AttemptID     string
	ReservationID string
	Candidate     CandidateOffering
	Requirements  Requirements
}

// ExecOutcome is one attempt's execution result in routing-local terms
// (Step 8.4). Err == nil means success. ActualCost carries provider-reported
// per-unit costs when known (full on success, known-so-far on a partial
// failure); nil ⇒ the loop settles at the reserved estimate. RetryAfter is a
// provider-suggested retry hint (0 = none) threaded into the exhaustion
// sentinel.
type ExecOutcome struct {
	Response   any
	ActualCost map[quota.Unit]float64
	RetryAfter time.Duration
	Err        error
}

// Executor executes one resolved attempt with NO reservation-mutating call in
// flight (Step 8.4). It must never be invoked before a successful reserve.
type Executor interface {
	Execute(ctx context.Context, attempt ResolvedAttempt) ExecOutcome
}

// ReconcileVerdict is this unit's closed routing-neutral vocabulary for the
// Step 8.5 reconcile branches. The adapter maps execution.TypedFailure.Scope
// onto these; ROUTE-014 owns that real mapping, cooldowns, and circuit
// breakers — this loop only consumes the verdict.
type ReconcileVerdict int

const (
	// VerdictSuccess: execution succeeded → settle → return the response.
	VerdictSuccess ReconcileVerdict = iota
	// VerdictPreConsumptionFailure: provider failed before consuming (401,
	// invalid schema) → release → loop.
	VerdictPreConsumptionFailure
	// VerdictPartialConsumption: provider confirmed partial tokens → settle the
	// known cost → loop.
	VerdictPartialConsumption
	// VerdictUnknownConsumption: network cut / unknown consumption → mark
	// reconciliation_pending (headroom stays debited) → loop.
	VerdictUnknownConsumption
	// VerdictRequestScope: the request itself is bad (nothing consumed) → stop
	// the loop and return the failure; no further attempts.
	VerdictRequestScope
)

// FailureClassifier maps a non-nil execution error to a ReconcileVerdict. It is
// never asked to classify success. It must never return VerdictSuccess.
type FailureClassifier interface {
	Classify(err error) ReconcileVerdict
}

// PoolReEvaluator returns a FRESH ranked pool snapshot (Step 8.3: after a
// rejected reservation, "re-evaluate the candidate pool from a fresh snapshot"
// — never reuse the stale one). It returns the whole ranked RoutePool, not a
// single group, so P4-WIRE-001's cross-offering / skip-provider actions remain
// expressible after a re-evaluation.
type PoolReEvaluator interface {
	ReEvaluate(ctx context.Context) (RoutePool, error)
}

// FailureScoper reports the routing-neutral FallbackScope for a failed attempt's
// error (P4-WIRE-001). It is separate from FailureClassifier (which yields the
// reconcile verdict): the loop needs BOTH the verdict (how to settle/release
// quota) AND the scope (how to steer the next attempt and which breaker to
// trip). The httpapi ScopeClassifier satisfies both. A nil Scoper disables the
// scope-classified fallback path, leaving the ROUTE-013 verdict-only behavior.
type FailureScoper interface {
	ScopeOf(err error) FallbackScope
}

// AttemptIDMinter mints a distinct attemptID per attempt (1-based
// attemptNumber) so quota.ReservationID yields a distinct reservation per
// attempt — no reservation is ever inherited across attempts.
type AttemptIDMinter interface {
	MintAttemptID(requestID string, attemptNumber int) string
}
