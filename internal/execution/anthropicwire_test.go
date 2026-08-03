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
)

func oauthRoute(baseURL string, schema WireSchema, model, cred string) ResolvedRoute {
	return ResolvedRoute{
		Provider:   ProviderID("p"),
		AccountID:  "a",
		Credential: StoredCredentials{Value: cred},
		ModelID:    model,
		BaseURL:    baseURL,
		WireSchema: schema,
	}
}

// captureServer records the decoded body, path, and headers of one request and
// replies with respBody (200).
type oauthCapture struct {
	path    string
	headers http.Header
	body    []byte
}

func oauthCaptureServer(t *testing.T, cap *oauthCapture, respBody string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.path = r.URL.Path
		cap.headers = r.Header.Clone()
		cap.body, _ = readAllBody(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(respBody))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func readAllBody(r *http.Request) ([]byte, error) {
	buf := make([]byte, 0, 1024)
	tmp := make([]byte, 512)
	for {
		n, err := r.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			return buf, nil
		}
	}
}

const anthropicOKResponse = `{"role":"assistant","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn"}`

// TestAnthropic_RequestMapping covers mutations 4 (system to top-level) and 5
// (required max_tokens): a system message lands in the top-level system field
// and NOT in messages; a system-less request omits system; max_tokens is always
// present (default when none, explicit when set); tools map to input_schema
// verbatim.
func TestAnthropic_RequestMapping(t *testing.T) {
	t.Run("system to top-level, max_tokens defaulted", func(t *testing.T) {
		var cap oauthCapture
		srv := oauthCaptureServer(t, &cap, anthropicOKResponse)
		tr := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)
		_, err := tr.Execute(context.Background(), oauthRoute(srv.URL, WireSchemaAnthropicMessages, "claude-x", "tok"), NormalizedRequest{
			Messages: []Message{{Role: "system", Content: "be terse"}, {Role: "user", Content: "hi"}},
		})
		if err != nil {
			t.Fatalf("Execute error = %v", err)
		}
		var body anthropicMessagesRequest
		if err := json.Unmarshal(cap.body, &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.System != "be terse" {
			t.Fatalf("system = %q, want 'be terse' (top-level)", body.System)
		}
		if len(body.Messages) != 1 || body.Messages[0].Role != "user" {
			t.Fatalf("messages = %+v, want only the user turn (system must NOT be a message)", body.Messages)
		}
		if body.MaxTokens != DefaultAnthropicMaxTokens {
			t.Fatalf("max_tokens = %d, want the default %d (Anthropic requires it)", body.MaxTokens, DefaultAnthropicMaxTokens)
		}
		if body.Model != "claude-x" {
			t.Fatalf("model = %q, want claude-x", body.Model)
		}
	})

	t.Run("no system omits the field; explicit max_tokens honored", func(t *testing.T) {
		var cap oauthCapture
		srv := oauthCaptureServer(t, &cap, anthropicOKResponse)
		tr := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)
		mt := 256
		_, err := tr.Execute(context.Background(), oauthRoute(srv.URL, WireSchemaAnthropicMessages, "claude-x", "tok"), NormalizedRequest{
			Messages: []Message{{Role: "user", Content: "hi"}}, MaxTokens: &mt,
		})
		if err != nil {
			t.Fatalf("Execute error = %v", err)
		}
		if strings.Contains(string(cap.body), `"system"`) {
			t.Fatalf("body carries a system field with no system message: %s", cap.body)
		}
		var body anthropicMessagesRequest
		_ = json.Unmarshal(cap.body, &body)
		if body.MaxTokens != 256 {
			t.Fatalf("max_tokens = %d, want 256", body.MaxTokens)
		}
	})

	t.Run("tools map to input_schema verbatim", func(t *testing.T) {
		var cap oauthCapture
		srv := oauthCaptureServer(t, &cap, anthropicOKResponse)
		tr := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)
		_, err := tr.Execute(context.Background(), oauthRoute(srv.URL, WireSchemaAnthropicMessages, "claude-x", "tok"), NormalizedRequest{
			Messages: []Message{{Role: "user", Content: "hi"}},
			Tools:    []ToolDefinition{{Name: "get_weather", Description: "look up", ParametersJSON: `{"type":"object"}`}},
		})
		if err != nil {
			t.Fatalf("Execute error = %v", err)
		}
		var body anthropicMessagesRequest
		_ = json.Unmarshal(cap.body, &body)
		if len(body.Tools) != 1 || body.Tools[0].Name != "get_weather" {
			t.Fatalf("tools = %+v, want one named get_weather", body.Tools)
		}
		var schema map[string]any
		if err := json.Unmarshal(body.Tools[0].InputSchema, &schema); err != nil || schema["type"] != "object" {
			t.Fatalf("input_schema = %s (err %v), want the verbatim client schema", body.Tools[0].InputSchema, err)
		}
	})
}

