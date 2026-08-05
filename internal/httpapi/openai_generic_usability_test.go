package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestClassifyOpenAICompatibleChatUsability pins the status-first taxonomy
// (design table, task-2 brief): unlike zen, these three providers signal with
// the HTTP status, and the body only refines a 2xx into usable/inconclusive.
func TestClassifyOpenAICompatibleChatUsability(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   zenChatUsability
	}{
		{"working", 200, `{"choices":[{"message":{"content":"hi"}}]}`, zenChatUsable},
		{"auth", 401, `{"error":{"message":"invalid api key"}}`, zenChatAuthFailure},
		{"payment", 402, `{"error":{"message":"insufficient credits"}}`, zenChatPaidUnusable},
		{"entitlement", 403, `{}`, zenChatPaidUnusable},
		{"unknown-model", 404, `{"error":{"message":"model not found"}}`, zenChatPaidUnusable},
		{"throttled", 429, `{}`, zenChatFreeExhausted},
		{"empty-200", 200, ``, zenChatInconclusive},
		{"server-error", 500, `{}`, zenChatInconclusive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyOpenAICompatibleChatUsability(tc.status, []byte(tc.body)); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestProbeOpenAICompatibleChatUsability_WireShape proves the probe posts the
// minimal openCodeZenChatProbeRequest (ping, max_tokens:1) to exactly
// "{baseURL}/chat/completions" — liveProviderBaseURLs' agnes-ai/ollama-cloud/
// nvidia-nim entries already carry the version segment, so the probe must NOT
// insert its own — with a bearer Authorization, and classifies a well-formed
// completion as usable.
func TestProbeOpenAICompatibleChatUsability_WireShape(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	var gotBody openCodeZenChatProbeRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	t.Cleanup(srv.Close)

	res, err := probeOpenAICompatibleChatUsability(context.Background(), srv.URL+"/v1", "k", "some-model")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if res.Verdict != zenChatUsable {
		t.Fatalf("verdict = %v, want usable", res.Verdict)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("path = %q, want /v1/chat/completions", gotPath)
	}
	if gotAuth != "Bearer k" {
		t.Fatalf("Authorization = %q, want Bearer k", gotAuth)
	}
	if gotBody.Model != "some-model" || gotBody.MaxTokens != 1 {
		t.Fatalf("body = %+v, want the named model + max_tokens:1", gotBody)
	}
	if len(gotBody.Messages) != 1 || gotBody.Messages[0].Content != "ping" {
		t.Fatalf("messages = %+v, want the single ping message", gotBody.Messages)
	}
}

// TestProbeOpenAICompatibleChatUsability_ThrottledCarriesRetryAfter proves a
// 429 classifies as the transient free-exhausted verdict and the Retry-After
// header flows through usabilityRetryAfter into the result.
func TestProbeOpenAICompatibleChatUsability_ThrottledCarriesRetryAfter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	res, err := probeOpenAICompatibleChatUsability(context.Background(), srv.URL+"/v1", "k", "some-model")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if res.Verdict != zenChatFreeExhausted {
		t.Fatalf("verdict = %v, want free-exhausted", res.Verdict)
	}
	if res.RetryAfter != 3*time.Second {
		t.Fatalf("RetryAfter = %v, want 3s", res.RetryAfter)
	}
}
