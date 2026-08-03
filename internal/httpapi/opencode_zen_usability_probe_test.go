package httpapi

// opencode_zen_usability_probe_test.go pins probeOpenCodeZenChatUsability — the
// HTTP layer that runs ONE real chat completion for a named model against a
// httptest server reproducing the exact opencode-zen responses, and returns the
// classifier's verdict. Unlike the account-health seam, it targets a specific
// model id (discovery supplies it) and surfaces the raw body verdict, never a
// key-validity collapse.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbeOpenCodeZenChatUsability(t *testing.T) {
	const (
		usableBody    = `{"id":"x","object":"chat.completion","choices":[{"index":0,"finish_reason":"length","message":{"role":"assistant","content":""}}],"cost":"0"}`
		exhaustedBody = `{"type":"error","error":{"type":"FreeUsageLimitError","message":"Free usage exceeded, subscribe to Go"}}`
		creditsBody   = `{"type":"error","error":{"type":"CreditsError","message":"Insufficient balance."}}`
		authBody      = `{"type":"error","error":{"type":"AuthError","message":"Invalid API key."}}`
	)

	tests := []struct {
		name   string
		status int
		body   string
		want   zenChatUsability
	}{
		{"working free model", http.StatusOK, usableBody, zenChatUsable},
		{"free model exhausted", http.StatusTooManyRequests, exhaustedBody, zenChatFreeExhausted},
		{"paid model no balance", http.StatusUnauthorized, creditsBody, zenChatPaidUnusable},
		{"bad key", http.StatusUnauthorized, authBody, zenChatAuthFailure},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotModel string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
					t.Errorf("Authorization = %q, want Bearer test-key", got)
				}
				var body struct {
					Model     string `json:"model"`
					MaxTokens int    `json:"max_tokens"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				gotModel = body.Model
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()

			got, err := probeOpenCodeZenChatUsability(context.Background(), srv.URL, "test-key", "big-pickle")
			if err != nil {
				t.Fatalf("probeOpenCodeZenChatUsability() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("probeOpenCodeZenChatUsability() = %v, want %v", got, tc.want)
			}
			if gotModel != "big-pickle" {
				t.Fatalf("probe sent model %q, want the requested big-pickle", gotModel)
			}
		})
	}
}

func TestProbeOpenCodeZenChatUsability_TransportErrorSurfaces(t *testing.T) {
	// A dead server (connection refused) is a transport failure — the caller
	// must see an error, never a silent zenChatUsable/zenChatInconclusive that
	// could be mistaken for a real verdict.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // now nothing is listening

	_, err := probeOpenCodeZenChatUsability(context.Background(), url, "test-key", "big-pickle")
	if err == nil {
		t.Fatal("probeOpenCodeZenChatUsability() error = nil, want a transport error")
	}
}
