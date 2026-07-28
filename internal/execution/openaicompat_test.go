package execution

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newOpenAICompatTestRoute(baseURL string) ResolvedRoute {
	return ResolvedRoute{
		Provider:   ProviderID("opencode-zen"),
		AccountID:  "acct_probe",
		Credential: StoredCredentials{Value: "sk-canary-test-key"},
		ModelID:    "test-model",
		BaseURL:    baseURL,
	}
}

// decodedRequestServer starts an httptest server that decodes every
// request body it receives into dst (a pointer the caller owns) and
// replies with a fixed, minimal chat-completion success body.
func decodedRequestServer(t *testing.T, dst *map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(dst)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": "ok"}, "finish_reason": "stop"},
			},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestOpenAICompatible_SendsMaxTokensWhenSet is mutation row 1.1's pinning
// test: a non-nil NormalizedRequest.MaxTokens must appear on the wire as
// "max_tokens".
func TestOpenAICompatible_SendsMaxTokensWhenSet(t *testing.T) {
	var got map[string]any
	srv := decodedRequestServer(t, &got)

	transport := NewOpenAICompatibleTransport(&http.Client{}, 5*time.Second)
	maxTokens := 16
	_, err := transport.Execute(context.Background(), newOpenAICompatTestRoute(srv.URL), NormalizedRequest{
		Messages:  []Message{{Role: "user", Content: "hi"}},
		MaxTokens: &maxTokens,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	v, ok := got["max_tokens"]
	if !ok {
		t.Fatalf("request body has no max_tokens key, want 16; body = %+v", got)
	}
	if n, ok := v.(float64); !ok || n != 16 {
		t.Fatalf("max_tokens = %v, want 16", v)
	}
}

// TestOpenAICompatible_OmitsItWhenNil is mutation row 1.2's pinning test:
// a nil NormalizedRequest.MaxTokens must be absent from the wire body
// entirely — never sent as a literal max_tokens: 0.
func TestOpenAICompatible_OmitsItWhenNil(t *testing.T) {
	var got map[string]any
	srv := decodedRequestServer(t, &got)

	transport := NewOpenAICompatibleTransport(&http.Client{}, 5*time.Second)
	_, err := transport.Execute(context.Background(), newOpenAICompatTestRoute(srv.URL), NormalizedRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if v, ok := got["max_tokens"]; ok {
		t.Fatalf("request body has max_tokens = %v, want the key absent entirely", v)
	}
}

func newFixedChatResponseServer(t *testing.T, message map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": message, "finish_reason": "stop"},
			},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestOpenAICompatible_ClassifiesToolCallResponse is mutation row 1.3's
// pinning test: a response carrying tool_calls must surface them on
// NormalizedResponse.ToolCalls so the adapter can classify WitnessToolCall
// — never silently dropped/treated as a plain text_only reply.
func TestOpenAICompatible_ClassifiesToolCallResponse(t *testing.T) {
	srv := newFixedChatResponseServer(t, map[string]any{
		"role":    "assistant",
		"content": "",
		"tool_calls": []map[string]any{
			{"function": map[string]any{"name": "add", "arguments": `{"a":2,"b":2}`}},
		},
	})

	transport := NewOpenAICompatibleTransport(&http.Client{}, 5*time.Second)
	resp, err := transport.Execute(context.Background(), newOpenAICompatTestRoute(srv.URL), NormalizedRequest{
		Messages: []Message{{Role: "user", Content: "2+2?"}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "add" {
		t.Fatalf("ToolCalls = %+v, want exactly one call named %q", resp.ToolCalls, "add")
	}
	if resp.ToolCalls[0].ArgumentsJSON != `{"a":2,"b":2}` {
		t.Fatalf("ArgumentsJSON = %q, want the raw provider argument string", resp.ToolCalls[0].ArgumentsJSON)
	}
}

// TestOpenAICompatible_StructuredJSON proves a JSON-object content
// response round-trips through Message.Content unchanged (the adapter,
// not this transport, decides structured_json vs text_only — this test
// only pins that the transport hands the content through faithfully).
func TestOpenAICompatible_StructuredJSON(t *testing.T) {
	srv := newFixedChatResponseServer(t, map[string]any{"role": "assistant", "content": `{"ok":true}`})

	transport := NewOpenAICompatibleTransport(&http.Client{}, 5*time.Second)
	resp, err := transport.Execute(context.Background(), newOpenAICompatTestRoute(srv.URL), NormalizedRequest{
		Messages: []Message{{Role: "user", Content: "return json"}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(resp.ToolCalls) != 0 {
		t.Fatalf("ToolCalls = %+v, want none for a structured-JSON-only response", resp.ToolCalls)
	}
	if resp.Message.Content != `{"ok":true}` {
		t.Fatalf("Message.Content = %q, want the raw JSON object text unchanged", resp.Message.Content)
	}
}

// TestOpenAICompatible_TextOnly is mutation row 1.4's pinning test: a
// plain-text response carries no tool calls and non-JSON content, the
// shape a real text_only witness classification (done by the adapter)
// depends on this transport reporting honestly.
func TestOpenAICompatible_TextOnly(t *testing.T) {
	srv := newFixedChatResponseServer(t, map[string]any{"role": "assistant", "content": "the sky is blue"})

	transport := NewOpenAICompatibleTransport(&http.Client{}, 5*time.Second)
	resp, err := transport.Execute(context.Background(), newOpenAICompatTestRoute(srv.URL), NormalizedRequest{
		Messages: []Message{{Role: "user", Content: "describe the sky"}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(resp.ToolCalls) != 0 {
		t.Fatalf("ToolCalls = %+v, want none for a plain text response", resp.ToolCalls)
	}
	var probe any
	if json.Unmarshal([]byte(resp.Message.Content), &probe) == nil {
		if _, isObject := probe.(map[string]any); isObject {
			t.Fatalf("Message.Content = %q parses as a JSON object, want plain text", resp.Message.Content)
		}
	}
}

// TestOpenAICompatible_FailureCarriesStatusCodeAndRawMessage is mutation
// row 1.5's pinning test (via the end-to-end test below it exercises the
// same path) and its own direct check: a 400 rejection's status/code/
// message must all come through Failure unmodified.
func TestOpenAICompatible_FailureCarriesStatusCodeAndRawMessage(t *testing.T) {
	const rawMessage = "maximum context length is 128000 tokens"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"code": "context_length_exceeded", "message": rawMessage},
		})
	}))
	t.Cleanup(srv.Close)

	transport := NewOpenAICompatibleTransport(&http.Client{}, 5*time.Second)
	route := newOpenAICompatTestRoute(srv.URL)
	_, err := transport.Execute(context.Background(), route, NormalizedRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatalf("Execute() error = nil, want a non-nil error for a 400 response")
	}

	failure := transport.Failure(err, route)
	if failure.HTTPStatus != http.StatusBadRequest {
		t.Fatalf("HTTPStatus = %d, want %d", failure.HTTPStatus, http.StatusBadRequest)
	}
	if failure.ProviderCode != "context_length_exceeded" {
		t.Fatalf("ProviderCode = %q, want %q", failure.ProviderCode, "context_length_exceeded")
	}
	if failure.RawMessage != rawMessage {
		t.Fatalf("RawMessage = %q, want %q", failure.RawMessage, rawMessage)
	}
	if failure.SafeMessage == rawMessage {
		t.Fatalf("SafeMessage must never equal the raw provider text")
	}
}

// TestOpenAICompatible_TimeoutAndNetworkAreTyped is mutation row 1.6's
// pinning test: a hung server maps to ErrTransportTimeout, a closed port
// maps to ErrTransportNetwork, and Failure never fabricates an HTTPStatus
// for either (it must stay 0 — never, e.g., a bogus 500).
func TestOpenAICompatible_TimeoutAndNetworkAreTyped(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		// block, not r.Context().Done(), is what the handler waits on: a
		// client-side context timeout does not reliably tear down the
		// underlying connection, so the server's own request context may
		// never actually fire Done — that left httptest.Server.Close (in
		// t.Cleanup) hanging forever waiting for this handler to return.
		// Closing block here is registered AFTER hung's own Cleanup, so it
		// runs FIRST (t.Cleanup is LIFO) and reliably unblocks the handler
		// before Close ever waits on it.
		block := make(chan struct{})
		hung := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-block
		}))
		t.Cleanup(hung.Close)
		t.Cleanup(func() { close(block) })

		transport := NewOpenAICompatibleTransport(&http.Client{}, 100*time.Millisecond)
		route := newOpenAICompatTestRoute(hung.URL)
		_, err := transport.Execute(context.Background(), route, NormalizedRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
		if !errors.Is(err, ErrTransportTimeout) {
			t.Fatalf("Execute() error = %v, want ErrTransportTimeout", err)
		}
		failure := transport.Failure(err, route)
		if failure.HTTPStatus != 0 {
			t.Fatalf("HTTPStatus = %d, want 0 (never a bogus status for a timeout)", failure.HTTPStatus)
		}
	})

	t.Run("network", func(t *testing.T) {
		// A listener that is opened then immediately closed yields a
		// deterministic "connection refused" without any real network
		// dependency or wall-clock timeout.
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("net.Listen: %v", err)
		}
		closedPortURL := "http://" + ln.Addr().String()
		if err := ln.Close(); err != nil {
			t.Fatalf("close listener: %v", err)
		}

		transport := NewOpenAICompatibleTransport(&http.Client{}, 5*time.Second)
		route := newOpenAICompatTestRoute(closedPortURL)
		_, execErr := transport.Execute(context.Background(), route, NormalizedRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
		if !errors.Is(execErr, ErrTransportNetwork) {
			t.Fatalf("Execute() error = %v, want ErrTransportNetwork", execErr)
		}
		failure := transport.Failure(execErr, route)
		if failure.HTTPStatus != 0 {
			t.Fatalf("HTTPStatus = %d, want 0 (never a bogus status for a network failure)", failure.HTTPStatus)
		}
	})
}
