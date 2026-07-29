package routing

import (
	"reflect"
	"testing"
	"time"
)

// TestBreaker_AdaptiveBackoffDoublesAndCaps proves each successive open cycle
// doubles the reset timeout and saturates at the ~16× cap (05 §3).
//
// Mutation row R14-M4: linear backoff instead of doubling → the sequence RED.
// Mutation row R14-M5: remove the cap → the saturation assertion RED.
func TestBreaker_AdaptiveBackoffDoublesAndCaps(t *testing.T) {
	now := drrTestNow
	var b Breaker
	wantMult := []int{1, 2, 4, 8, 16, 16, 16} // cycle 1..7, capped at 16
	for i, mult := range wantMult {
		b = b.Trip(now)
		want := time.Duration(mult) * BreakerBaseTimeout
		if got := b.ResetTimeout(); got != want {
			t.Fatalf("open cycle %d: reset timeout = %v, want %v (mult %d)", i+1, got, want, mult)
		}
	}
}

// TestBreaker_LazyRecoveryNoTimer proves state refreshes on READ from
// (openedAt, now) with no timer/goroutine: an expired open window reads as
// half-open. Structurally asserts the type carries no time.Timer/Ticker.
func TestBreaker_LazyRecoveryNoTimer(t *testing.T) {
	now := drrTestNow
	b := Breaker{}.Trip(now) // open, cycle 1, timeout = base
	if b.EffectiveState(now) != BreakerOpen {
		t.Fatalf("freshly tripped breaker must read open")
	}
	if b.EffectiveState(now.Add(BreakerBaseTimeout-time.Second)) != BreakerOpen {
		t.Fatalf("inside the window the breaker must still read open")
	}
	if b.EffectiveState(now.Add(BreakerBaseTimeout)) != BreakerHalfOpen {
		t.Fatalf("at window expiry the breaker must lazily read half-open")
	}

	// Structural: no timer/ticker field anywhere in Breaker.
	bt := reflect.TypeOf(Breaker{})
	for i := 0; i < bt.NumField(); i++ {
		ft := bt.Field(i).Type.String()
		if ft == "*time.Timer" || ft == "time.Timer" || ft == "*time.Ticker" || ft == "time.Ticker" {
			t.Fatalf("Breaker must not carry a timer/ticker (lazy recovery only); field %q is %s", bt.Field(i).Name, ft)
		}
	}
}

// TestBreaker_HalfOpenAdmitsExactlyOneProbe proves half-open admits one trial:
// success closes + resets the multiplier; failure re-opens with the NEXT
// doubled timeout.
//
// Mutation row R14-M6: half-open admits unlimited probes → the second-probe
// assertion RED.
func TestBreaker_HalfOpenAdmitsExactlyOneProbe(t *testing.T) {
	now := drrTestNow
	b := Breaker{}.Trip(now) // cycle 1
	atHalfOpen := now.Add(BreakerBaseTimeout)

	if b.EffectiveState(atHalfOpen) != BreakerHalfOpen {
		t.Fatalf("precondition: breaker should be half-open")
	}
	if !b.Admits(atHalfOpen) {
		t.Fatalf("half-open must admit the first probe")
	}
	probing := b.MarkProbe()
	if probing.Admits(atHalfOpen) {
		t.Fatalf("half-open must admit EXACTLY one probe; the second was admitted")
	}

	// Success closes and resets the backoff multiplier.
	closed := probing.RecordSuccess()
	if closed.EffectiveState(atHalfOpen) != BreakerClosed {
		t.Fatalf("probe success must close the breaker")
	}
	if closed.OpenCycles != 0 {
		t.Fatalf("probe success must reset the backoff multiplier; OpenCycles=%d", closed.OpenCycles)
	}
	if !closed.Admits(atHalfOpen) {
		t.Fatalf("a closed breaker must admit requests")
	}

	// Failure re-opens with the NEXT doubled timeout (cycle 2 → 2× base).
	reopened := probing.Trip(atHalfOpen)
	if reopened.EffectiveState(atHalfOpen) != BreakerOpen {
		t.Fatalf("probe failure must re-open the breaker")
	}
	if reopened.ResetTimeout() != 2*BreakerBaseTimeout {
		t.Fatalf("re-open must use the next doubled timeout; got %v want %v", reopened.ResetTimeout(), 2*BreakerBaseTimeout)
	}
}

// TestBreaker_UnknownStateFailsClosed proves an unrecognized stored state is
// treated as OPEN (route skipped), never as closed.
//
// Mutation row R14-M7: treat unknown state as closed → this test RED.
func TestBreaker_UnknownStateFailsClosed(t *testing.T) {
	now := drrTestNow
	b := Breaker{State: BreakerState("corrupt-value")}
	if b.EffectiveState(now) != BreakerOpen {
		t.Fatalf("unknown breaker state must fail closed to open, got %q", b.EffectiveState(now))
	}
	if b.Admits(now) {
		t.Fatalf("unknown breaker state must NOT admit a route")
	}
}
