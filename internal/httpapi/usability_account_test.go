package httpapi

// usability_account_test.go pins verifyAccountChatUsability — the per-account
// loop that drives each free chat offering-operation from `observed` through a
// probe to a recorded verdict, and STOPS the whole account the moment a probe
// reports an AuthError (the credential itself is bad, so probing the rest is
// pointless and would just repeat the same auth failure).

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

type recordedAttempt struct {
	op       string
	outcome  intelligence.ProbeOutcome
	attempts int
}

// fakeCertLifecycle is mutex-guarded because verifyAccountChatUsability now
// records from several probe goroutines at once; the lock is the fake's own
// bookkeeping, never a relaxation of what the tests assert.
type fakeCertLifecycle struct {
	mu      sync.Mutex
	records []recordedAttempt
}

func (f *fakeCertLifecycle) RecordAttempt(_ context.Context, op string, o intelligence.ProbeOutcome, a int) (models.Certification, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records = append(f.records, recordedAttempt{op, o, a})
	return models.Certification{}, nil
}

// recorded returns a snapshot of every attempt written so far.
func (f *fakeCertLifecycle) recorded() []recordedAttempt {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedAttempt(nil), f.records...)
}

// serialPacer is a pacer that admits exactly one probe at a time, so a test can
// pin the ORDER-dependent behaviour (stop-on-auth skipping later offerings)
// that concurrency would otherwise make nondeterministic.
func serialPacer() *usabilityPacer { return newUsabilityPacer(1, nil) }

// numberedOfferings builds n offerings named op-i / model-i.
func numberedOfferings(n int) []chatOffering {
	out := make([]chatOffering, n)
	for i := range out {
		out[i] = chatOffering{
			OfferingOperationID: fmt.Sprintf("op-%d", i),
			ProviderModelID:     fmt.Sprintf("model-%d", i),
		}
	}
	return out
}

func probeByModel(m map[string]zenChatUsability) usabilityProbeFn {
	return func(_ context.Context, _, _, modelID string) (usabilityProbeResult, error) {
		return usabilityProbeResult{Verdict: m[modelID]}, nil
	}
}

func TestCertifyDeclaredCapabilities_RecordsDefinitiveSupportedForEach(t *testing.T) {
	lc := &fakeCertLifecycle{}
	caps := []declaredCapability{
		{OfferingOperationID: "op-tools", Operation: "tools"},
		{OfferingOperationID: "op-vision", Operation: "vision"},
	}

	got := certifyDeclaredCapabilities(context.Background(), lc, caps)

	if got != 2 {
		t.Fatalf("certified %d, want 2", got)
	}
	if len(lc.records) != 2 {
		t.Fatalf("recorded %d attempts, want 2", len(lc.records))
	}
	// Each non-chat capability is certified FROM DECLARATION: a definitive,
	// capability_confirmed "supported" verdict — the edge that drives
	// probing -> certified carrying TruthSupported.
	for _, r := range lc.records {
		if !r.outcome.Definitive || r.outcome.Truth != models.TruthSupported {
			t.Fatalf("op %s recorded %+v, want Definitive supported", r.op, r.outcome)
		}
		if r.outcome.Reason != intelligence.ReasonCapabilityConfirmed {
			t.Fatalf("op %s reason = %q, want capability_confirmed", r.op, r.outcome.Reason)
		}
	}
}

func TestVerifyAccountChatUsability_ProbesEveryOfferingAndCountsUsable(t *testing.T) {
	lc := &fakeCertLifecycle{}
	offerings := []chatOffering{
		{OfferingOperationID: "op-a", ProviderModelID: "big-pickle"},
		{OfferingOperationID: "op-b", ProviderModelID: "gpt-5.5-pro"},
		{OfferingOperationID: "op-c", ProviderModelID: "deepseek-v4-flash-free"},
	}
	probe := probeByModel(map[string]zenChatUsability{
		"big-pickle":             zenChatUsable,
		"gpt-5.5-pro":            zenChatPaidUnusable,
		"deepseek-v4-flash-free": zenChatUsable,
	})

	got := verifyAccountChatUsability(context.Background(), lc, probe, serialPacer(), "http://x", "key", offerings)

	if got.Probed != 3 || got.Usable != 2 || got.StoppedOnAuth {
		t.Fatalf("summary = %+v, want Probed 3 Usable 2 StoppedOnAuth false", got)
	}
	if len(lc.records) != 3 {
		t.Fatalf("recorded %d attempts, want 3", len(lc.records))
	}
}

