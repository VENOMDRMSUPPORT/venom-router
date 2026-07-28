package execution

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// AdapterRule is one entry in a transport's adapter-specific override
// table (01 §4.2 rung 3): a per-transport code→TypedFailure mapping for
// provider-specific error codes the standard 4-rung ladder does not
// recognize. The first matching rule wins; nil/empty table is valid.
type AdapterRule struct {
	Code   string
	Result TypedFailure
}

// safeMessageFor returns a user-safe, class-appropriate message for the
// given FailureClass. It never contains raw provider text.
func safeMessageFor(fc FailureClass) string {
	if fc == FailureClassInvalidRequest {
		return "the request was invalid"
	}
	if fc == FailureClassAuth {
		return "the credential was rejected"
	}
	if fc == FailureClassNotFound {
		return "the model was not found"
	}
	if fc == FailureClassQuota {
		return "account quota has been exhausted"
	}
	if fc == FailureClassRateLimit {
		return "the provider rate-limited this request"
	}
	if fc == FailureClassServer {
		return "the provider encountered an error"
	}
	if fc == FailureClassNetwork {
		return "a network error occurred"
	}
	return "the provider rejected the request"
}

// retryableFor returns the spec-mandated retryability for each
// FailureClass on the ordinary routing path (not the probe path).
func retryableFor(fc FailureClass) bool {
	// auth and server/rate/network are retryable; quota/invalid/not-found are not
	if fc == FailureClassAuth {
		return true
	}
	if fc == FailureClassServer {
		return true
	}
	if fc == FailureClassNetwork {
		return true
	}
	if fc == FailureClassRateLimit {
		return true // retryable only after cooldown
	}
	return false
}

// sanitizedEvidence builds TypedFailure.Evidence (01 §4.2): identifiers
// and enumerations ONLY — the HTTP status, the provider's error CODE
// (an enum, never free text), and which rung produced the scope. Raw
// provider message text never enters this map; RawMessage is its only
// sanctioned carrier.
func sanitizedEvidence(status int, code, scopeSource string) map[string]any {
	ev := map[string]any{
		"http_status":  status,
		"scope_source": scopeSource,
	}
	if code != "" {
		ev["provider_code"] = code
	}
	return ev
}

// providerCodeMatch is the result of a rung-1 classification.
type providerCodeMatch struct {
	class     FailureClass
	scope     FailureScope
	retryable bool
}

// classifyProviderCode attempts rung-1 classification (01 §4.2):
// maps a provider-reported semantic code + explicit scope hint onto a
// providerCodeMatch. scope is the combined body-level + header-level
// scope (may be empty). Returns (match, ok); ok=false means the code is
// unrecognized and rung 3/4 should be attempted.
// Uses if/else, never a switch on string literals (noslugswitch).
func classifyProviderCode(code, scope string) (providerCodeMatch, bool) {
	if code == "context_length_exceeded" {
		return providerCodeMatch{FailureClassInvalidRequest, FailureScopeRequest, false}, true
	}
	if code == "invalid_api_key" {
		// retryable=true: caller retries after credential refresh (01 §4.2 row 2)
		return providerCodeMatch{FailureClassAuth, FailureScopeAccount, true}, true
	}
	if code == "model_not_found" {
		return providerCodeMatch{FailureClassNotFound, FailureScopeOffering, false}, true
	}
	if code == "quota_exhausted" {
		if scope == "model" || scope == "offering" {
			// rate_limit/offering: cooldown the offering — not retryable directly
			return providerCodeMatch{FailureClassRateLimit, FailureScopeOffering, false}, true
		}
		// scope=account or empty: account quota exhausted — cooldown until reset
		return providerCodeMatch{FailureClassQuota, FailureScopeAccount, false}, true
	}
	return providerCodeMatch{}, false
}

// parseRetryAfter parses the value of a Retry-After header. The value
// may be either a decimal number of seconds (integer) or an HTTP-date
// (RFC 1123 / RFC 850 / ANSIC). Returns nil for both on unrecognized input.
func parseRetryAfter(value string, now time.Time) (seconds *int, until *time.Time) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if n, err := strconv.Atoi(value); err == nil {
		t := now.Add(time.Duration(n) * time.Second)
		secs := n
		return &secs, &t
	}
	for _, layout := range []string{time.RFC1123, time.RFC850, time.ANSIC} {
		if t, err := time.Parse(layout, value); err == nil {
			secs := int(t.Sub(now).Seconds())
			if secs < 0 {
				secs = 0
			}
			return &secs, &t
		}
	}
	return nil, nil
}

// applyHeaderEnrichment updates time-related fields on f from the
// response headers: Retry-After → RetryAfter/CooldownUntil, X-Quota-Reset
// → QuotaResetAt. It does NOT re-apply X-RateLimit-Scope (that is merged
// into the scope parameter before ClassifyFailure is called so the class
// selection above can use it). Safe to call with nil headers.
func applyHeaderEnrichment(f *TypedFailure, headers http.Header, now time.Time) {
	if headers == nil {
		return
	}
	if ra := headers.Get("Retry-After"); ra != "" {
		secs, until := parseRetryAfter(ra, now)
		f.RetryAfter = secs
		f.CooldownUntil = until
	}
	if qr := headers.Get("X-Quota-Reset"); qr != "" {
		qr = strings.TrimSpace(qr)
		if n, err := strconv.ParseInt(qr, 10, 64); err == nil {
			t := time.Unix(n, 0).UTC()
			f.QuotaResetAt = &t
		} else {
			for _, layout := range []string{time.RFC1123, time.RFC850, time.ANSIC} {
				if t, err := time.Parse(layout, qr); err == nil {
					f.QuotaResetAt = &t
					break
				}
			}
		}
	}
}

