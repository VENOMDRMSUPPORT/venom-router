package execution

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestClassifyFailure_NineSpecRows is the binding-law test: every row
// in 01 §4.2's failure table must be covered. A transport that skips a
// row (e.g. silently maps quota_exhausted/account to rate_limit) breaks
// the tier engine's cooldown logic. Mutation row: change any expected
// FailureClass or FailureScope → RED; restore → GREEN.
func TestClassifyFailure_NineSpecRows(t *testing.T) {
	cases := []struct {
		name          string
		code          string
		scope         string
		headers       http.Header
		status        int
		wantClass     FailureClass
		wantScope     FailureScope
		wantRetryable bool
	}{
		// Row 1: Invalid prompt / context length exceeded
		{
			name: "context_length_exceeded → invalid_request/request/not-retryable",
			code: "context_length_exceeded", status: 400,
			wantClass: FailureClassInvalidRequest, wantScope: FailureScopeRequest, wantRetryable: false,
		},
		// Row 2: Invalid/expired credential (semantic code)
		{
			name: "invalid_api_key → auth_error/account/retryable",
			code: "invalid_api_key", status: 401,
			wantClass: FailureClassAuth, wantScope: FailureScopeAccount, wantRetryable: true,
		},
		// Row 3: Model missing/disabled
		{
			name: "model_not_found → not_found/offering/not-retryable",
			code: "model_not_found", status: 404,
			wantClass: FailureClassNotFound, wantScope: FailureScopeOffering, wantRetryable: false,
		},
		// Row 4: Account quota exhausted (scope=account via body)
		{
			name: "quota_exhausted+scope=account → quota_error/account/not-retryable",
			code: "quota_exhausted", scope: "account", status: 429,
			wantClass: FailureClassQuota, wantScope: FailureScopeAccount, wantRetryable: false,
		},
		// Row 5: Model-specific rate limit (scope=model via body)
		{
			name: "quota_exhausted+scope=model → rate_limit/offering/not-retryable",
			code: "quota_exhausted", scope: "model", status: 429,
			wantClass: FailureClassRateLimit, wantScope: FailureScopeOffering, wantRetryable: false,
		},
		// Row 6: Provider overload / 503
		{
			name:      "503 → server_error/provider/retryable",
			status:    503,
			wantClass: FailureClassServer, wantScope: FailureScopeProvider, wantRetryable: true,
		},
		// Row 7: Unrecognized 5xx
		{
			name:      "504 → server_error/provider/retryable",
			status:    504,
			wantClass: FailureClassServer, wantScope: FailureScopeProvider, wantRetryable: true,
		},
		// Row 8: 429 without any scope signal → conservative transient_transport
		{
			name:      "429-no-scope → rate_limit/transient_transport/retryable",
			status:    429,
			wantClass: FailureClassRateLimit, wantScope: FailureScopeTransientTransport, wantRetryable: true,
		},
		// Row 9: 429 with X-RateLimit-Scope:account header (no semantic code)
		{
			name:      "429+X-RateLimit-Scope:account → quota_error/account",
			status:    429,
			headers:   http.Header{"X-Ratelimit-Scope": []string{"account"}},
			wantClass: FailureClassQuota, wantScope: FailureScopeAccount, wantRetryable: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyFailure(tc.code, tc.scope, tc.headers, nil, tc.status)
			if got.FailureClass != tc.wantClass {
				t.Errorf("FailureClass = %q, want %q", got.FailureClass, tc.wantClass)
			}
			if got.Scope != tc.wantScope {
				t.Errorf("Scope = %q, want %q", got.Scope, tc.wantScope)
			}
			if got.Retryable != tc.wantRetryable {
				t.Errorf("Retryable = %v, want %v", got.Retryable, tc.wantRetryable)
			}
		})
	}
}

// TestClassifyFailure_OrderingProof verifies that rung 1 beats rung 4:
// when a recognized semantic code (context_length_exceeded) is present
// alongside a 503 status, the code's class (invalid_request) wins over
// the status fallback (server_error). Mutation: swap the class check →
// RED; restore → GREEN.
func TestClassifyFailure_OrderingProof(t *testing.T) {
	// code="context_length_exceeded" (→ invalid_request) but HTTP 503
	// (→ server_error in rung 4). Rung 1 must win.
	f := ClassifyFailure("context_length_exceeded", "", nil, nil, 503)
	if f.FailureClass != FailureClassInvalidRequest {
		t.Fatalf("FailureClass = %q, want %q (rung 1 must beat rung 4)", f.FailureClass, FailureClassInvalidRequest)
	}
}

// TestClassifyFailure_Rung3AdapterRuleBeatsRung4 verifies that an
// adapter rule (rung 3) beats the HTTP fallback (rung 4): an unknown
// code "PROVIDER_THROTTLED" maps to rate_limit via adapter rule, not
// the server_error the 503 status would produce.
func TestClassifyFailure_Rung3AdapterRuleBeatsRung4(t *testing.T) {
	rules := []AdapterRule{
		{Code: "PROVIDER_THROTTLED", Result: TypedFailure{
			FailureClass: FailureClassRateLimit,
			Scope:        FailureScopeOffering,
			Retryable:    true,
			SafeMessage:  "provider throttled this request",
		}},
	}
	f := ClassifyFailure("PROVIDER_THROTTLED", "", nil, rules, 503)
	if f.FailureClass != FailureClassRateLimit {
		t.Fatalf("FailureClass = %q, want %q (adapter rule must beat HTTP fallback)", f.FailureClass, FailureClassRateLimit)
	}
	if f.Scope != FailureScopeOffering {
		t.Fatalf("Scope = %q, want %q", f.Scope, FailureScopeOffering)
	}
}

