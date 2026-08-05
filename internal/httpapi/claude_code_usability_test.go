package httpapi

// claude_code_usability_test.go pins classifyClaudeCodeChatUsability and
// probeClaudeCodeChatUsability — the per-model "does this model actually work
// for THIS account" judge for claude-code's native Anthropic /v1/messages wire
// shape (task-4 brief, 2026-08-05). This is an entitlement confirmation at the
// smallest possible spend (spec D6): max_tokens: 1, the single word "ping" —
// it runs against the owner's real subscription.
//
// Anthropic's error envelope is {"type":"error","error":{"type":"..."}}. Like
// zen and gemini, the body wins over the HTTP status: a parseable envelope's
// error.type is authoritative regardless of the HTTP code, so a 200 that
// somehow carries an error envelope is never blessed as usable.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClassifyClaudeCodeChatUsability_TableOfVerdicts(t *testing.T) {
	const workingBody = `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"x"}]}`
	const authErrorBody = `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`
	const permissionErrorBody = `{"type":"error","error":{"type":"permission_error","message":"not authorized"}}`
	const billingErrorBody = `{"type":"error","error":{"type":"billing_error","message":"payment required"}}`
	const notFoundErrorBody = `{"type":"error","error":{"type":"not_found_error","message":"model not found"}}`
	const rateLimitErrorBody = `{"type":"error","error":{"type":"rate_limit_error","message":"rate limited"}}`
	const overloadedErrorBody = `{"type":"error","error":{"type":"overloaded_error","message":"overloaded"}}`
	const unknownErrorBody = `{"type":"error","error":{"type":"invalid_request_error","message":"bad request"}}`

	tests := []struct {
		name   string
		status int
		body   string
		want   zenChatUsability
	}{
		// The taxonomy rows from the brief's table.
		{"2xx with non-empty content is usable", 200, workingBody, zenChatUsable},
		{"authentication_error is auth failure", 401, authErrorBody, zenChatAuthFailure},
		{"permission_error is paid-unusable", 403, permissionErrorBody, zenChatPaidUnusable},
		{"billing_error is paid-unusable", 402, billingErrorBody, zenChatPaidUnusable},
		{"not_found_error is paid-unusable (model not on this plan)", 404, notFoundErrorBody, zenChatPaidUnusable},
		{"rate_limit_error is free-exhausted (transient)", 429, rateLimitErrorBody, zenChatFreeExhausted},
		{"overloaded_error is free-exhausted (transient)", 529, overloadedErrorBody, zenChatFreeExhausted},
		{"unknown error type is inconclusive", 400, unknownErrorBody, zenChatInconclusive},
		{"garbage bytes are inconclusive", 500, "\xff\xfe not json at all \x00\x01", zenChatInconclusive},

		// Precedence + edge coverage, mirroring the zen/gemini classifiers' proven shape.
		{"body error envelope wins even on a 200", 200, rateLimitErrorBody, zenChatFreeExhausted},
		{"empty body on 200 is inconclusive", 200, "", zenChatInconclusive},
		{"2xx with empty content array is inconclusive", 200, `{"type":"message","content":[]}`, zenChatInconclusive},
		{"2xx with no content field at all is inconclusive", 200, `{"type":"message"}`, zenChatInconclusive},
		{"5xx with no recognized envelope is inconclusive", 503, `{"nope":true}`, zenChatInconclusive},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyClaudeCodeChatUsability(tc.status, []byte(tc.body))
			if got != tc.want {
				t.Fatalf("classifyClaudeCodeChatUsability(%d, %q) = %v, want %v", tc.status, tc.body, got, tc.want)
			}
		})
	}
}

