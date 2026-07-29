package routing

import (
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

// TestResolveScope_ActionsAndCooldownTargets asserts the exact action and the
// exact cooldown target for each scope (05 §3). Account/offering scope each
// carry a positive control (the right cooldown scope) plus the negatives (they
// never cool a wider scope).
//
// Mutation row R14-M1: make account-scope also cool the provider → the
// account-scope negative (CooldownScope must be account, not provider) RED.
func TestResolveScope_ActionsAndCooldownTargets(t *testing.T) {
	tests := []struct {
		scope          FallbackScope
		crossAccount   bool
		wantAction     FallbackAction
		wantCooldown   bool
		wantCDScope    quota.CooldownScope
		wantRecognized bool
	}{
		{ScopeRequest, false, ActionStop, false, "", true},
		{ScopeAccount, false, ActionNextAccount, true, quota.CooldownScopeAccount, true},
		{ScopeOffering, false, ActionNextOffering, true, quota.CooldownScopeOffering, true},
		{ScopeProvider, false, ActionSkipProvider, false, "", true}, // no cross-account evidence
		{ScopeProvider, true, ActionSkipProvider, true, quota.CooldownScopeProvider, true},
		{ScopeTransientTransport, false, ActionBoundedRetry, false, "", true},
		{FallbackScope("bogus"), false, ActionStop, false, "", false}, // fail closed
	}
	for _, tc := range tests {
		got := ResolveScope(tc.scope, tc.crossAccount)
		if got.Action != tc.wantAction {
			t.Errorf("scope %q: action=%v want %v", tc.scope, got.Action, tc.wantAction)
		}
		if got.Cooldown != tc.wantCooldown {
			t.Errorf("scope %q: cooldown=%v want %v", tc.scope, got.Cooldown, tc.wantCooldown)
		}
		if got.CooldownScope != tc.wantCDScope {
			t.Errorf("scope %q: cooldownScope=%q want %q", tc.scope, got.CooldownScope, tc.wantCDScope)
		}
		if got.Recognized != tc.wantRecognized {
			t.Errorf("scope %q: recognized=%v want %v", tc.scope, got.Recognized, tc.wantRecognized)
		}
	}
}

// TestResolveScope_AccountNeverCoolsOfferingOrProvider is the explicit negative
// control for the account scope: it cools the account and ONLY the account.
func TestResolveScope_AccountNeverCoolsOfferingOrProvider(t *testing.T) {
	r := ResolveScope(ScopeAccount, true) // even with cross-account evidence present
	if r.CooldownScope == quota.CooldownScopeOffering || r.CooldownScope == quota.CooldownScopeProvider {
		t.Fatalf("account scope cooled a wider scope: %q", r.CooldownScope)
	}
	if r.CooldownScope != quota.CooldownScopeAccount {
		t.Fatalf("account scope must cool the account, got %q", r.CooldownScope)
	}
}

// TestProviderFailureEvidence_CrossAccount proves a single account's failure
// does NOT constitute cross-account evidence, but a second distinct account
// does — the load-bearing provider-cooldown rule (05 §3).
//
// Mutation row R14-M2: trip on the first account (CrossAccount uses >= 1) →
// this test RED.
func TestProviderFailureEvidence_CrossAccount(t *testing.T) {
	e := NewProviderFailureEvidence()
	if e.CrossAccount() {
		t.Fatalf("empty evidence must not be cross-account")
	}
	e.Observe("acc-1")
	if e.CrossAccount() {
		t.Fatalf("a single account's failure must NOT be cross-account evidence")
	}
	e.Observe("acc-1") // same account again — still one distinct
	if e.CrossAccount() {
		t.Fatalf("repeat of the same account is still one distinct account")
	}
	e.Observe("acc-2")
	if !e.CrossAccount() {
		t.Fatalf("two distinct accounts must be cross-account evidence")
	}
	if e.DistinctAccounts() != 2 {
		t.Fatalf("distinct accounts = %d, want 2", e.DistinctAccounts())
	}
}

// TestProviderCooldownRequiresCrossAccount ties the evidence to ResolveScope:
// one account's provider-scope 5xx does not trip a provider cooldown; a second
// distinct account does.
func TestProviderCooldownRequiresCrossAccount(t *testing.T) {
	e := NewProviderFailureEvidence()
	e.Observe("acc-1")
	if ResolveScope(ScopeProvider, e.CrossAccount()).Cooldown {
		t.Fatalf("single-account 5xx must not trip a provider cooldown")
	}
	e.Observe("acc-2")
	if !ResolveScope(ScopeProvider, e.CrossAccount()).Cooldown {
		t.Fatalf("cross-account 5xx must trip a provider cooldown")
	}
}

// TestTransientBackoff_BoundedExponential proves the transient-transport retry
// path is a bounded (<=3), exponentially-growing COMPUTED duration — never a
// sleep.
func TestTransientBackoff_BoundedExponential(t *testing.T) {
	if TransientMaxRetries != 3 {
		t.Fatalf("transient retries must be bounded at 3, got %d", TransientMaxRetries)
	}
	b1, b2, b3 := TransientBackoff(1), TransientBackoff(2), TransientBackoff(3)
	if b1 >= b2 || b2 >= b3 {
		t.Fatalf("backoff must grow: %v %v %v", b1, b2, b3)
	}
	if b2 != 2*b1 || b3 != 4*b1 {
		t.Fatalf("backoff must double each retry: %v %v %v", b1, b2, b3)
	}
	if !ShouldRetryTransient(2) || ShouldRetryTransient(3) {
		t.Fatalf("must retry while prior retries < 3; ShouldRetryTransient(2)=%v (3)=%v", ShouldRetryTransient(2), ShouldRetryTransient(3))
	}
	_ = time.Second
}