// applyDefaultScope assigns Scope and Retryable for rung-4 HTTP
// fallback, based purely on the integer status code (no string comparisons).
// The 429-without-scope row is conservative: transient_transport + retryable.
func applyDefaultScope(f *TypedFailure) {
	switch {
	case f.HTTPStatus == 401 || f.HTTPStatus == 403:
		f.Scope = FailureScopeAccount
		f.Retryable = true // retry after credential refresh
	case f.HTTPStatus == 404:
		f.Scope = FailureScopeOffering
		f.Retryable = false
	case f.HTTPStatus == 429:
		// No scope signal in rung 1 or 2 → conservative transient_transport
		f.Scope = FailureScopeTransientTransport
		f.Retryable = true
	case f.HTTPStatus >= 500:
		f.Scope = FailureScopeProvider
		f.Retryable = true
	default:
		f.Scope = FailureScopeRequest
		f.Retryable = false
	}
}

// ClassifyFailure applies the 4-rung failure classification ladder
// (01 §4.2, priority: semantic code → headers → adapter rules → HTTP
// fallback). It never reads rawMessage — that field's content is
// exclusively for the probe path and must never influence routing decisions.
//
// code: provider-reported error code (may be empty).
// scope: explicit scope hint from the response body (may be empty); if
//
//	empty, X-RateLimit-Scope header is checked as a fallback.
//
// headers: response HTTP headers (nil treated as empty).
// rules: adapter-specific override table (nil/empty = no override).
// status: HTTP status code (0 for non-HTTP errors).
func ClassifyFailure(code, scope string, headers http.Header, rules []AdapterRule, status int) TypedFailure {
	now := time.Now()

	// Merge body scope with X-RateLimit-Scope header (body wins).
	mergedScope := scope
	if mergedScope == "" && headers != nil {
		if rlScope := strings.TrimSpace(headers.Get("X-RateLimit-Scope")); rlScope != "" {
			mergedScope = rlScope
		}
	}

	// Rung 1: provider semantic codes — first match wins.
	if code != "" {
		if m, ok := classifyProviderCode(code, mergedScope); ok {
			f := TypedFailure{
				FailureClass: m.class,
				Scope:        m.scope,
				Retryable:    m.retryable,
				ProviderCode: code,
				HTTPStatus:   status,
				SafeMessage:  safeMessageFor(m.class),
				Evidence:     sanitizedEvidence(status, code, "provider_code"),
			}
			applyHeaderEnrichment(&f, headers, now)
			return f
		}
	}

	// Rung 2: 429 with explicit scope from headers but no recognized
	// semantic code. The scope signal changes both FailureClass and Scope
	// (account quota vs model rate-limit vs conservative transient).
	if status == 429 && mergedScope != "" {
		if mergedScope == "account" {
			f := TypedFailure{
				FailureClass: FailureClassQuota,
				Scope:        FailureScopeAccount,
				Retryable:    false,
				ProviderCode: code,
				HTTPStatus:   status,
				SafeMessage:  safeMessageFor(FailureClassQuota),
				Evidence:     sanitizedEvidence(status, code, "scope_signal"),
			}
			applyHeaderEnrichment(&f, headers, now)
			return f
		}
		if mergedScope == "model" || mergedScope == "offering" {
			f := TypedFailure{
				FailureClass: FailureClassRateLimit,
				Scope:        FailureScopeOffering,
				Retryable:    false,
				ProviderCode: code,
				HTTPStatus:   status,
				SafeMessage:  safeMessageFor(FailureClassRateLimit),
				Evidence:     sanitizedEvidence(status, code, "scope_signal"),
			}
			applyHeaderEnrichment(&f, headers, now)
			return f
		}
	}

	// Rung 3: adapter-specific rules.
	for _, rule := range rules {
		if rule.Code == code && code != "" {
			f := rule.Result
			if f.HTTPStatus == 0 {
				f.HTTPStatus = status
			}
			if f.ProviderCode == "" {
				f.ProviderCode = code
			}
			if f.Evidence == nil {
				f.Evidence = sanitizedEvidence(f.HTTPStatus, f.ProviderCode, "adapter_rule")
			}
			applyHeaderEnrichment(&f, headers, now)
			return f
		}
	}

	// Rung 4: HTTP status fallback — classifyHTTPStatus provides the class
	// (switches on int, not strings), applyDefaultScope fills in scope +
	// retryability per the spec table.
	fc := classifyHTTPStatus(status)
	f := TypedFailure{
		FailureClass: fc,
		HTTPStatus:   status,
		ProviderCode: code,
		SafeMessage:  safeMessageFor(fc),
		Evidence:     sanitizedEvidence(status, code, "http_status"),
	}
	applyDefaultScope(&f)
	applyHeaderEnrichment(&f, headers, now)
	return f
}
