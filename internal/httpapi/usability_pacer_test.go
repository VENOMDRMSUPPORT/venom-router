package httpapi

import (
	"sync"
	"testing"
	"time"
)

// TestPacer_AIMDAndBreaker is the binding scenario from the spec (§3.C.2-3):
// multiplicative decrease on every rate-limit (halve, floor 1), additive
// increase on success (+1, cap max), and the 3rd CONSECUTIVE rate-limit opens
// a half-open breaker for the advertised retryAfter (or 30s when 0).
func TestPacer_AIMDAndBreaker(t *testing.T) {
	clock := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	p := newUsabilityPacer(4, now)

	if got := p.Concurrency(); got != 4 {
		t.Fatalf("initial concurrency = %d, want 4", got)
	}
	p.OnRateLimited(0)
	if got := p.Concurrency(); got != 2 {
		t.Fatalf("after 1st RL = %d, want 2 (halved)", got)
	}
	p.OnRateLimited(0)
	if got := p.Concurrency(); got != 1 {
		t.Fatalf("after 2nd RL = %d, want 1 (floor)", got)
	}
	if !p.Admit() {
		t.Fatal("breaker must still admit before the 3rd consecutive rate-limit")
	}
	p.OnRateLimited(10 * time.Second) // 3rd consecutive -> open with the advertised pause
	if p.Admit() {
		t.Fatal("breaker open: must not admit")
	}
	clock = clock.Add(11 * time.Second)
	if !p.Admit() {
		t.Fatal("cooldown elapsed: half-open must admit exactly one")
	}
	if p.Admit() {
		t.Fatal("half-open: the second concurrent admit must be refused")
	}
	p.OnSuccess()
	if !p.Admit() {
		t.Fatal("success in half-open must close the breaker")
	}
	if got := p.Concurrency(); got != 2 {
		t.Fatalf("post-success concurrency = %d, want 2 (additive +1 from 1)", got)
	}
}

// TestPacer_ZeroRetryAfterDefaultsTo30s pins down the "or 30s when retryAfter
// is 0" edge from the spec: a 3rd consecutive rate-limit reported with a zero
// retryAfter must still open the breaker, with a 30s cooldown.
func TestPacer_ZeroRetryAfterDefaultsTo30s(t *testing.T) {
	clock := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	p := newUsabilityPacer(4, now)

	p.OnRateLimited(0)
	p.OnRateLimited(0)
	p.OnRateLimited(0) // 3rd consecutive, retryAfter=0 -> 30s pause

	if p.Admit() {
		t.Fatal("breaker open: must not admit before 30s elapse")
	}
	clock = clock.Add(29 * time.Second)
	if p.Admit() {
		t.Fatal("breaker open: 29s must not be enough")
	}
	clock = clock.Add(1 * time.Second) // total 30s elapsed
	if !p.Admit() {
		t.Fatal("30s elapsed: half-open must admit exactly one")
	}
}

// TestPacer_SuccessResetsConsecutiveStreak checks that a success anywhere
// resets the consecutive rate-limit counter, so the breaker only opens after
// 3 CONSECUTIVE rate-limits, not 3 total.
func TestPacer_SuccessResetsConsecutiveStreak(t *testing.T) {
	clock := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	p := newUsabilityPacer(4, now)

	p.OnRateLimited(0)
	p.OnRateLimited(0)
	p.OnSuccess() // resets the streak
	p.OnRateLimited(0)
	p.OnRateLimited(0)

	// Only 2 consecutive since the reset -> breaker must still be closed.
	if !p.Admit() {
		t.Fatal("streak was reset by OnSuccess: breaker must not have opened yet")
	}
}

