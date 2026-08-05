package httpapi

import (
	"sync"
	"time"
)

// defaultBreakerPause is the cooldown applied when a rate-limit response does
// not advertise a retryAfter (spec §3.C.3: "or 30s when retryAfter is 0").
const defaultBreakerPause = 30 * time.Second

// consecutiveRateLimitsToOpenBreaker is the number of CONSECUTIVE rate-limit
// verdicts (with no intervening success) that trip the breaker open.
const consecutiveRateLimitsToOpenBreaker = 3

// usabilityPacer is a pure, mutex-guarded, clock-injected AIMD concurrency
// limiter with a per-account half-open circuit breaker, as specified in
// spec §3.C.2-3. It holds no goroutines and performs no I/O of its own; the
// model-usability probe loop (Task 7) is expected to call Concurrency() to
// size its worker wave, Admit() to gate each probe, and report the verdict
// back via OnSuccess/OnRateLimited.
type usabilityPacer struct {
	mu  sync.Mutex
	cur int
	max int
	now func() time.Time

	consecutiveRL int

	// breakerOpen is true while the pause is in effect (Admit refuses).
	// Once now() reaches pausedUntil, the breaker transitions to half-open
	// (tracked by halfOpenPending/halfOpenInFlight) rather than closing
	// outright — exactly one probe must prove the account healthy again.
	breakerOpen      bool
	pausedUntil      time.Time
	halfOpenInFlight bool
}

// newUsabilityPacer builds a pacer capped at maxConcurrency parallel probes
// (clamped to at least 1) using now for all time comparisons. A nil now
// defaults to time.Now.
func newUsabilityPacer(maxConcurrency int, now func() time.Time) *usabilityPacer {
	if maxConcurrency < 1 {
		maxConcurrency = 1
	}
	if now == nil {
		now = time.Now
	}
	return &usabilityPacer{
		cur: maxConcurrency,
		max: maxConcurrency,
		now: now,
	}
}

// Concurrency returns the current allowed parallel probe count. Always >= 1.
func (p *usabilityPacer) Concurrency() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cur
}

// Admit reports whether a new probe may start. While the breaker is open
// (cooldown not yet elapsed) it returns false with no side effects. Once the
// cooldown elapses, the breaker becomes half-open and Admit admits exactly
// one probe; further calls return false until that probe's outcome is
// reported via OnSuccess or OnRateLimited.
func (p *usabilityPacer) Admit() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.breakerOpen {
		return true
	}

	if p.halfOpenInFlight {
		// The single half-open slot is already out; refuse until resolved.
		return false
	}

	if !p.now().Before(p.pausedUntil) {
		// Cooldown elapsed: admit exactly one half-open probe.
		p.halfOpenInFlight = true
		return true
	}

	return false
}

// OnSuccess records a successful probe: additive increase of concurrency
// (+1, capped at max), resets the consecutive rate-limit streak, and — if
// this was the half-open probe — closes the breaker.
func (p *usabilityPacer) OnSuccess() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.consecutiveRL = 0

	if p.cur < p.max {
		p.cur++
	}

	if p.breakerOpen && p.halfOpenInFlight {
		p.breakerOpen = false
		p.halfOpenInFlight = false
	}
}

// OnRateLimited records a rate-limited probe: multiplicative decrease of
// concurrency (halve, floor 1) on every call. The 3rd CONSECUTIVE rate-limit
// opens the breaker for retryAfter (or defaultBreakerPause when retryAfter is
// 0). If this was the half-open probe, it re-arms the same pause instead of
// closing the breaker.
func (p *usabilityPacer) OnRateLimited(retryAfter time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.cur /= 2
	if p.cur < 1 {
		p.cur = 1
	}

	pause := retryAfter
	if pause <= 0 {
		pause = defaultBreakerPause
	}

	if p.breakerOpen && p.halfOpenInFlight {
		// The half-open probe failed again: re-arm the same pause.
		p.halfOpenInFlight = false
		p.pausedUntil = p.now().Add(pause)
		return
	}

	p.consecutiveRL++
	if p.consecutiveRL >= consecutiveRateLimitsToOpenBreaker {
		p.breakerOpen = true
		p.pausedUntil = p.now().Add(pause)
	}
}