// TestClassifyFailure_Rung1BeatsRung3 verifies that a recognized
// semantic code (rung 1) beats an adapter rule for the same code
// (rung 3): if rung 1 recognizes "invalid_api_key", the adapter rule
// for "invalid_api_key" must NOT fire.
func TestClassifyFailure_Rung1BeatsRung3(t *testing.T) {
	rules := []AdapterRule{
		{Code: "invalid_api_key", Result: TypedFailure{
			FailureClass: FailureClassRateLimit, // wrong, but rung 3 must not fire
			Scope:        FailureScopeProvider,
		}},
	}
	f := ClassifyFailure("invalid_api_key", "", nil, rules, 401)
	if f.FailureClass != FailureClassAuth {
		t.Fatalf("FailureClass = %q, want %q (rung 1 must beat rung 3)", f.FailureClass, FailureClassAuth)
	}
}

// TestClassifyFailure_RetryAfterHeaderSeconds verifies that a numeric
// Retry-After header (e.g. "60") populates RetryAfter and CooldownUntil.
func TestClassifyFailure_RetryAfterHeaderSeconds(t *testing.T) {
	headers := http.Header{"Retry-After": []string{"60"}}
	f := ClassifyFailure("", "", headers, nil, 429)
	if f.RetryAfter == nil {
		t.Fatal("RetryAfter = nil, want non-nil (Retry-After: 60 header present)")
	}
	if *f.RetryAfter != 60 {
		t.Fatalf("RetryAfter = %d, want 60", *f.RetryAfter)
	}
	if f.CooldownUntil == nil {
		t.Fatal("CooldownUntil = nil, want non-nil")
	}
}

// TestClassifyFailure_RetryAfterHeaderHTTPDate verifies that an
// HTTP-date Retry-After header populates RetryAfter and CooldownUntil.
func TestClassifyFailure_RetryAfterHeaderHTTPDate(t *testing.T) {
	future := time.Now().Add(2 * time.Minute).UTC().Truncate(time.Second)
	headers := http.Header{"Retry-After": []string{future.Format(time.RFC1123)}}
	f := ClassifyFailure("", "", headers, nil, 429)
	if f.CooldownUntil == nil {
		t.Fatal("CooldownUntil = nil, want non-nil for HTTP-date Retry-After")
	}
}

// TestClassifyFailure_XQuotaResetPopulatesField verifies that a
// X-Quota-Reset unix timestamp populates QuotaResetAt.
func TestClassifyFailure_XQuotaResetPopulatesField(t *testing.T) {
	resetUnix := time.Now().Add(1 * time.Hour).Unix()
	headers := http.Header{"X-Quota-Reset": []string{strings.TrimSpace(time.Unix(resetUnix, 0).UTC().Format(time.RFC1123))}}
	f := ClassifyFailure("quota_exhausted", "account", headers, nil, 429)
	if f.QuotaResetAt == nil {
		t.Fatal("QuotaResetAt = nil, want non-nil (X-Quota-Reset header present)")
	}
}

// TestClassifyFailure_SafeMessageNeverEqualsCode is the canary for
// SECRET-SAFETY: safeMessageFor must never return the raw provider
// code string for any of the known FailureClass values.
func TestClassifyFailure_SafeMessageNeverEqualsCode(t *testing.T) {
	codes := []string{
		"context_length_exceeded",
		"invalid_api_key",
		"model_not_found",
		"quota_exhausted",
		"RESOURCE_EXHAUSTED",
	}
	for _, code := range codes {
		f := ClassifyFailure(code, "", nil, nil, 400)
		if f.SafeMessage == code {
			t.Errorf("code=%q: SafeMessage == code — raw provider code leaked into safe message", code)
		}
	}
}

// TestClassifyFailure_UnknownCodeFallsToHTTPStatus verifies rung 4:
// an unrecognized code with no adapter rule falls to the HTTP status
// classifier, not an error.
func TestClassifyFailure_UnknownCodeFallsToHTTPStatus(t *testing.T) {
	f := ClassifyFailure("some_unknown_provider_code", "", nil, nil, 404)
	if f.FailureClass != FailureClassNotFound {
		t.Fatalf("FailureClass = %q, want %q (unknown code falls to rung 4)", f.FailureClass, FailureClassNotFound)
	}
}

// TestClassifyFailure_NilHeadersDoNotPanic verifies that nil headers
// are handled gracefully (no panic, no nil-pointer dereference).
func TestClassifyFailure_NilHeadersDoNotPanic(t *testing.T) {
	f := ClassifyFailure("context_length_exceeded", "", nil, nil, 400)
	if f.FailureClass == "" {
		t.Fatal("FailureClass is empty with nil headers — should have classified successfully")
	}
}