func TestVerifyAccountChatUsability_AuthFailureStopsTheAccount(t *testing.T) {
	lc := &fakeCertLifecycle{}
	offerings := []chatOffering{
		{OfferingOperationID: "op-a", ProviderModelID: "big-pickle"},
		{OfferingOperationID: "op-b", ProviderModelID: "bad-auth-model"},
		{OfferingOperationID: "op-c", ProviderModelID: "never-reached"},
	}
	probe := probeByModel(map[string]zenChatUsability{
		"big-pickle":     zenChatUsable,
		"bad-auth-model": zenChatAuthFailure,
		"never-reached":  zenChatUsable,
	})

	// A serial pacer (concurrency 1) is what makes "op-c is never even started"
	// an exact claim: with a parallel wave the later offerings are already in
	// flight when the auth failure lands. The concurrent stop-on-auth behaviour
	// is pinned separately by
	// TestVerifyAccountChatUsability_AuthFailureCancelsTheRemainingWaves.
	got := verifyAccountChatUsability(context.Background(), lc, probe, serialPacer(), "http://x", "key", offerings)

	if !got.StoppedOnAuth {
		t.Fatal("StoppedOnAuth = false, want true")
	}
	// op-c must never be probed/recorded once the account hit an auth failure.
	records := lc.recorded()
	for _, r := range records {
		if r.op == "op-c" {
			t.Fatalf("op-c was recorded after an auth failure; records = %v", records)
		}
	}
}

func TestVerifyAccountChatUsability_TransportFailureSkipsWithoutRecording(t *testing.T) {
	lc := &fakeCertLifecycle{}
	offerings := []chatOffering{
		{OfferingOperationID: "op-a", ProviderModelID: "unreachable"},
		{OfferingOperationID: "op-b", ProviderModelID: "big-pickle"},
	}
	// op-a's probe hits a transport failure; op-b succeeds.
	probe := func(_ context.Context, _, _, modelID string) (usabilityProbeResult, error) {
		if modelID == "unreachable" {
			return usabilityProbeResult{Verdict: zenChatInconclusive}, errors.New("connection refused")
		}
		return usabilityProbeResult{Verdict: zenChatUsable}, nil
	}

	got := verifyAccountChatUsability(context.Background(), lc, probe, serialPacer(), "http://x", "key", offerings)

	if got.Probed != 1 || got.Usable != 1 {
		t.Fatalf("summary = %+v, want Probed 1 Usable 1 (op-a's transport failure skipped)", got)
	}
	for _, r := range lc.recorded() {
		if r.op == "op-a" {
			t.Fatal("op-a recorded a verdict despite a transport failure")
		}
	}
}

// TestVerifyAccountChatUsability_BoundsInFlightProbesToPacerConcurrency pins the
// per-model worker pool: 8 offerings behind a pacer capped at 4 must run in
// waves of at most 4 — and must genuinely run them in PARALLEL. The barrier
// below is the proof: every probe blocks until 4 of them are simultaneously
// inside the probe, which a sequential loop can never achieve.
func TestVerifyAccountChatUsability_BoundsInFlightProbesToPacerConcurrency(t *testing.T) {
	const total = 8
	want := int32(usabilityProbeMaxConcurrency)

	lc := &fakeCertLifecycle{}
	offerings := numberedOfferings(total)

	var inFlight, maxSeen int32
	waveFull := make(chan struct{})
	var once sync.Once
	probe := func(context.Context, string, string, string) (usabilityProbeResult, error) {
		n := atomic.AddInt32(&inFlight, 1)
		for {
			m := atomic.LoadInt32(&maxSeen)
			if n <= m || atomic.CompareAndSwapInt32(&maxSeen, m, n) {
				break
			}
		}
		if n >= want {
			once.Do(func() { close(waveFull) })
		}
		select {
		case <-waveFull:
		case <-time.After(5 * time.Second):
			// Failsafe only: a pool that never reaches `want` in flight would
			// otherwise deadlock instead of failing the assertion below.
		}
		atomic.AddInt32(&inFlight, -1)
		return usabilityProbeResult{Verdict: zenChatUsable}, nil
	}

	got := verifyAccountChatUsability(
		context.Background(), lc, probe,
		newUsabilityPacer(usabilityProbeMaxConcurrency, nil),
		"http://x", "key", offerings,
	)

	if got.Probed != total || got.Usable != total {
		t.Fatalf("summary = %+v, want Probed %d Usable %d", got, total, total)
	}
	if n := len(lc.recorded()); n != total {
		t.Fatalf("recorded %d attempts, want %d", n, total)
	}
	if m := atomic.LoadInt32(&maxSeen); m != want {
		t.Fatalf("max in-flight probes = %d, want exactly %d (bounded by the pacer AND genuinely parallel)", m, want)
	}
}

