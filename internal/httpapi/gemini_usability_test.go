package httpapi

// gemini_usability_test.go pins classifyGeminiChatUsability and
// probeGeminiChatUsability — the per-model "does this model actually work for
// THIS account" judge for gemini-cli's native generateContent wire shape
// (task-3 brief, 2026-08-05). Gemini is NOT OpenAI-shaped: no "choices", no
// bearer auth. The verified wire facts (internal/providers/gemini_cli.go,
// chatcompletions.go's liveProviderBaseURLs) are:
//   - base URL already carries the version segment ("{base}/v1beta"); the
//     probe appends ONLY "/models/{id}:generateContent".
//   - auth travels as the x-goog-api-key header, never Authorization.
//   - Google's error envelope is {"error":{"code":429,"status":"RESOURCE_EXHAUSTED",...}}.
//
// Like zen, the body wins over the HTTP status: a parseable error envelope's
// `status` string is classified regardless of the HTTP code, so a 200 that
// carries an error is never blessed as usable. Only when the envelope carries
// no recognized `error.status` does the HTTP status decide.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClassifyGeminiChatUsability(t *testing.T) {
	const workingBody = `{"candidates":[{"content":{"parts":[{"text":"x"}]},"finishReason":"STOP"}]}`
	const unauthenticatedBody = `{"error":{"code":401,"status":"UNAUTHENTICATED","message":"API key invalid."}}`
	const permissionDeniedBody = `{"error":{"code":403,"status":"PERMISSION_DENIED","message":"Caller does not have permission."}}`
	const notFoundBody = `{"error":{"code":404,"status":"NOT_FOUND","message":"model not found."}}`
	const resourceExhaustedBody = `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"Quota exceeded."}}`
	const unknownStatusBody = `{"error":{"code":400,"status":"INVALID_ARGUMENT","message":"bad request"}}`

	tests := []struct {
		name   string
		status int
		body   string
		want   zenChatUsability
	}{
		// The 6 taxonomy rows from the brief's table.
		{"working completion is usable", 200, workingBody, zenChatUsable},
		{"UNAUTHENTICATED status is auth failure", 401, unauthenticatedBody, zenChatAuthFailure},
		{"PERMISSION_DENIED status is paid-unusable", 403, permissionDeniedBody, zenChatPaidUnusable},
		{"NOT_FOUND status is paid-unusable", 404, notFoundBody, zenChatPaidUnusable},
		{"RESOURCE_EXHAUSTED status is free-exhausted", 429, resourceExhaustedBody, zenChatFreeExhausted},
		{"garbage bytes are inconclusive", 500, "\xff\xfe not json at all \x00\x01", zenChatInconclusive},

		// Extra precedence coverage, mirroring the zen classifier's proven shape.
		{"body status wins even on a 200", 200, resourceExhaustedBody, zenChatFreeExhausted},
		{"recognized envelope but unknown status is inconclusive", 400, unknownStatusBody, zenChatInconclusive},
		{"HTTP 401 with no error envelope falls back to status", 401, `{}`, zenChatAuthFailure},
		{"HTTP 403 with no error envelope falls back to status", 403, `{}`, zenChatPaidUnusable},
		{"HTTP 404 with no error envelope falls back to status", 404, `{}`, zenChatPaidUnusable},
		{"HTTP 429 with no error envelope falls back to status", 429, `{}`, zenChatFreeExhausted},
		{"empty body on 200 is inconclusive", 200, "", zenChatInconclusive},
		{"2xx with no candidates and no error is inconclusive", 200, `{"promptFeedback":{}}`, zenChatInconclusive},
		{"5xx with no recognized envelope is inconclusive", 503, `{"nope":true}`, zenChatInconclusive},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyGeminiChatUsability(tc.status, []byte(tc.body))
			if got != tc.want {
				t.Fatalf("classifyGeminiChatUsability(%d, %q) = %v, want %v", tc.status, tc.body, got, tc.want)
			}
		})
	}
}

// TestProbeGeminiChatUsability_WireShape proves the probe posts the minimal
// generateContent body to exactly "{baseURL}/models/{modelID}:generateContent"
// with the x-goog-api-key header (never Authorization), and classifies a
// well-formed candidates response as usable.
func TestProbeGeminiChatUsability_WireShape(t *testing.T) {
	var gotMethod, gotPath, gotGoogHeader, gotAuthHeader string
	var gotBody struct {
		Contents []struct {
			Role  string `json:"role"`
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"contents"`
		GenerationConfig struct {
			MaxOutputTokens int `json:"maxOutputTokens"`
		} `json:"generationConfig"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotGoogHeader = r.Header.Get("x-goog-api-key")
		gotAuthHeader = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}`))
	}))
	t.Cleanup(srv.Close)

	res, err := probeGeminiChatUsability(context.Background(), srv.URL, "test-key", "gemini-x")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if res.Verdict != zenChatUsable {
		t.Fatalf("verdict = %v, want usable", res.Verdict)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/models/gemini-x:generateContent" {
		t.Fatalf("path = %q, want /models/gemini-x:generateContent", gotPath)
	}
	if gotGoogHeader != "test-key" {
		t.Fatalf("x-goog-api-key = %q, want test-key", gotGoogHeader)
	}
	if gotAuthHeader != "" {
		t.Fatalf("Authorization = %q, want empty (gemini auths via x-goog-api-key, never Authorization)", gotAuthHeader)
	}
	if gotBody.GenerationConfig.MaxOutputTokens != 1 {
		t.Fatalf("generationConfig.maxOutputTokens = %d, want 1", gotBody.GenerationConfig.MaxOutputTokens)
	}
	if len(gotBody.Contents) != 1 || gotBody.Contents[0].Role != "user" {
		t.Fatalf("contents = %+v, want a single user-role entry", gotBody.Contents)
	}
	if len(gotBody.Contents[0].Parts) != 1 || gotBody.Contents[0].Parts[0].Text != "ping" {
		t.Fatalf("parts = %+v, want a single ping part", gotBody.Contents[0].Parts)
	}
}

// TestProbeGeminiChatUsability_ErrorEnvelopeCarriesRetryAfter proves a
// RESOURCE_EXHAUSTED body classifies as the transient free-exhausted verdict
// even under a non-429 HTTP status, and that a Retry-After header still flows
// through the shared usabilityRetryAfter helper.
func TestProbeGeminiChatUsability_ErrorEnvelopeCarriesRetryAfter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"Quota exceeded."}}`))
	}))
	t.Cleanup(srv.Close)

	res, err := probeGeminiChatUsability(context.Background(), srv.URL, "test-key", "gemini-x")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if res.Verdict != zenChatFreeExhausted {
		t.Fatalf("verdict = %v, want free-exhausted", res.Verdict)
	}
	if res.RetryAfter != 5*time.Second {
		t.Fatalf("RetryAfter = %v, want 5s", res.RetryAfter)
	}
}

// TestProbeGeminiChatUsability_TransportErrorIsNeverAVerdict proves an
// unreachable server surfaces as a non-nil error with the zero-value
// inconclusive verdict, never as a real classification — the shared honesty
// rule every probe seam upholds.
func TestProbeGeminiChatUsability_TransportErrorIsNeverAVerdict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // closed before use: guarantees a transport-level failure

	res, err := probeGeminiChatUsability(context.Background(), srv.URL, "test-key", "gemini-x")
	if err == nil {
		t.Fatalf("expected a transport error, got nil (verdict %v)", res.Verdict)
	}
	if res.Verdict != zenChatInconclusive {
		t.Fatalf("verdict on transport error = %v, want the zero-value inconclusive", res.Verdict)
	}
}
