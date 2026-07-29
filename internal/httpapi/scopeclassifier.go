package httpapi

import (
	"github.com/VENOMDRMSUPPORT/venom-router/internal/execution"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/routing"
)

// scopeclassifier.go is the P4-ROUTE-014 composition-layer adapter that maps
// execution's failure taxonomy onto routing's reconcile vocabulary. It lives in
// internal/httpapi — NOT internal/routing — because it must import
// internal/execution, which transitively pulls net/http; internal/routing is
// staticgate-pure and can never do that. This mirrors how BuildInferenceDispatcher
// composes execution for EXEC-001.

// ScopeClassifier implements routing.FailureClassifier by turning an execution
// error into an execution.TypedFailure (via an injected classify function that
// wraps the resolved transport's Failure(err, route) — supplied by the P5 wiring
// layer) and mapping its scope onto a routing.ReconcileVerdict.
type ScopeClassifier struct {
	classify func(err error) execution.TypedFailure
}

// NewScopeClassifier builds a classifier from a function that normalizes an
// error to a TypedFailure. In production the wiring layer passes a closure over
// the resolved transport's Failure(err, route); tests pass a fake.
func NewScopeClassifier(classify func(err error) execution.TypedFailure) *ScopeClassifier {
	return &ScopeClassifier{classify: classify}
}

// Classify normalizes err and maps the resulting scope to a verdict.
func (c *ScopeClassifier) Classify(err error) routing.ReconcileVerdict {
	return VerdictForTypedFailure(c.classify(err))
}

// ScopeOf normalizes err and returns its routing.FallbackScope (P4-WIRE-001).
// routing.FallbackScope is pinned byte-identical to execution.FailureScope by
// TestFallbackScopeVocabularySyncWithFailureScope, so this cast is total and
// safe: an unrecognized value simply resolves to ResolveScope's fail-closed
// default at the routing layer.
func (c *ScopeClassifier) ScopeOf(err error) routing.FallbackScope {
	return routing.FallbackScope(c.classify(err).Scope)
}

var (
	_ routing.FailureClassifier = (*ScopeClassifier)(nil)
	_ routing.FailureScoper     = (*ScopeClassifier)(nil)
)

// VerdictForTypedFailure maps a TypedFailure onto a routing.ReconcileVerdict,
// TOTALLY and FAIL-CLOSED (P4-ROUTE-014; the ROUTE-013 fail-open defect must
// never recur):
//   - request → VerdictRequestScope (stop the loop).
//   - account/offering/provider/transient_transport → VerdictPreConsumptionFailure
//     when the failure is a definite pre-generation rejection, else
//     VerdictUnknownConsumption when the request may have reached the model and
//     consumed tokens (see mightBeConsumed).
//   - anything else, INCLUDING the empty scope → VerdictUnknownConsumption
//     (headroom stays debited). NEVER VerdictSuccess, and never
//     VerdictPreConsumptionFailure on a guess (which would free a reservation).
func VerdictForTypedFailure(tf execution.TypedFailure) routing.ReconcileVerdict {
	switch tf.Scope {
	case execution.FailureScopeRequest:
		return routing.VerdictRequestScope
	case execution.FailureScopeAccount,
		execution.FailureScopeOffering,
		execution.FailureScopeProvider,
		execution.FailureScopeTransientTransport:
		if mightBeConsumed(tf.FailureClass) {
			return routing.VerdictUnknownConsumption
		}
		return routing.VerdictPreConsumptionFailure
	default:
		return routing.VerdictUnknownConsumption
	}
}

// mightBeConsumed decides pre-consumption vs unknown-consumption from the
// TypedFailure's own class field — never per-scope guesswork. A DEFINITE
// pre-generation rejection (the provider refused before generating: auth,
// not_found, invalid_request, quota, rate_limit) consumed nothing → safe to
// release. A server outage or network fault may have reached the model and
// produced tokens before failing → unknown-consumption (fail closed, keep
// headroom). An unrecognized/empty class fails closed to "might".
//
// This also gives the transient_transport criterion for free: a 429
// (rate_limit) reached the provider but generated nothing → pre-consumption; a
// network timeout (could have reached the model) → unknown.
func mightBeConsumed(class execution.FailureClass) bool {
	switch class {
	case execution.FailureClassAuth,
		execution.FailureClassNotFound,
		execution.FailureClassInvalidRequest,
		execution.FailureClassQuota,
		execution.FailureClassRateLimit:
		return false
	case execution.FailureClassServer, execution.FailureClassNetwork:
		return true
	default:
		return true
	}
}
