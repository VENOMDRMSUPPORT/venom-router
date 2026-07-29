package routing

import (
	"context"
	"errors"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

// P4-WIRE-002 — streaming first-byte fallback boundary.
//
// 05 §3 streaming bullet: fallback may happen ONLY before the first byte
// reaches the client; a router must never emit a second response after
// streaming has begun (docs/11 risk row R-12). ExecOutcome.StreamStarted is the
// signal a failed attempt carries when a chunk already streamed; the loop must
// then stop — even when the scope action would otherwise steer to another route.

// sbTwoGroupPool builds a pool with two healthy single-account groups on
// distinct providers: P1/M1 (a1) and P2/M2 (a2). If a1 is skipped or blocked,
// a2 is the sole remaining fallback target.
func sbTwoGroupPool() RoutePool {
	return RoutePool{Groups: []RouteGroup{
		wireGroup("P1", "M1", wireCand("a1", "P1", "M1")),
		wireGroup("P2", "M2", wireCand("a2", "P2", "M2")),
	}}
}

// sbScript is the shared fixture for the boundary test and its positive control:
// attempt 1 fails with an ACCOUNT scope (a non-stopping action that trips a1 and
// would normally fall back), attempt 2 is a clean success on a2. The ONLY
// difference between the two tests is attempt 1's StreamStarted flag.
func sbScript(firstByteStarted bool) []scriptedAttempt {
	return []scriptedAttempt{
		{
			outcome: ExecOutcome{Err: errRetryable, StreamStarted: firstByteStarted},
			verdict: VerdictUnknownConsumption,
			scope:   ScopeAccount,
		},
		{outcome: successOutcome(), verdict: VerdictSuccess, scope: ScopeRequest},
	}
}

// TestRunFallbackLoop_NoFallbackAfterFirstByte proves that once a streaming
// response has begun, a mid-stream failure stops the loop: no second attempt is
// made and no second response is produced, even though a healthy fallback group
// exists and the scope action (account) would otherwise steer onward.
//
// Mutation W2-M1: delete the StreamStarted stop branch → the loop falls back to
// a2 and succeeds → execCalls==2, this test RED.
// Mutation W2-M2: move the stop branch BEFORE the reconcile switch → the single
// terminal reconcile op never runs → the terminal-op assertion RED.
func TestRunFallbackLoop_NoFallbackAfterFirstByte(t *testing.T) {
	h := &fakeHarness{script: sbScript(true)}
	in := wireInput(t, TierMax, sbTwoGroupPool(), h) // budget 5 > 2, so only the boundary stops it

	res, err := RunFallbackLoop(context.Background(), in)

	if !errors.Is(err, errRetryable) {
		t.Fatalf("a mid-stream failure must return attempt 1's error; got %v", err)
	}
	if h.execCalls != 1 {
		t.Fatalf("no fallback after first byte: want exactly 1 execute, got %d", h.execCalls)
	}
	if res.Attempts != 1 {
		t.Fatalf("want Attempts==1 after the first-byte stop, got %d", res.Attempts)
	}
	if res.Response != nil {
		t.Fatalf("no second response may be produced after streaming began; got %v", res.Response)
	}
	// The exactly-one terminal reconcile op for the failed attempt still ran.
	terminal := h.settle + h.settleEstimate + h.release + h.markPending
	if terminal != h.execCalls {
		t.Fatalf("the terminal reconcile op must still run exactly once; terminal=%d execCalls=%d", terminal, h.execCalls)
	}
	if h.markPending != 1 {
		t.Fatalf("the unknown-consumption verdict must mark reconciliation pending once; got %d", h.markPending)
	}
}

// TestRunFallbackLoop_FallbackStillHappensBeforeFirstByte is the POSITIVE
// CONTROL: the identical fixture with StreamStarted==false must fall back and
// succeed on attempt 2. Without it, the test above could pass for a trivial
// reason (e.g. the loop always stopping).
//
// Mutation W2-M3: make the stop unconditional (ignore the flag) → the loop stops
// at attempt 1 and never reaches a2 → this test RED.
func TestRunFallbackLoop_FallbackStillHappensBeforeFirstByte(t *testing.T) {
	h := &fakeHarness{script: sbScript(false)}
	in := wireInput(t, TierMax, sbTwoGroupPool(), h)

	res, err := RunFallbackLoop(context.Background(), in)
	if err != nil {
		t.Fatalf("before the first byte the loop must fall back and succeed; got %v", err)
	}
	if h.execCalls != 2 {
		t.Fatalf("want fallback to a second attempt, got %d execute calls", h.execCalls)
	}
	if res.AccountID != "a2" {
		t.Fatalf("fallback must land on the surviving account a2, got %q", res.AccountID)
	}
}

// TestRunFallbackLoop_FirstByteStopStillRecordsScopeState proves the stop does
// not skip the scope-classified steering: a mid-stream ACCOUNT-scope failure
// still trips the account breaker and emits an account cooldown before the loop
// halts — a mid-stream failure is real failure evidence.
//
// Mutation W2-M4: skip the scope-steering block when StreamStarted → neither the
// breaker trip nor the cooldown is recorded → this test RED.
func TestRunFallbackLoop_FirstByteStopStillRecordsScopeState(t *testing.T) {
	h := &fakeHarness{script: []scriptedAttempt{{
		outcome: ExecOutcome{Err: errRetryable, StreamStarted: true},
		verdict: VerdictUnknownConsumption,
		scope:   ScopeAccount,
	}}}
	pool := RoutePool{Groups: []RouteGroup{wireGroup("P1", "M1", wireCand("a1", "P1", "M1"))}}
	in := wireInput(t, TierMax, pool, h)

	res, err := RunFallbackLoop(context.Background(), in)
	if !errors.Is(err, errRetryable) {
		t.Fatalf("the mid-stream failure must be returned; got %v", err)
	}
	if h.execCalls != 1 {
		t.Fatalf("the loop must stop after the first byte; execCalls=%d", h.execCalls)
	}
	if b := res.Breakers.Account["a1"]; b.State != BreakerOpen {
		t.Fatalf("the account breaker must be tripped by the mid-stream failure; got state=%q", b.State)
	}
	if !hasCooldownScope(res.Cooldowns, quota.CooldownScopeAccount) {
		t.Fatalf("an account cooldown must be emitted even though the loop stopped; got %v", res.Cooldowns)
	}
}
