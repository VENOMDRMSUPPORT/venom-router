package httpapi

import (
	"errors"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/execution"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/routing"
)

// TestFallbackScopeVocabularySyncWithFailureScope pins routing's local
// FallbackScope vocabulary byte-identical to execution's FailureScope — the two
// are intentionally duplicated (routing may never import execution) and this is
// the drift guard, mirroring TestTransportKindVocabularySyncWithTransportType.
func TestFallbackScopeVocabularySyncWithFailureScope(t *testing.T) {
	pairs := []struct {
		rs   routing.FallbackScope
		es   execution.FailureScope
		name string
	}{
		{routing.ScopeRequest, execution.FailureScopeRequest, "request"},
		{routing.ScopeAccount, execution.FailureScopeAccount, "account"},
		{routing.ScopeOffering, execution.FailureScopeOffering, "offering"},
		{routing.ScopeProvider, execution.FailureScopeProvider, "provider"},
		{routing.ScopeTransientTransport, execution.FailureScopeTransientTransport, "transient_transport"},
	}
	for _, p := range pairs {
		if string(p.rs) != string(p.es) {
			t.Errorf("vocabulary drift: routing.FallbackScope(%q)=%q, execution.FailureScope(%q)=%q — must be identical",
				p.name, p.rs, p.name, p.es)
		}
	}
}

// TestClassifier_FailClosedNeverSuccess is the REQUIRED fail-closed test: for
// every execution.FailureScope value, plus the empty and a bogus scope,
// Classify must NEVER return VerdictSuccess, and the empty scope specifically
// must return VerdictUnknownConsumption (headroom stays debited).
//
// Mutation row R14-M8: map the empty/unknown scope to VerdictSuccess → this
// test RED. (This is the exact ROUTE-013 fail-open defect.)
func TestClassifier_FailClosedNeverSuccess(t *testing.T) {
	scopes := []execution.FailureScope{
		execution.FailureScopeRequest,
		execution.FailureScopeAccount,
		execution.FailureScopeOffering,
		execution.FailureScopeProvider,
		execution.FailureScopeTransientTransport,
		execution.FailureScope(""),
		execution.FailureScope("totally-bogus"),
	}
	for _, sc := range scopes {
		// Vary the class too, since the mapping consults it.
		for _, class := range []execution.FailureClass{execution.FailureClassServer, execution.FailureClassAuth, ""} {
			v := VerdictForTypedFailure(execution.TypedFailure{Scope: sc, FailureClass: class})
			if v == routing.VerdictSuccess {
				t.Fatalf("scope %q class %q mapped to VerdictSuccess — fail-open defect", sc, class)
			}
		}
	}
	if got := VerdictForTypedFailure(execution.TypedFailure{Scope: ""}); got != routing.VerdictUnknownConsumption {
		t.Fatalf("empty scope = %v, want VerdictUnknownConsumption", got)
	}
	if got := VerdictForTypedFailure(execution.TypedFailure{Scope: execution.FailureScope("bogus")}); got != routing.VerdictUnknownConsumption {
		t.Fatalf("unknown scope = %v, want VerdictUnknownConsumption", got)
	}
}

// TestClassifier_PreConsumptionVsUnknown proves the criterion: a definite
// pre-generation rejection (auth/not_found/invalid/quota/rate_limit) is
// pre-consumption; a server/network failure that may have reached the model is
// unknown-consumption.
func TestClassifier_PreConsumptionVsUnknown(t *testing.T) {
	preConsumption := []execution.FailureClass{
		execution.FailureClassAuth, execution.FailureClassNotFound,
		execution.FailureClassInvalidRequest, execution.FailureClassQuota,
		execution.FailureClassRateLimit,
	}
	for _, class := range preConsumption {
		v := VerdictForTypedFailure(execution.TypedFailure{Scope: execution.FailureScopeAccount, FailureClass: class})
		if v != routing.VerdictPreConsumptionFailure {
			t.Errorf("account/%s should be pre-consumption, got %v", class, v)
		}
	}
	for _, class := range []execution.FailureClass{execution.FailureClassServer, execution.FailureClassNetwork} {
		v := VerdictForTypedFailure(execution.TypedFailure{Scope: execution.FailureScopeProvider, FailureClass: class})
		if v != routing.VerdictUnknownConsumption {
			t.Errorf("provider/%s should be unknown-consumption, got %v", class, v)
		}
	}
}

// TestClassifier_RequestScope proves request scope maps to VerdictRequestScope
// regardless of class.
func TestClassifier_RequestScope(t *testing.T) {
	for _, class := range []execution.FailureClass{execution.FailureClassInvalidRequest, execution.FailureClassServer} {
		if got := VerdictForTypedFailure(execution.TypedFailure{Scope: execution.FailureScopeRequest, FailureClass: class}); got != routing.VerdictRequestScope {
			t.Errorf("request/%s = %v, want VerdictRequestScope", class, got)
		}
	}
}

// TestClassifier_Unscoped429IsTransientNotWidened feeds a real unscoped 429
// through execution's OWN classifier and asserts it stays transient_transport —
// NOT widened to an account/provider cooldown — both in execution's scope and
// in routing's action mapping.
//
// Mutation row R14-M3: widen an unscoped 429 to account scope (in routing's
// ResolveScope transient case) → the action assertion RED.
func TestClassifier_Unscoped429IsTransientNotWidened(t *testing.T) {
	tf := execution.ClassifyFailure("", "", nil, nil, 429)
	if tf.Scope != execution.FailureScopeTransientTransport {
		t.Fatalf("execution widened an unscoped 429 to %q, want transient_transport", tf.Scope)
	}
	res := routing.ResolveScope(routing.FallbackScope(tf.Scope), false)
	if res.Action != routing.ActionBoundedRetry {
		t.Fatalf("unscoped 429 action = %v, want ActionBoundedRetry (not a cooldown path)", res.Action)
	}
	if res.Cooldown {
		t.Fatalf("unscoped 429 must not cool any scope; cooled %q", res.CooldownScope)
	}
	// A rate-limit reached the provider but generated nothing → pre-consumption.
	if got := VerdictForTypedFailure(tf); got != routing.VerdictPreConsumptionFailure {
		t.Fatalf("unscoped 429 verdict = %v, want VerdictPreConsumptionFailure", got)
	}
}

// TestScopeClassifier_UsesInjectedClassify proves the adapter satisfies
// routing.FailureClassifier and routes err → TypedFailure → verdict through the
// injected classify function.
func TestScopeClassifier_UsesInjectedClassify(t *testing.T) {
	var seen error
	planted := errors.New("boom")
	c := NewScopeClassifier(func(err error) execution.TypedFailure {
		seen = err
		return execution.TypedFailure{Scope: execution.FailureScopeRequest}
	})
	var _ routing.FailureClassifier = c

	got := c.Classify(planted)
	if !errors.Is(seen, planted) {
		t.Fatalf("adapter did not pass the error to the injected classify fn")
	}
	if got != routing.VerdictRequestScope {
		t.Fatalf("Classify = %v, want VerdictRequestScope", got)
	}
}
