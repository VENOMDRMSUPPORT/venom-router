package quota

import (
	"testing"
	"time"
)

func TestDefaultReconciliationPolicy_Defaults(t *testing.T) {
	pol := DefaultReconciliationPolicy()
	if pol.MaxRetries != 5 {
		t.Fatalf("MaxRetries = %d, want 5", pol.MaxRetries)
	}
	if pol.BaseBackoff.String() != "30s" {
		t.Fatalf("BaseBackoff = %v, want 30s", pol.BaseBackoff)
	}
	if pol.MaxBackoff.String() != "30m0s" {
		t.Fatalf("MaxBackoff = %v, want 30m", pol.MaxBackoff)
	}
	if pol.BatchSize != 20 {
		t.Fatalf("BatchSize = %d, want 20", pol.BatchSize)
	}
}

func TestReconciliationOutcome_ZeroValue(t *testing.T) {
	var o ReconciliationOutcome
	if o.ReservationID != "" || o.Outcome != "" || o.Actuals != nil {
		t.Fatalf("zero ReconciliationOutcome = %+v, want all zero", o)
	}
}

// TestBackoffFor_MatchesDocumentedSchedule pins 05 §4's retry schedule
// verbatim: 30s -> 5m -> 30m (capped), and the two edge cases that keep
// the worker from ever backing off by zero.
func TestBackoffFor_MatchesDocumentedSchedule(t *testing.T) {
	policy := DefaultReconciliationPolicy() // BaseBackoff=30s, MaxBackoff=30m

	cases := []struct {
		attempts int
		want     time.Duration
	}{
		{0, 30 * time.Second},
		{1, 5 * time.Minute},
		{2, 30 * time.Minute},
		{3, 30 * time.Minute},
		{-1, 30 * time.Second},
	}
	for _, tc := range cases {
		got := BackoffFor(policy, tc.attempts)
		if got != tc.want {
			t.Fatalf("BackoffFor(attempts=%d) = %v, want %v", tc.attempts, got, tc.want)
		}
	}

	zeroBase := ReconciliationPolicy{BaseBackoff: 0, MaxBackoff: 30 * time.Minute, MaxRetries: 5}
	if got := BackoffFor(zeroBase, 0); got != 30*time.Minute {
		t.Fatalf("BackoffFor with BaseBackoff=0 = %v, want MaxBackoff (30m), never 0", got)
	}
}

// TestRetryExhausted_BoundaryAndFailClosed pins the single terminal-
// boundary predicate both the janitor and the worker must share: the
// exact MaxRetries boundary in both directions, plus the fail-closed
// MaxRetries<=0 case.
func TestRetryExhausted_BoundaryAndFailClosed(t *testing.T) {
	policy := ReconciliationPolicy{MaxRetries: 5}

	if RetryExhausted(policy, 4) {
		t.Fatal("RetryExhausted(attempts=4, MaxRetries=5) = true, want false")
	}
	if !RetryExhausted(policy, 5) {
		t.Fatal("RetryExhausted(attempts=5, MaxRetries=5) = false, want true")
	}
	if !RetryExhausted(policy, 6) {
		t.Fatal("RetryExhausted(attempts=6, MaxRetries=5) = false, want true")
	}

	// -1 is the case that actually distinguishes the fail-closed special
	// case from a bare `attempts >= MaxRetries` comparison: for any
	// non-negative attempts, MaxRetries<=0 already makes the naive
	// comparison true on its own, so a mutant that DELETES the
	// `MaxRetries <= 0` branch entirely would still pass on 0/1/100 —
	// only a negative attempts value exposes it.
	zeroPolicy := ReconciliationPolicy{MaxRetries: 0}
	for _, attempts := range []int{-1, 0, 1, 100} {
		if !RetryExhausted(zeroPolicy, attempts) {
			t.Fatalf("RetryExhausted(attempts=%d, MaxRetries=0) = false, want true (fail closed)", attempts)
		}
	}
}