// TestAnthropic_URLImageFailsClosed proves a URL-only image is rejected with
// the typed error and NO request is sent.
func TestAnthropic_URLImageFailsClosed(t *testing.T) {
	var count int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&count, 1)
		_, _ = w.Write([]byte(anthropicOKResponse))
	}))
	t.Cleanup(srv.Close)
	tr := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)
	_, err := tr.Execute(context.Background(), oauthRoute(srv.URL, WireSchemaAnthropicMessages, "claude-x", "tok"), NormalizedRequest{
		Messages: []Message{{Role: "user", Parts: []ContentPart{{Kind: ContentPartImage, ImageURL: "https://example.com/cat.png"}}}},
	})
	if !errors.Is(err, ErrRequestFeatureUnsupported) {
		t.Fatalf("error = %v, want ErrRequestFeatureUnsupported", err)
	}
	if atomic.LoadInt64(&count) != 0 {
		t.Fatalf("server received %d requests, want 0", count)
	}
}

// TestAnthropic_ResponseDecodeAndEndpoint proves text blocks concatenate, a
// tool_use block becomes a ToolCall, stop_reason maps to FinishReason, and the
// request hits /v1/messages.
func TestAnthropic_ResponseDecodeAndEndpoint(t *testing.T) {
	var cap oauthCapture
	srv := oauthCaptureServer(t, &cap, `{"role":"assistant","content":[{"type":"text","text":"Hel"},{"type":"text","text":"lo"},{"type":"tool_use","name":"get_weather","input":{"city":"Paris"}}],"stop_reason":"tool_use"}`)
	tr := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)
	resp, err := tr.Execute(context.Background(), oauthRoute(srv.URL, WireSchemaAnthropicMessages, "claude-x", "tok"), NormalizedRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if resp.Message.Content != "Hello" {
		t.Fatalf("content = %q, want Hello", resp.Message.Content)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "get_weather" || !strings.Contains(resp.ToolCalls[0].ArgumentsJSON, "Paris") {
		t.Fatalf("tool calls = %+v, want get_weather(Paris)", resp.ToolCalls)
	}
	if resp.FinishReason != "tool_use" {
		t.Fatalf("finish = %q, want tool_use", resp.FinishReason)
	}
	if cap.path != "/v1/messages" {
		t.Fatalf("path = %q, want /v1/messages", cap.path)
	}
}

// TestAnthropic_Headers is mutation row 3: the required headers are present on
// BOTH Execute and Stream. Missing any is a 429 outage (03 §3).
func TestAnthropic_Headers(t *testing.T) {
	assertHeaders := func(t *testing.T, h http.Header) {
		t.Helper()
		if h.Get("anthropic-version") == "" {
			t.Error("anthropic-version header missing")
		}
		if !strings.Contains(h.Get("anthropic-beta"), "oauth-2025-04-20") {
			t.Errorf("anthropic-beta = %q, want to contain oauth-2025-04-20", h.Get("anthropic-beta"))
		}
		if h.Get("X-App") != "cli" {
			t.Errorf("X-App = %q, want cli", h.Get("X-App"))
		}
		if !strings.HasPrefix(h.Get("User-Agent"), "claude-cli/") {
			t.Errorf("User-Agent = %q, want claude-cli/ prefix", h.Get("User-Agent"))
		}
	}

	t.Run("execute", func(t *testing.T) {
		var cap oauthCapture
		srv := oauthCaptureServer(t, &cap, anthropicOKResponse)
		tr := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)
		if _, err := tr.Execute(context.Background(), oauthRoute(srv.URL, WireSchemaAnthropicMessages, "claude-x", "tok"), NormalizedRequest{Messages: []Message{{Role: "user", Content: "hi"}}}); err != nil {
			t.Fatalf("Execute error = %v", err)
		}
		assertHeaders(t, cap.headers)
	})

	t.Run("stream", func(t *testing.T) {
		var gotHeaders http.Header
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotHeaders = r.Header.Clone()
			w.Header().Set("Content-Type", "text/event-stream")
			fl, _ := w.(http.Flusher)
			_, _ = w.Write([]byte("data: {\"type\":\"message_stop\"}\n\n"))
			if fl != nil {
				fl.Flush()
			}
		}))
		t.Cleanup(srv.Close)
		tr := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)
		ch, err := tr.Stream(context.Background(), oauthRoute(srv.URL, WireSchemaAnthropicMessages, "claude-x", "tok"), NormalizedRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
		if err != nil {
			t.Fatalf("Stream error = %v", err)
		}
		for range ch { //nolint:revive // drain
		}
		assertHeaders(t, gotHeaders)
	})
}