// TestVerifyAccountChatUsability_AuthFailureCancelsTheRemainingWaves pins
// stop-on-auth under concurrency: an auth failure inside the FIRST wave must
// stop the account, so the later waves are never started, and the auth verdict
// itself is recorded exactly once.
func TestVerifyAccountChatUsability_AuthFailureCancelsTheRemainingWaves(t *testing.T) {
	const total = 8
	lc := &fakeCertLifecycle{}
	offerings := numberedOfferings(total)

	var calls int32
	probe := func(_ context.Context, _, _, modelID string) (usabilityProbeResult, error) {
		atomic.AddInt32(&calls, 1)
		if modelID == "model-1" {
			return usabilityProbeResult{Verdict: zenChatAuthFailure}, nil
		}
		return usabilityProbeResult{Verdict: zenChatUsable}, nil
	}

	got := verifyAccountChatUsability(
		context.Background(), lc, probe,
		newUsabilityPacer(usabilityProbeMaxConcurrency, nil),
		"http://x", "key", offerings,
	)

	if !got.StoppedOnAuth {
		t.Fatal("StoppedOnAuth = false, want true")
	}
	if got.Probed >= total {
		t.Fatalf("Probed = %d, want fewer than %d (the auth failure must stop the account)", got.Probed, total)
	}
	if c := atomic.LoadInt32(&calls); c > usabilityProbeMaxConcurrency {
		t.Fatalf("probe called %d times, want at most one wave of %d", c, usabilityProbeMaxConcurrency)
	}

	authRecords := 0
	for _, r := range lc.recorded() {
		switch r.op {
		case "op-1":
			authRecords++
		case "op-4", "op-5", "op-6", "op-7":
			t.Fatalf("%s was probed after the auth failure stopped the account", r.op)
		}
	}
	if authRecords != 1 {
		t.Fatalf("auth verdict recorded %d times, want exactly 1", authRecords)
	}
}

// TestVerifyAccountChatUsability_TransportFailureIsNotAPacerSignal pins the
// third pool rule: an unreachable host says nothing about the provider's
// rate-limit window, so a transport failure must feed the pacer NEITHER a
// success nor a rate-limit. It is observable through the consecutive-rate-limit
// streak: with the pacer already two consecutive rate-limits deep, a transport
// failure in between must not reset the streak, so the very next rate-limit is
// still the 3rd and still opens the breaker.
func TestVerifyAccountChatUsability_TransportFailureIsNotAPacerSignal(t *testing.T) {
	lc := &fakeCertLifecycle{}
	clock := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	pacer := newUsabilityPacer(usabilityProbeMaxConcurrency, func() time.Time { return clock })
	pacer.OnRateLimited(0) // 1st consecutive
	pacer.OnRateLimited(0) // 2nd consecutive -> concurrency 1, so the waves below are serial

	offerings := []chatOffering{
		{OfferingOperationID: "op-down", ProviderModelID: "unreachable"},
		{OfferingOperationID: "op-rl", ProviderModelID: "throttled"},
	}
	probe := func(_ context.Context, _, _, modelID string) (usabilityProbeResult, error) {
		if modelID == "unreachable" {
			return usabilityProbeResult{Verdict: zenChatInconclusive}, errors.New("connection refused")
		}
		return usabilityProbeResult{Verdict: zenChatFreeExhausted, RetryAfter: 20 * time.Second}, nil
	}

	verifyAccountChatUsability(context.Background(), lc, probe, pacer, "http://x", "key", offerings)

	if pacer.Admit() {
		t.Fatal("breaker did not open: the transport failure was fed to the pacer and reset the rate-limit streak")
	}
}

// TestVerifyAccountChatUsability_SustainedRateLimitShrinksWavesAndNeverRecordsUnsupported
// pins the pacer feedback loop: an account whose every probe comes back
// free-exhausted must SHRINK (concurrency collapses and the breaker stops
// admitting), and — the honesty invariant — a rate-limit must never be recorded
// as an unsupported verdict.
func TestVerifyAccountChatUsability_SustainedRateLimitShrinksWavesAndNeverRecordsUnsupported(t *testing.T) {
	const total = 8
	lc := &fakeCertLifecycle{}
	offerings := numberedOfferings(total)

	// A frozen clock keeps the breaker's cooldown from elapsing mid-test, so
	// the shrink is deterministic rather than a wall-clock race.
	clock := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	pacer := newUsabilityPacer(usabilityProbeMaxConcurrency, func() time.Time { return clock })

	var calls int32
	probe := func(context.Context, string, string, string) (usabilityProbeResult, error) {
		atomic.AddInt32(&calls, 1)
		return usabilityProbeResult{Verdict: zenChatFreeExhausted, RetryAfter: 20 * time.Second}, nil
	}

	got := verifyAccountChatUsability(context.Background(), lc, probe, pacer, "http://x", "key", offerings)

	if c := atomic.LoadInt32(&calls); c > usabilityProbeMaxConcurrency {
		t.Fatalf("probe called %d times, want at most the first wave of %d (the breaker must refuse the rest)", c, usabilityProbeMaxConcurrency)
	}
	if got.Probed >= total {
		t.Fatalf("Probed = %d, want fewer than %d (refused probes are skipped, not verdicts)", got.Probed, total)
	}
	if got.Usable != 0 {
		t.Fatalf("Usable = %d, want 0", got.Usable)
	}
	if c := pacer.Concurrency(); c > 2 {
		t.Fatalf("pacer concurrency = %d after sustained rate-limits, want it shrunk to <= 2", c)
	}
	for _, r := range lc.recorded() {
		if r.outcome.Truth == models.TruthUnsupported {
			t.Fatalf("%s recorded %+v: a rate-limit must NEVER be recorded as unsupported", r.op, r.outcome)
		}
		if r.outcome.Definitive {
			t.Fatalf("%s recorded a DEFINITIVE outcome %+v for a rate-limit", r.op, r.outcome)
		}
	}
}