// TestProbeClaudeCodeChatUsability_WireShape proves the probe posts the
// minimal-spend body to exactly "{baseURL}/v1/messages" with the bearer
// Authorization header (extracted from the stored token JSON) and the
// anthropic-version / anthropic-beta headers copied from the production
// anthropic_messages codec (internal/execution/anthropicwire.go), and
// classifies a well-formed content response as usable.
func TestProbeClaudeCodeChatUsability_WireShape(t *testing.T) {
	var gotMethod, gotPath, gotAuth, gotVersion, gotBeta string
	var gotBody openCodeZenChatProbeRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotVersion = r.Header.Get("anthropic-version")
		gotBeta = r.Header.Get("anthropic-beta")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"type":"message","content":[{"type":"text","text":"ok"}]}`))
	}))
	t.Cleanup(srv.Close)

	stored := `{"access_token":"raw-oauth-token","refresh_token":"r","expires_at":99}`
	res, err := probeClaudeCodeChatUsability(context.Background(), srv.URL, stored, "claude-sonnet-4")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if res.Verdict != zenChatUsable {
		t.Fatalf("verdict = %v, want usable", res.Verdict)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/messages" {
		t.Fatalf("path = %q, want /v1/messages", gotPath)
	}
	if gotAuth != "Bearer raw-oauth-token" {
		t.Fatalf("Authorization = %q, want bearer with the extracted access token", gotAuth)
	}
	if gotVersion != claudeCodeAnthropicVersion {
		t.Fatalf("anthropic-version = %q, want %q (copied from the production codec)", gotVersion, claudeCodeAnthropicVersion)
	}
	if gotBeta != claudeCodeMessagesAnthropicBeta {
		t.Fatalf("anthropic-beta = %q, want %q (copied from the production codec)", gotBeta, claudeCodeMessagesAnthropicBeta)
	}
	if gotBody.Model != "claude-sonnet-4" || gotBody.MaxTokens != 1 {
		t.Fatalf("body = %+v, want the named model + max_tokens:1 (minimal spend, spec D6)", gotBody)
	}
	if len(gotBody.Messages) != 1 || gotBody.Messages[0].Role != "user" || gotBody.Messages[0].Content != "ping" {
		t.Fatalf("messages = %+v, want a single user-role \"ping\" message", gotBody.Messages)
	}
}

// TestProbeClaudeCodeChatUsability_ErrorEnvelopeCarriesRetryAfter proves a
// rate_limit_error body classifies as the transient free-exhausted verdict
// even under a non-429 HTTP status, and that a Retry-After header still flows
// through the shared usabilityRetryAfter helper.
func TestProbeClaudeCodeChatUsability_ErrorEnvelopeCarriesRetryAfter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"rate limited"}}`))
	}))
	t.Cleanup(srv.Close)

	stored := `{"access_token":"raw-oauth-token"}`
	res, err := probeClaudeCodeChatUsability(context.Background(), srv.URL, stored, "claude-sonnet-4")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if res.Verdict != zenChatFreeExhausted {
		t.Fatalf("verdict = %v, want free-exhausted", res.Verdict)
	}
	if res.RetryAfter.Seconds() != 7 {
		t.Fatalf("RetryAfter = %v, want 7s", res.RetryAfter)
	}
}

// TestProbeClaudeCodeChatUsability_UnparseableCredentialIsAuthFailure: a
// credential that is not the token JSON, or one with no access_token, can
// never authenticate — an account-level failure decided WITHOUT any HTTP
// call, exactly like probeClinePassChatUsability.
func TestProbeClaudeCodeChatUsability_UnparseableCredentialIsAuthFailure(t *testing.T) {
	cases := []struct {
		name       string
		credential string
	}{
		{"not JSON at all", "not-json"},
		{"JSON without access_token", `{"refresh_token":"r"}`},
		{"empty access_token", `{"access_token":""}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
			t.Cleanup(srv.Close)

			res, err := probeClaudeCodeChatUsability(context.Background(), srv.URL, tc.credential, "claude-sonnet-4")
			if err != nil {
				t.Fatalf("probe: %v", err)
			}
			if res.Verdict != zenChatAuthFailure {
				t.Fatalf("verdict = %v, want auth failure", res.Verdict)
			}
			if calls != 0 {
				t.Fatalf("server received %d calls, want 0", calls)
			}
		})
	}
}

// TestProbeClaudeCodeChatUsability_TransportErrorIsNeverAVerdict proves an
// unreachable server surfaces as a non-nil error with the zero-value
// inconclusive verdict, never as a real classification — the shared honesty
// rule every probe seam upholds.
func TestProbeClaudeCodeChatUsability_TransportErrorIsNeverAVerdict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close() // closed before use: guarantees a transport-level failure

	stored := `{"access_token":"raw-oauth-token"}`
	res, err := probeClaudeCodeChatUsability(context.Background(), srv.URL, stored, "claude-sonnet-4")
	if err == nil {
		t.Fatalf("expected a transport error, got nil (verdict %v)", res.Verdict)
	}
	if res.Verdict != zenChatInconclusive {
		t.Fatalf("verdict on transport error = %v, want the zero-value inconclusive", res.Verdict)
	}
}