// TestNativeOAuth_EmptySchemaFailsClosed is mutation row 6: a native_oauth route
// with an empty (or unknown) schema returns a typed error and makes NO HTTP
// call — never defaulting to the Gemini path.
func TestNativeOAuth_EmptySchemaFailsClosed(t *testing.T) {
	for _, schema := range []WireSchema{"", WireSchema("responses")} {
		var count int64
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			atomic.AddInt64(&count, 1)
			_, _ = w.Write([]byte(`{}`))
		}))
		tr := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)
		_, err := tr.Execute(context.Background(), oauthRoute(srv.URL, schema, "m", "tok"), NormalizedRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
		if !errors.Is(err, ErrUnsupportedWireSchema) {
			t.Errorf("schema %q: error = %v, want ErrUnsupportedWireSchema", schema, err)
		}
		if atomic.LoadInt64(&count) != 0 {
			t.Errorf("schema %q: server received %d requests, want 0 (fail closed before any call)", schema, count)
		}
		srv.Close()
	}
}

// TestOpenAIChat_BodyAndWireCredential proves the openai_chat schema reuses the
// OpenAI body mapping (correct model/messages/stream at /chat/completions), and
// is mutation row 8: the workos: prefix is applied AT THE WIRE
// (Authorization: Bearer workos:<raw>) while the route's stored credential
// value stays the raw token.
func TestOpenAIChat_BodyAndWireCredential(t *testing.T) {
	var cap oauthCapture
	srv := oauthCaptureServer(t, &cap, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	tr := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)
	route := oauthRoute(srv.URL, WireSchemaOpenAIChat, "cline-model", "rawtoken")
	_, err := tr.Execute(context.Background(), route, NormalizedRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if cap.path != "/chat/completions" {
		t.Fatalf("path = %q, want /chat/completions", cap.path)
	}
	var body chatCompletionRequestBody
	if err := json.Unmarshal(cap.body, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Model != "cline-model" || len(body.Messages) != 1 {
		t.Fatalf("body = %+v, want model cline-model + one message (OpenAI mapping reused)", body)
	}
	if got := cap.headers.Get("Authorization"); got != "Bearer workos:rawtoken" {
		t.Fatalf("Authorization = %q, want 'Bearer workos:rawtoken' (prefix applied at the wire)", got)
	}
	if cap.headers.Get("X-CLIENT-TYPE") != "venom-router" {
		t.Fatalf("X-CLIENT-TYPE = %q, want venom-router", cap.headers.Get("X-CLIENT-TYPE"))
	}
	if route.Credential.Value != "rawtoken" {
		t.Fatalf("stored credential = %q, want the untouched raw token", route.Credential.Value)
	}
}

// TestOpenAIChat_WorkosPrefixIdempotent is mutation row 7 (clinepass): a token
// already carrying the workos: prefix must not be double-prefixed.
func TestOpenAIChat_WorkosPrefixIdempotent(t *testing.T) {
	var cap oauthCapture
	srv := oauthCaptureServer(t, &cap, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	tr := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)
	_, err := tr.Execute(context.Background(), oauthRoute(srv.URL, WireSchemaOpenAIChat, "m", "workos:already"), NormalizedRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if got := cap.headers.Get("Authorization"); got != "Bearer workos:already" {
		t.Fatalf("Authorization = %q, want 'Bearer workos:already' (no double prefix)", got)
	}
}

// TestOpenAIChat_BodyMatchesOpenAICompatTransport proves the native_oauth
// openai_chat body is byte-identical to what OpenAICompatibleTransport produces
// for the same request — the SAME mapping, not a second one.
func TestOpenAIChat_BodyMatchesOpenAICompatTransport(t *testing.T) {
	req := NormalizedRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
		Tools:    []ToolDefinition{{Name: "t", ParametersJSON: `{"type":"object"}`}},
	}
	var oauthCap, compatCap oauthCapture
	oauthSrv := oauthCaptureServer(t, &oauthCap, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	compatSrv := oauthCaptureServer(t, &compatCap, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)

	oauthTr := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)
	if _, err := oauthTr.Execute(context.Background(), oauthRoute(oauthSrv.URL, WireSchemaOpenAIChat, "m", "tok"), req); err != nil {
		t.Fatalf("oauth execute: %v", err)
	}
	compatTr := NewOpenAICompatibleTransport(&http.Client{}, 5*time.Second)
	if _, err := compatTr.Execute(context.Background(), ResolvedRoute{ModelID: "m", BaseURL: compatSrv.URL, Credential: StoredCredentials{Value: "tok"}}, req); err != nil {
		t.Fatalf("compat execute: %v", err)
	}
	if string(oauthCap.body) != string(compatCap.body) {
		t.Fatalf("bodies differ:\n oauth = %s\ncompat = %s", oauthCap.body, compatCap.body)
	}
}

// TestNativeOAuth_Streaming_PerSchema proves ordered deltas + terminal Done for
// anthropic and openai_chat, and that a mid-stream error surfaces as Chunk.Err.
func TestNativeOAuth_Streaming_PerSchema(t *testing.T) {
	t.Run("anthropic ordered then done", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fl, _ := w.(http.Flusher)
			for _, p := range []string{"Hel", "lo"} {
				_, _ = w.Write([]byte("data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"" + p + "\"}}\n\n"))
				fl.Flush()
			}
			_, _ = w.Write([]byte("data: {\"type\":\"message_stop\"}\n\n"))
			fl.Flush()
		}))
		t.Cleanup(srv.Close)
		assertStream(t, WireSchemaAnthropicMessages, srv.URL, "Hello")
	})

	t.Run("openai ordered then done", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fl, _ := w.(http.Flusher)
			for _, p := range []string{"Hel", "lo"} {
				_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"" + p + "\"}}]}\n\n"))
				fl.Flush()
			}
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
			fl.Flush()
		}))
		t.Cleanup(srv.Close)
		assertStream(t, WireSchemaOpenAIChat, srv.URL, "Hello")
	})

	t.Run("anthropic mid-stream error surfaces", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fl, _ := w.(http.Flusher)
			_, _ = w.Write([]byte("data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"x\"}}\n\n"))
			fl.Flush()
			_, _ = w.Write([]byte("data: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\"}}\n\n"))
			fl.Flush()
		}))
		t.Cleanup(srv.Close)
		tr := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)
		ch, err := tr.Stream(context.Background(), oauthRoute(srv.URL, WireSchemaAnthropicMessages, "m", "tok"), NormalizedRequest{Messages: []Message{{Role: "user", Content: "hi"}}, RequestID: "r"})
		if err != nil {
			t.Fatalf("Stream error = %v", err)
		}
		var sawErr bool
		for c := range ch {
			if c.Err != nil {
				sawErr = true
			}
		}
		if !sawErr {
			t.Fatal("a mid-stream anthropic error event must surface as Chunk.Err")
		}
	})
}

