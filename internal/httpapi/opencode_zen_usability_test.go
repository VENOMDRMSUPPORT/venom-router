package httpapi

// opencode_zen_usability_test.go pins classifyOpenCodeZenChatUsability — the
// per-model "does this model actually work for THIS account" judge used by the
// chat-usability probe (design 2026-08-03). Unlike the account-health chat
// probe (opencode_zen_seams.go), this classifies the SEMANTIC outcome of a real
// completion, keying on the response BODY (never the HTTP status alone), so a
// 200 that carries an error envelope is never mistaken for a working model.
//
// The taxonomy (verified against opencode's own source + live responses,
// 2026-08-03):
//   - 200 + a well-formed completion (choices present, no error envelope) -> usable,
//     EVEN when the visible content is empty (a reasoning model can spend the
//     whole max_tokens budget on reasoning_content — big-pickle does exactly this).
//   - FreeUsageLimitError -> free model, quota exhausted RIGHT NOW: transient,
//     never a permanent "unusable" verdict.
//   - CreditsError / GoUsageLimitError -> paid tier, not free-usable for this account.
//   - AuthError -> an account-credential problem, not a per-model verdict.
//   - anything else (unknown error type, malformed/empty body, non-2xx with no
//     recognized envelope) -> inconclusive.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClassifyOpenCodeZenChatUsability(t *testing.T) {
	// A real big-pickle 200: a reasoning model that spent the whole tiny
	// max_tokens budget on reasoning_content, so the visible content is "" —
	// this MUST still classify as usable (the model responded).
	const bigPickle200EmptyContent = `{"id":"3a59b394","object":"chat.completion","created":1785708208,"model":"big-pickle","choices":[{"index":0,"finish_reason":"length","message":{"role":"assistant","content":"","reasoning_content":"We need answer"}}],"usage":{"total_tokens":100},"cost":"0"}`
	const plain200WithContent = `{"object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hi there"}}],"cost":"0"}`
	const freeExhaustedBody = `{"type":"error","error":{"type":"FreeUsageLimitError","message":"Free usage exceeded, subscribe to Go"}}`
	const creditsBody = `{"type":"error","error":{"type":"CreditsError","message":"Insufficient balance."}}`
	const goLimitBody = `{"type":"error","error":{"type":"GoUsageLimitError","message":"Go plan limit reached"}}`
	const authBody = `{"type":"error","error":{"type":"AuthError","message":"Invalid API key."}}`
	const unknownErrBody = `{"type":"error","error":{"type":"TeapotError","message":"short and stout"}}`

	tests := []struct {
		name   string
		status int
		body   string
		want   zenChatUsability
	}{
		{"200 empty content reasoning model is usable", 200, bigPickle200EmptyContent, zenChatUsable},
		{"200 with content is usable", 200, plain200WithContent, zenChatUsable},
		{"free usage limit is transient exhausted", 429, freeExhaustedBody, zenChatFreeExhausted},
		{"free usage limit on 401 is still transient", 401, freeExhaustedBody, zenChatFreeExhausted},
		{"credits error is paid-unusable", 401, creditsBody, zenChatPaidUnusable},
		{"go usage limit is paid-unusable", 401, goLimitBody, zenChatPaidUnusable},
		{"auth error is an account problem", 401, authBody, zenChatAuthFailure},
		{"unknown error type is inconclusive", 401, unknownErrBody, zenChatInconclusive},
		// The crown invariant: a 200 that CARRIES an error envelope is judged by
		// the body, never blessed as usable by its status code.
		{"200 carrying a credits envelope is paid-unusable, not usable", 200, creditsBody, zenChatPaidUnusable},
		{"200 carrying a free-limit envelope is exhausted, not usable", 200, freeExhaustedBody, zenChatFreeExhausted},
		{"malformed body is inconclusive", 200, "not json at all", zenChatInconclusive},
		{"empty body is inconclusive", 200, "", zenChatInconclusive},
		{"2xx with no choices and no error is inconclusive", 200, `{"object":"chat.completion"}`, zenChatInconclusive},
		{"5xx with no recognized envelope is inconclusive", 503, `{"nope":true}`, zenChatInconclusive},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyOpenCodeZenChatUsability(tc.status, []byte(tc.body))
			if got != tc.want {
				t.Fatalf("classifyOpenCodeZenChatUsability(%d, %q) = %v, want %v", tc.status, tc.body, got, tc.want)
			}
		})
	}
}

// TestProbeOpenCodeZen_RetryAfterSurfaced pins the seam that lets a provider's
// advertised backoff survive the probe: the HTTP Retry-After header (seconds).
func TestProbeOpenCodeZen_RetryAfterSurfaced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"error":{"type":"FreeUsageLimitError"}}`))
	}))
	defer srv.Close()
	res, err := probeOpenCodeZenChatUsability(context.Background(), srv.URL, "k", "m")
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if res.Verdict != zenChatFreeExhausted {
		t.Fatalf("verdict = %v, want zenChatFreeExhausted", res.Verdict)
	}
	if res.RetryAfter != 7*time.Second {
		t.Fatalf("retryAfter = %v, want 7s", res.RetryAfter)
	}
}

// TestProbeOpenCodeZen_RetryAfterMSBodyWinsOverHeader pins zen's documented
// body field (retry-after-ms, milliseconds) taking priority over the coarser
// HTTP header when the provider sends both.
func TestProbeOpenCodeZen_RetryAfterMSBodyWinsOverHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"error":{"type":"FreeUsageLimitError","retry-after-ms":1500}}`))
	}))
	defer srv.Close()
	res, err := probeOpenCodeZenChatUsability(context.Background(), srv.URL, "k", "m")
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if res.Verdict != zenChatFreeExhausted {
		t.Fatalf("verdict = %v, want zenChatFreeExhausted", res.Verdict)
	}
	if res.RetryAfter != 1500*time.Millisecond {
		t.Fatalf("retryAfter = %v, want 1500ms (body wins over header)", res.RetryAfter)
	}
}
