package quota

import (
	"errors"
	"time"
)

// ReconciliationPolicy bounds the reconciliation worker's retry behavior
// over reconciliation_pending reservations (02 §3 / 05 §4): how many
// retries it allows before giving up, the backoff between them, and how
// many pending reservations it processes per sweep.
type ReconciliationPolicy struct {
	MaxRetries  int
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
	BatchSize   int
}

// DefaultReconciliationPolicy returns the conservative defaults (05 §4).
func DefaultReconciliationPolicy() ReconciliationPolicy {
	return ReconciliationPolicy{
		MaxRetries:  5,
		BaseBackoff: 30 * time.Second,
		MaxBackoff:  30 * time.Minute,
		BatchSize:   20,
	}
}

// ReconciliationOutcome is what ReconcileOne decided for one pending
// reservation: the terminal or non-terminal ReservationState it moved
// to, and (when settled) the per-window costs it settled at.
type ReconciliationOutcome struct {
	ReservationID string
	Outcome       ReservationState
	Actuals       map[string]float64
}

// The reconciliation worker inherits the same SetMaxOpenConns(1) hazard
// documented on the janitor: its storage methods each acquire their own
// connection (via QuotaLifecycleRepo's Settle/Release/Transition), so
// calling them from inside a transaction the caller already holds
// deadlocks. As there, this is documentation rather than an exported
// sentinel, because no runtime path can detect the misuse.

// BackoffFor is 05 §4's retry schedule verbatim: 30s -> 5m -> 30m,
// capped at policy.MaxBackoff. attempts <= 0 (a reservation's first
// reconciliation attempt) yields policy.BaseBackoff outright — there is
// no "0th power" step. A non-positive BaseBackoff yields MaxBackoff
// rather than 0: a zero backoff would let the worker busy-loop on a
// broken policy instead of failing slow.
func BackoffFor(policy ReconciliationPolicy, attempts int) time.Duration {
	if policy.BaseBackoff <= 0 {
		return policy.MaxBackoff
	}
	if attempts <= 0 {
		return policy.BaseBackoff
	}

	backoff := policy.BaseBackoff
	for i := 0; i < attempts; i++ {
		backoff *= 10
		if policy.MaxBackoff > 0 && backoff >= policy.MaxBackoff {
			return policy.MaxBackoff
		}
	}
	return backoff
}

// RetryExhausted is THE single terminal-boundary predicate (05 §4's
// "bounded number of attempts, default 5"): both the janitor
// (storage.QuotaLifecycleRepo's reconciliation-pending sweep) and the
// reconciliation worker (storage.ReconciliationRepo.ReconcileOne) call
// THIS and nothing else, so the two can never disagree about when a
// reservation becomes unknown_consumption — the exact contradiction this
// remediation batch exists to close. attempts >= policy.MaxRetries is
// exhausted; a non-positive MaxRetries is treated as ALWAYS exhausted —
// fail closed, so a misconfigured policy never retries forever.
func RetryExhausted(policy ReconciliationPolicy, attempts int) bool {
	if policy.MaxRetries <= 0 {
		return true
	}
	return attempts >= policy.MaxRetries
}

// DefaultLeaseTTL is how long a reconciliation worker's claim on a
// pending reservation lasts before it is treated as abandoned (05 §4
// "Lease / ownership": auto-expiring, so a crashed worker's item is
// reclaimed).
const DefaultLeaseTTL = 5 * time.Minute

// ErrLeaseNotHeld is returned by ReconcileOne when the caller's owner
// token no longer matches the reservation's current lease_owner (e.g.
// the lease expired and another worker already reclaimed or re-claimed
// it) — a worker whose lease expired must not settle.
var ErrLeaseNotHeld = errors.New("quota: reconciliation lease not held by this worker")

// Confidence marks the provenance of a settled allocation's actual_cost
// (05 §4's "Final outcomes"): settle(actual) and
// settle(estimate, confidence=low) are DISTINCT outcomes — a guess must
// never be byte-identical, in the database, to a provider-confirmed
// cost.
type Confidence string

const (
	// ConfidenceHigh marks an actual_cost the caller asserts is
	// provider-confirmed.
	ConfidenceHigh Confidence = "high"
	// ConfidenceLow marks an actual_cost that is a conservative estimate
	// standing in for a provider-confirmed value that was never obtained.
	ConfidenceLow Confidence = "low"
)

// ErrUnknownConfidence is returned by ParseConfidence for any token
// outside the two canonical confidence values.
var ErrUnknownConfidence = errors.New("quota: unknown confidence")

// ParseConfidence fails closed: an unrecognized token — including a
// wrong-case variant or an empty string — is rejected rather than
// silently accepted or defaulted to a particular confidence.
func ParseConfidence(s string) (Confidence, error) {
	switch Confidence(s) {
	case ConfidenceHigh, ConfidenceLow:
		return Confidence(s), nil
	default:
		return "", ErrUnknownConfidence
	}
}
