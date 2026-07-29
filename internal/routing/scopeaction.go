package routing

import (
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

// FallbackScope is routing's LOCAL mirror of execution.FailureScope (05 §3).
// internal/routing is staticgate-pure and can never import internal/execution
// (which pulls net/http), so this vocabulary is duplicated here and pinned
// byte-identical to execution's five constants by a sync test in
// internal/httpapi — exactly as providers.TransportKind is pinned to
// execution.TransportType.
type FallbackScope string

const (
	ScopeRequest            FallbackScope = "request"
	ScopeAccount            FallbackScope = "account"
	ScopeOffering           FallbackScope = "offering"
	ScopeProvider           FallbackScope = "provider"
	ScopeTransientTransport FallbackScope = "transient_transport"
)

// FallbackAction is the closed set of next-step actions the fallback loop takes
// for a scoped failure (05 §3).
type FallbackAction int

const (
	// ActionStop: request-scope failure — stop the loop, return the error.
	ActionStop FallbackAction = iota
	// ActionNextAccount: try the next account of the same offering.
	ActionNextAccount
	// ActionNextOffering: try another offering on the same account.
	ActionNextOffering
	// ActionSkipProvider: skip all routes for this provider.
	ActionSkipProvider
	// ActionBoundedRetry: bounded exponential-backoff retry before falling back.
	ActionBoundedRetry
)

// ScopeResolution is ResolveScope's total verdict: the action to take, whether
// a cooldown should be emitted and at which quota scope, and whether the input
// scope was recognized at all (false ⇒ fail-closed default).
type ScopeResolution struct {
	Action        FallbackAction
	Cooldown      bool
	CooldownScope quota.CooldownScope // meaningful only when Cooldown is true
	Recognized    bool
}

// ResolveScope maps a failure scope to its fallback action and cooldown target
// (05 §3), totally and fail-closed:
//   - request → stop, no cooldown.
//   - account → next account; cool the ACCOUNT only.
//   - offering → next offering; cool the OFFERING only.
//   - provider → skip provider; cool the PROVIDER only on cross-account
//     evidence (providerCrossAccount) — a single account's 5xx never trips it.
//   - transient_transport → bounded retry; no cooldown.
//   - anything else → stop, no cooldown, Recognized=false (fail closed — never
//     silently cool a wider scope on an unrecognized value).
//
// It never widens a scope: account/offering/provider each cool their own scope
// and no other.
func ResolveScope(scope FallbackScope, providerCrossAccount bool) ScopeResolution {
	switch scope {
	case ScopeRequest:
		return ScopeResolution{Action: ActionStop, Recognized: true}
	case ScopeAccount:
		return ScopeResolution{Action: ActionNextAccount, Cooldown: true, CooldownScope: quota.CooldownScopeAccount, Recognized: true}
	case ScopeOffering:
		return ScopeResolution{Action: ActionNextOffering, Cooldown: true, CooldownScope: quota.CooldownScopeOffering, Recognized: true}
	case ScopeProvider:
		r := ScopeResolution{Action: ActionSkipProvider, Recognized: true}
		if providerCrossAccount {
			r.Cooldown = true
			r.CooldownScope = quota.CooldownScopeProvider
		}
		return r
	case ScopeTransientTransport:
		return ScopeResolution{Action: ActionBoundedRetry, Recognized: true}
	default:
		return ScopeResolution{Action: ActionStop, Recognized: false}
	}
}

// ProviderFailureEvidence accumulates the DISTINCT accounts that have produced a
// provider-scope failure for one provider, so a provider cooldown trips ONLY on
// cross-account evidence (≥2 distinct accounts — 05 §3's load-bearing rule). A
// single account's repeated 5xx is one distinct account and never cross-account.
type ProviderFailureEvidence struct {
	accounts map[string]struct{}
}

// NewProviderFailureEvidence builds empty evidence.
func NewProviderFailureEvidence() *ProviderFailureEvidence {
	return &ProviderFailureEvidence{accounts: make(map[string]struct{})}
}

// Observe records a provider-scope failure from accountID (idempotent per
// account — observing the same account twice is still one distinct account).
func (e *ProviderFailureEvidence) Observe(accountID string) {
	if e.accounts == nil {
		e.accounts = make(map[string]struct{})
	}
	e.accounts[accountID] = struct{}{}
}

// DistinctAccounts is the number of distinct accounts observed.
func (e *ProviderFailureEvidence) DistinctAccounts() int { return len(e.accounts) }

// CrossAccount reports whether ≥2 distinct accounts have failed — the threshold
// for a provider cooldown.
func (e *ProviderFailureEvidence) CrossAccount() bool { return len(e.accounts) >= 2 }

// TransientMaxRetries bounds the transient_transport retry path (05 §3: "up to
// 3"). The loop retries while its prior retry count is below this.
const TransientMaxRetries = 3

// TransientBaseBackoff is the first transient-retry backoff; each subsequent
// retry doubles it. It is a COMPUTED duration the caller may schedule against —
// this unit never sleeps (05 §3: cooldown/backoff is an eligibility input).
const TransientBaseBackoff = 250 * time.Millisecond

// TransientBackoff returns the exponential backoff for the given retry number
// (1-based): retry 1 → base, 2 → 2×base, 3 → 4×base. It is a value to observe
// or schedule, never a sleep.
func TransientBackoff(retry int) time.Duration {
	if retry < 1 {
		retry = 1
	}
	mult := 1
	for i := 1; i < retry; i++ {
		mult *= 2
	}
	return TransientBaseBackoff * time.Duration(mult)
}

// ShouldRetryTransient reports whether another transient retry is allowed given
// the number of retries already performed (bounded by TransientMaxRetries).
func ShouldRetryTransient(priorRetries int) bool {
	return priorRetries < TransientMaxRetries
}
