package routing

import "time"

// BreakerState is routing's OWN closed circuit-breaker state vocabulary
// (05 §3). M6's circuit_breakers.state column is deliberately CHECK-free so
// this unit — not the schema — owns the vocabulary.
type BreakerState string

const (
	BreakerClosed   BreakerState = "closed"
	BreakerOpen     BreakerState = "open"
	BreakerHalfOpen BreakerState = "half_open"
)

// BreakerBaseTimeout is the base reset timeout for the first open cycle
// (05 §3). Successive cycles double it up to BreakerMaxBackoffMultiplier×.
const BreakerBaseTimeout = 30 * time.Second

// BreakerMaxBackoffMultiplier caps the adaptive backoff at ~16× the base
// (05 §3).
const BreakerMaxBackoffMultiplier = 16

// Breaker is one scope's circuit-breaker state as a pure VALUE (05 §3). The
// caller persists it (M6 tables are storage's job); this type never touches
// storage. Recovery is LAZY: EffectiveState recomputes from (OpenedAt,
// OpenCycles, now) — there is deliberately no time.Timer/Ticker field, so no
// background goroutine or timer is ever involved.
type Breaker struct {
	State         BreakerState
	FailureCount  int
	OpenCycles    int // how many times it has opened — drives the doubling backoff
	OpenedAt      time.Time
	ProbeInFlight bool // a half-open trial request is outstanding
}

// backoffMultiplier returns 2^(openCycles-1) capped at BreakerMaxBackoffMultiplier:
// cycle 1 → 1×, 2 → 2×, 3 → 4×, 4 → 8×, 5 → 16×, ≥5 → 16× (saturated).
func backoffMultiplier(openCycles int) int {
	if openCycles < 1 {
		return 1
	}
	m := 1
	for i := 1; i < openCycles; i++ {
		m *= 2
		if m >= BreakerMaxBackoffMultiplier {
			return BreakerMaxBackoffMultiplier
		}
	}
	return m
}

// ResetTimeout is the adaptive reset window for the current open cycle.
func (b Breaker) ResetTimeout() time.Duration {
	return time.Duration(backoffMultiplier(b.OpenCycles)) * BreakerBaseTimeout
}

// EffectiveState computes the breaker's state as of now (lazy recovery,
// 05 §3): an open breaker whose reset window has elapsed reads as half-open
// WITHOUT any timer. An unrecognized stored state fails CLOSED to open (skip
// the route), never to closed.
func (b Breaker) EffectiveState(now time.Time) BreakerState {
	switch b.State {
	case BreakerClosed:
		return BreakerClosed
	case BreakerOpen:
		if !now.Before(b.OpenedAt.Add(b.ResetTimeout())) {
			return BreakerHalfOpen
		}
		return BreakerOpen
	case BreakerHalfOpen:
		return BreakerHalfOpen
	default:
		return BreakerOpen
	}
}

// Admits reports whether the breaker allows a request through as of now: a
// closed breaker always admits; a half-open breaker admits exactly one probe
// (only while none is in flight); an open (or unknown) breaker admits nothing.
func (b Breaker) Admits(now time.Time) bool {
	switch b.EffectiveState(now) {
	case BreakerClosed:
		return true
	case BreakerHalfOpen:
		return !b.ProbeInFlight
	default:
		return false
	}
}

// Trip opens the breaker as of now, advancing the open cycle so the next reset
// window uses the next doubled timeout. It clears any in-flight probe. Used
// both to open a closed breaker and to re-open after a failed half-open probe.
func (b Breaker) Trip(now time.Time) Breaker {
	b.State = BreakerOpen
	b.OpenCycles++
	b.FailureCount++
	b.OpenedAt = now
	b.ProbeInFlight = false
	return b
}

// RecordSuccess closes the breaker and resets the adaptive backoff multiplier
// and failure count — a recovered scope reopens at the base timeout, not its
// last inflated one.
func (b Breaker) RecordSuccess() Breaker {
	b.State = BreakerClosed
	b.OpenCycles = 0
	b.FailureCount = 0
	b.ProbeInFlight = false
	b.OpenedAt = time.Time{}
	return b
}

// MarkProbe records that a half-open trial request has been admitted, so the
// breaker admits no further probe until that trial resolves.
func (b Breaker) MarkProbe() Breaker {
	b.ProbeInFlight = true
	return b
}
