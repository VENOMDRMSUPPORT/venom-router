package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestClassifyClinePassChatUsability_TableOfVerdicts pins the legacy
// reference's classification (docs/evidence/clinepass-legacy-wire-reference.md
// §7) onto the shared usability vocabulary — including the owner's core
// requirement: a 200 is NOT proof; only a completion whose text actually
// contains the requested "ok" is usable.
func TestClassifyClinePassChatUsability_TableOfVerdicts(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   zenChatUsability
	}{
		{
			"enveloped completion saying ok",
			200,
			`{"success":true,"data":{"choices":[{"message":{"content":"ok"}}]}}`,
			zenChatUsable,
		},
		{
			"ok inside a sentence still counts (word-boundary match)",
			200,
			`{"success":true,"data":{"choices":[{"message":{"content":"Sure — ok"}}]}}`,
			zenChatUsable,
		},
		{
			"reasoning model spends budget on reasoning_content",
			200,
			`{"success":true,"data":{"choices":[{"message":{"content":"","reasoning_content":"The user wants ok"}}]}}`,
			zenChatUsable,
		},
		{
			"200 with empty content is NOT usable (not just a 200)",
			200,
			`{"success":true,"data":{"choices":[{"message":{"content":""}}]}}`,
			zenChatInconclusive,
		},
		{
			"200 without choices is NOT usable",
			200,
			`{"success":true,"data":{}}`,
			zenChatInconclusive,
		},
		{
			"401 token wording stops the account",
			401,
			`{"success":false,"error":"token expired"}`,
			zenChatAuthFailure,
		},
		{
			"403 subscription wording is a definitive per-account unsupported",
			403,
			`{"error":{"code":"forbidden","message":"You are not subscribed to individual inference"}}`,
			zenChatPaidUnusable,
		},
		{
			"402 insufficient credits is transient",
			402,
			`{"success":false,"error":"insufficient credits"}`,
			zenChatFreeExhausted,
		},
		{
			"429 is transient",
			429,
			`{"error":{"message":"rate limit exceeded"}}`,
			zenChatFreeExhausted,
		},
		{
			"404 model not found is definitive unsupported",
			404,
			`{"error":{"code":"not_found","message":"model not found"}}`,
			zenChatPaidUnusable,
		},
		{
			"400 without model wording is inconclusive",
			400,
			`{"error":{"message":"invalid temperature"}}`,
			zenChatInconclusive,
		},
		{
			"500 is inconclusive (transient, never a verdict)",
			500,
			`{"success":false,"error":"internal"}`,
			zenChatInconclusive,
		},
	}
	for _, tc := range cases {
		if got := classifyClinePassChatUsability(tc.status, []byte(tc.body)); got != tc.want {
			t.Errorf("%s: classify = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestProbeClinePassChatUsability_WireShape proves the probe sends the legacy
// model test verbatim — the enveloped chat endpoint path, the workos-prefixed
// Authorization from the stored token JSON, the cline headers, the "ok"
// prompt with the 256-token budget — and classifies the enveloped reply.
func TestProbeClinePassChatUsability_WireShape(t *testing.T) {
	var gotPath, gotAuth, gotClientType string
	var gotBody openCodeZenChatProbeRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotClientType = r.Header.Get("X-CLIENT-TYPE")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"success":true,"data":{"choices":[{"message":{"content":"ok"}}]}}`))
	}))
	t.Cleanup(srv.Close)

	stored := `{"access_token":"raw-token","refresh_token":"r","expires_at":99}`
	verdict, err := probeClinePassChatUsability(context.Background(), srv.URL, stored, "kimi-k2")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if verdict != zenChatUsable {
		t.Fatalf("verdict = %v, want usable", verdict)
	}
	if gotPath != "/api/v1/chat/completions" {
		t.Fatalf("path = %q, want /api/v1/chat/completions", gotPath)
	}
	if gotAuth != "Bearer workos:raw-token" {
		t.Fatalf("Authorization = %q, want workos-prefixed bearer", gotAuth)
	}
	if gotClientType != "venom-router" {
		t.Fatalf("X-CLIENT-TYPE = %q, want venom-router", gotClientType)
	}
	if gotBody.Model != "kimi-k2" || gotBody.MaxTokens != clinePassModelTestMaxTokens {
		t.Fatalf("body = %+v, want the named model + %d max_tokens", gotBody, clinePassModelTestMaxTokens)
	}
	if len(gotBody.Messages) != 1 || gotBody.Messages[0].Content != clinePassModelTestPrompt {
		t.Fatalf("messages = %+v, want the single ok-prompt message", gotBody.Messages)
	}
}

// TestProbeClinePassChatUsability_UnparseableCredentialIsAuthFailure: a
// credential that is not the token JSON can never authenticate — an
// account-level failure that stops the sweep, decided WITHOUT any HTTP call.
func TestProbeClinePassChatUsability_UnparseableCredentialIsAuthFailure(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	t.Cleanup(srv.Close)

	verdict, err := probeClinePassChatUsability(context.Background(), srv.URL, "not-json", "m")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if verdict != zenChatAuthFailure {
		t.Fatalf("verdict = %v, want auth failure", verdict)
	}
	if calls != 0 {
		t.Fatalf("server received %d calls, want 0", calls)
	}
}
