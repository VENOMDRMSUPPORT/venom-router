package execution

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

// newFakeOpenAIServer is a test fixture only: a local httptest server
// returning a canned OpenAI-compatible chat completion response, plus a
// counter of how many requests it actually received (used to prove the
// no-reselect test never calls out for a rejected route).
func newFakeOpenAIServer(t *testing.T, replyContent string) (*httptest.Server, *atomic.Int64) {
	t.Helper()

	var requestCount atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		requestCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-fake-1",
			"object":  "chat.completion",
			"created": 1,
			"model":   "gpt-test-model",
			"choices": []map[string]any{
				{
					"index":         0,
					"finish_reason": "stop",
					"message": map[string]any{
						"role":    "assistant",
						"content": replyContent,
					},
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     5,
				"completion_tokens": 5,
				"total_tokens":      10,
			},
		})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &requestCount
}

// TestBifrostTransport_ChatRoundTrip is Test A1: one non-streamed chat
// request round-trips through the bifrost shim against the local fake
// server, and the fake server's canned response comes back unchanged.
func TestBifrostTransport_ChatRoundTrip(t *testing.T) {
	const wantReply = "hello from the fake server"
	srv, requestCount := newFakeOpenAIServer(t, wantReply)

	transport, err := NewBifrostTransport(context.Background(), BifrostTransportConfig{
		Provider: ProviderID(schemas.OpenAI),
		ModelID:  "gpt-test-model",
		APIKey:   "sk-test-key",
		BaseURL:  srv.URL,
	})
	if err != nil {
		t.Fatalf("NewBifrostTransport() error = %v", err)
	}
	t.Cleanup(transport.Close)

	route := ResolvedRoute{
		Provider:   ProviderID(schemas.OpenAI),
		AccountID:  "acct_smoke",
		Credential: StoredCredentials{Value: "sk-test-key"},
		ModelID:    "gpt-test-model",
		BaseURL:    srv.URL,
	}

	resp, err := transport.Execute(context.Background(), route, NormalizedRequest{
		Operation: OperationChat,
		Messages:  []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if resp.Message.Content != wantReply {
		t.Fatalf("Execute() response content = %q, want %q", resp.Message.Content, wantReply)
	}
	if got := requestCount.Load(); got != 1 {
		t.Fatalf("fake server received %d requests, want 1", got)
	}
}

// TestBifrostTransport_CannotReselectRoute is Test A2: single-route
// handoff. A BifrostTransport is constructed for exactly one (provider,
// model) pair. This asserts, by calling Execute with routes that name a
// different model and a different provider, that:
//  1. each call is rejected with ErrRouteNotConfigured — Bifrost is never
//     handed a route it wasn't configured for;
//  2. the fake server's request counter stays at 0 across both rejected
//     calls, proving the rejection happens before any network call, not
//     merely that the eventual HTTP response looked wrong;
//  3. the correctly-configured route still succeeds through the SAME
//     transport instance afterward, proving the rejections were
//     specifically about route mismatch, not a broken transport.
func TestBifrostTransport_CannotReselectRoute(t *testing.T) {
	srv, requestCount := newFakeOpenAIServer(t, "should never be returned")

	transport, err := NewBifrostTransport(context.Background(), BifrostTransportConfig{
		Provider: ProviderID(schemas.OpenAI),
		ModelID:  "gpt-test-model",
		APIKey:   "sk-test-key",
		BaseURL:  srv.URL,
	})
	if err != nil {
		t.Fatalf("NewBifrostTransport() error = %v", err)
	}
	t.Cleanup(transport.Close)

	baseRoute := ResolvedRoute{
		Provider:   ProviderID(schemas.OpenAI),
		AccountID:  "acct_smoke",
		Credential: StoredCredentials{Value: "sk-test-key"},
		ModelID:    "gpt-test-model",
		BaseURL:    srv.URL,
	}
	req := NormalizedRequest{Operation: OperationChat, Messages: []Message{{Role: "user", Content: "hi"}}}

	otherModelRoute := baseRoute
	otherModelRoute.ModelID = "some-other-model" // not what this transport was configured for
	if _, err := transport.Execute(context.Background(), otherModelRoute, req); !errors.Is(err, ErrRouteNotConfigured) {
		t.Fatalf("Execute() with mismatched model, error = %v, want ErrRouteNotConfigured", err)
	}
	if got := requestCount.Load(); got != 0 {
		t.Fatalf("fake server received %d requests after mismatched-model call, want 0 (must reject before any network call)", got)
	}

	otherProviderRoute := baseRoute
	otherProviderRoute.Provider = ProviderID("anthropic") // not what this transport was configured for
	if _, err := transport.Execute(context.Background(), otherProviderRoute, req); !errors.Is(err, ErrRouteNotConfigured) {
		t.Fatalf("Execute() with mismatched provider, error = %v, want ErrRouteNotConfigured", err)
	}
	if got := requestCount.Load(); got != 0 {
		t.Fatalf("fake server received %d requests after mismatched-provider call, want 0", got)
	}

	resp, err := transport.Execute(context.Background(), baseRoute, req)
	if err != nil {
		t.Fatalf("Execute() with the correctly-configured route, error = %v", err)
	}
	if resp == nil {
		t.Fatalf("Execute() with the correctly-configured route returned a nil response")
	}
	if got := requestCount.Load(); got != 1 {
		t.Fatalf("fake server received %d requests after the matching call, want 1", got)
	}
}

// --- P5-EXEC-004: bifrost fails closed; the content canary ----------------

// TestBifrostTransport_RejectsToolsAndParts proves the plain-text smoke shim
// rejects a request carrying tools or multimodal parts with the typed error,
// before any network call.
//
// Mutation (E4-M4/E4-M1 sibling): drop the requestCarriesRichFeatures guard →
// the request proceeds and the fake upstream is called → RED.
func TestBifrostTransport_RejectsToolsAndParts(t *testing.T) {
	srv, requestCount := newFakeOpenAIServer(t, "should never be returned")
	transport, err := NewBifrostTransport(context.Background(), BifrostTransportConfig{
		Provider: ProviderID(schemas.OpenAI), ModelID: "gpt-test-model", APIKey: "sk-test-key", BaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("NewBifrostTransport() error = %v", err)
	}
	t.Cleanup(transport.Close)
	route := ResolvedRoute{Provider: ProviderID(schemas.OpenAI), ModelID: "gpt-test-model", Credential: StoredCredentials{Value: "sk-test-key"}, BaseURL: srv.URL}

	withTools := NormalizedRequest{Messages: []Message{{Role: "user", Content: "hi"}}, Tools: []ToolDefinition{{Name: "f"}}}
	if _, err := transport.Execute(context.Background(), route, withTools); !errors.Is(err, ErrRequestFeatureUnsupported) {
		t.Fatalf("Execute(tools) error = %v, want ErrRequestFeatureUnsupported", err)
	}
	withParts := NormalizedRequest{Messages: []Message{{Role: "user", Parts: []ContentPart{{Kind: ContentPartImage, ImageBase64: "x", MediaType: "image/png"}}}}}
	if _, err := transport.Execute(context.Background(), route, withParts); !errors.Is(err, ErrRequestFeatureUnsupported) {
		t.Fatalf("Execute(parts) error = %v, want ErrRequestFeatureUnsupported", err)
	}
	if _, err := transport.Stream(context.Background(), route, withTools); !errors.Is(err, ErrRequestFeatureUnsupported) {
		t.Fatalf("Stream(tools) error = %v, want ErrRequestFeatureUnsupported", err)
	}
	if got := requestCount.Load(); got != 0 {
		t.Fatalf("fake upstream received %d requests, want 0 (fail closed before any network call)", got)
	}
}

// TestExecution_SeamErrorsNeverLeakContent is the P5-EXEC-004 content canary: a
// credential-shaped token AND a plain marker planted in a tool description and
// an image URL never appear in any error string a feature-unsupported rejection
// returns.
//
// Mutation E4-M6: include the image URL in the returned error message → the
// native-OAuth URL-image error carries the marker → RED.
func TestExecution_SeamErrorsNeverLeakContent(t *testing.T) {
	const credShaped = "sk-live-CANARY-9f3a2b1c"
	const plainMarker = "PLAINMARKER_ZZZ"
	planted := func(s string) bool { return strings.Contains(s, credShaped) || strings.Contains(s, plainMarker) }

	// Native-OAuth: a URL-only image whose URL carries both canaries.
	noSrv, _ := nativeOAuthCountingServer(t)
	native := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)
	_, nerr := native.Execute(context.Background(), newNativeOAuthTestRoute(noSrv.URL), NormalizedRequest{
		Messages: []Message{{Role: "user", Parts: []ContentPart{{Kind: ContentPartImage, ImageURL: "https://x/" + credShaped + "/" + plainMarker + ".png"}}}},
	})
	if nerr == nil || planted(nerr.Error()) {
		t.Fatalf("native-OAuth URL-image error leaked content: %v", nerr)
	}

	// Bifrost: a tool whose description carries both canaries.
	bSrv, _ := newFakeOpenAIServer(t, "x")
	bt, err := NewBifrostTransport(context.Background(), BifrostTransportConfig{Provider: ProviderID(schemas.OpenAI), ModelID: "gpt-test-model", APIKey: "k", BaseURL: bSrv.URL})
	if err != nil {
		t.Fatalf("NewBifrostTransport() error = %v", err)
	}
	t.Cleanup(bt.Close)
	route := ResolvedRoute{Provider: ProviderID(schemas.OpenAI), ModelID: "gpt-test-model", BaseURL: bSrv.URL}
	_, berr := bt.Execute(context.Background(), route, NormalizedRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
		Tools:    []ToolDefinition{{Name: "f", Description: credShaped + " " + plainMarker}},
	})
	if berr == nil || planted(berr.Error()) {
		t.Fatalf("bifrost tools error leaked content: %v", berr)
	}
}