func assertStream(t *testing.T, schema WireSchema, url, want string) {
	t.Helper()
	tr := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)
	ch, err := tr.Stream(context.Background(), oauthRoute(url, schema, "m", "tok"), NormalizedRequest{Messages: []Message{{Role: "user", Content: "hi"}}, RequestID: "r"})
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	var got strings.Builder
	var done bool
	for c := range ch {
		if c.Err != nil {
			t.Fatalf("unexpected chunk error: %v", c.Err)
		}
		got.WriteString(c.Delta)
		if c.Done {
			done = true
		}
	}
	if got.String() != want {
		t.Fatalf("deltas = %q, want %q", got.String(), want)
	}
	if !done {
		t.Fatal("stream never signalled Done")
	}
}

// TestNativeOAuth_CredentialNeverInError proves no schema leaks the credential
// into a returned error.
func TestNativeOAuth_CredentialNeverInError(t *testing.T) {
	const secret = "super-secret-oauth-token"
	for _, schema := range []WireSchema{WireSchemaGoogleGenerateContent, WireSchemaAnthropicMessages, WireSchemaOpenAIChat} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"nope"}}`))
		}))
		tr := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)
		_, err := tr.Execute(context.Background(), oauthRoute(srv.URL, schema, "m", secret), NormalizedRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
		if err != nil && strings.Contains(err.Error(), secret) {
			t.Errorf("schema %q: error leaks credential: %v", schema, err)
		}
		srv.Close()
	}
}
