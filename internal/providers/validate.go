package providers

import (
	"context"
	"strings"
)

// ChatProbe performs the authentic-validation HTTP probe 03 §1/§5
// requires: a zero-cost POST to the provider's chat-completions
// endpoint with max_tokens=1 — deliberately NOT a mere host-up
// GET /v1/models check, which returns 200 for any token on some
// providers and would let an invalid key pass as "connected."
// internal/providers holds no net/http import (01 §3/§8 layering); the
// concrete HTTP implementation is supplied by the caller
// (internal/accounts/application, P2b-PROV-005) and injected here as
// this function type. It must never log key.
type ChatProbe func(ctx context.Context, baseURL, key string) (statusCode int, err error)

// ValidationStatus is ValidateAPIKey's 3-way classification (03 §1).
type ValidationStatus string

const (
	// ValidationValid means a successful authenticated response — the
	// key genuinely authenticates.
	ValidationValid ValidationStatus = "valid"
	// ValidationInvalid means a genuine authentication failure (401/403
	// with auth semantics) — the key does not authenticate.
	ValidationInvalid ValidationStatus = "invalid"
	// ValidationUnavailable means the provider could not be reached or
	// answered ambiguously (429, 5xx, a transport error, or any other
	// unrecognized status) — retryable, and specifically NEVER treated
	// as either valid or invalid. This is the fail-safe default: an
	// unrecognized outcome must never be mistaken for a working key.
	ValidationUnavailable ValidationStatus = "unavailable"
)

// NormalizeAPIKey trims surrounding whitespace and collapses internal
// whitespace runs. Both the fingerprint (P2b-PROV-003) and the
// validation probe (this unit) operate on this normalized form, so two
// keys differing only in incidental whitespace are treated identically.
func NormalizeAPIKey(key string) string {
	return strings.Join(strings.Fields(key), " ")
}

// ValidateAPIKey classifies the outcome of ONE authentic-validation
// probe call against baseURL with key (03 §1's rule):
//
//   - a transport-level error (probe returned err != nil: DNS failure,
//     timeout, connection refused) ⇒ ValidationUnavailable — never
//     invalid, since the failure says nothing about the key itself.
//   - HTTP 401 or 403 (a genuine auth rejection) ⇒ ValidationInvalid.
//   - HTTP 429 or any 5xx (rate-limited or a provider-side failure)
//     ⇒ ValidationUnavailable, retryable — NOT invalid.
//   - HTTP 2xx (a successful authenticated response) ⇒ ValidationValid.
//   - anything else (an unrecognized/ambiguous status) ⇒
//     ValidationUnavailable — the fail-safe default; an ambiguous
//     result is never classified as valid.
//
// key is passed to probe and never otherwise inspected, logged, or
// included in the returned status.
func ValidateAPIKey(ctx context.Context, probe ChatProbe, baseURL, key string) ValidationStatus {
	status, err := probe(ctx, baseURL, NormalizeAPIKey(key))
	if err != nil {
		return ValidationUnavailable
	}

	switch {
	case status == 401 || status == 403:
		return ValidationInvalid
	case status == 429 || (status >= 500 && status <= 599):
		return ValidationUnavailable
	case status >= 200 && status < 300:
		return ValidationValid
	default:
		return ValidationUnavailable
	}
}