// TestPacer_HalfOpenRateLimitedReArmsPause checks that a rate-limit verdict on
// the single half-open probe re-arms the same pause rather than closing or
// permanently opening the breaker.
func TestPacer_HalfOpenRateLimitedReArmsPause(t *testing.T) {
	clock := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	p := newUsabilityPacer(4, now)

	p.OnRateLimited(0)
	p.OnRateLimited(0)
	p.OnRateLimited(5 * time.Second) // opens breaker

	clock = clock.Add(5 * time.Second)
	if !p.Admit() {
		t.Fatal("cooldown elapsed: half-open must admit exactly one")
	}
	p.OnRateLimited(5 * time.Second) // half-open probe rate-limited -> re-arm

	if p.Admit() {
		t.Fatal("half-open probe was rate-limited: breaker must be open again immediately")
	}
	clock = clock.Add(4 * time.Second)
	if p.Admit() {
		t.Fatal("re-armed pause: 4s of 5s must not be enough")
	}
	clock = clock.Add(1 * time.Second)
	if !p.Admit() {
		t.Fatal("re-armed pause elapsed: half-open must admit exactly one")
	}
}

// TestPacer_StragglerRateLimitDoesNotExtendPause covers the fix-round-1
// defect: a probe admitted while the breaker was still closed (a "straggler")
// can still be in flight when the 3rd consecutive rate-limit trips the
// breaker open. When that straggler later reports OnRateLimited with its own
// retryAfter, it must NOT override the pause advertised at open time and must
// NOT re-trigger the open transition — only the true half-open probe's
// OnRateLimited re-arms the pause.
func TestPacer_StragglerRateLimitDoesNotExtendPause(t *testing.T) {
	clock := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	p := newUsabilityPacer(4, now)

	p.OnRateLimited(0)
	p.OnRateLimited(0)
	p.OnRateLimited(10 * time.Second) // 3rd consecutive -> opens with a 10s pause

	clock = clock.Add(1 * time.Second)
	p.OnRateLimited(60 * time.Second) // straggler admitted before the breaker opened

	clock = clock.Add(8 * time.Second) // total elapsed since open: 9s of the original 10s
	if p.Admit() {
		t.Fatal("straggler's retryAfter must not extend the pause: 9s of the original 10s must not be enough")
	}
	clock = clock.Add(1 * time.Second) // total elapsed since open: 10s
	if !p.Admit() {
		t.Fatal("original 10s pause elapsed: half-open must admit exactly one (unchanged by the straggler)")
	}
}

// TestPacer_ClampsAndDefaults covers the resolved edge decisions: maxConcurrency
// < 1 clamps to 1, and a nil clock defaults to time.Now (so it must not panic
// and must return a sane, non-zero-max concurrency).
func TestPacer_ClampsAndDefaults(t *testing.T) {
	p := newUsabilityPacer(0, nil)
	if got := p.Concurrency(); got != 1 {
		t.Fatalf("maxConcurrency=0 must clamp to 1, got %d", got)
	}
	// Exercise the default clock path: Admit must work without panicking.
	if !p.Admit() {
		t.Fatal("fresh pacer with default clock must admit")
	}

	p2 := newUsabilityPacer(-5, nil)
	if got := p2.Concurrency(); got != 1 {
		t.Fatalf("maxConcurrency=-5 must clamp to 1, got %d", got)
	}
}

// TestPacer_ConcurrencyNeverBelowOne hammers OnSuccess/OnRateLimited/Admit/
// Concurrency from multiple goroutines concurrently. Under -race this proves
// the mutex actually guards every field; the only assertions are the
// invariants the brief calls out (values in flight are chaotic by design):
// Concurrency() must always be in [1, max].
func TestPacer_ConcurrencyNeverBelowOne(t *testing.T) {
	const maxConcurrency = 8
	clock := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	now := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return clock
	}
	p := newUsabilityPacer(maxConcurrency, now)

	const workers = 16
	const iterations = 200
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				switch (id + i) % 4 {
				case 0:
					p.OnSuccess()
				case 1:
					p.OnRateLimited(0)
				case 2:
					p.Admit()
				case 3:
					if got := p.Concurrency(); got < 1 || got > maxConcurrency {
						t.Errorf("Concurrency() = %d, want in [1,%d]", got, maxConcurrency)
					}
				}
				if i%10 == 0 {
					mu.Lock()
					clock = clock.Add(time.Second)
					mu.Unlock()
				}
			}
		}(w)
	}
	wg.Wait()

	if got := p.Concurrency(); got < 1 || got > maxConcurrency {
		t.Fatalf("final Concurrency() = %d, want in [1,%d]", got, maxConcurrency)
	}
}
