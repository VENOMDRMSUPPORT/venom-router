package execution

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// oneByOnePNG is a 1x1 PNG, base64, used only to exercise an image part.
const oneByOnePNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

// requestFor builds the minimal request that exercises one operation.
func requestFor(op Operation) NormalizedRequest {
	req := NormalizedRequest{Operation: OperationChat, Messages: []Message{{Role: "user", Content: "hi"}}}
	switch op {
	case OperationStreaming:
		req.Stream = true
	case OperationTools:
		req.Tools = []ToolDefinition{{Name: "add", Description: "adds", ParametersJSON: `{"type":"object"}`}}
	case OperationVision:
		req.Messages = []Message{{Role: "user", Parts: []ContentPart{
			{Kind: ContentPartText, Text: "what colour"},
			{Kind: ContentPartImage, ImageBase64: oneByOnePNG, MediaType: "image/png"},
		}}}
	case OperationStructuredOutput:
		req.ResponseFormat = "json_object"
	}
	return req
}

// declaredCapabilitiesFixedReplyServer starts an httptest server that accepts
// any decodable JSON body and replies with a fixed, minimal chat-completion
// success body — the OpenAI-compatible transport case below only cares
// whether Execute returns ErrRequestFeatureUnsupported before ever reaching
// the network, so the reply body's content is irrelevant.
func declaredCapabilitiesFixedReplyServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var discard map[string]any
		_ = json.NewDecoder(r.Body).Decode(&discard)
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

// TestDeclaredCapabilitiesAreExpressible proves each transport/codec declares
// only operations its own request builder can actually serialize. Deleting a
// serialization branch (or making the builder fail closed on a feature it
// claims to support) must break this test — that is the point of a
// falsifiable declaration.
func TestDeclaredCapabilitiesAreExpressible(t *testing.T) {
	openAICompatSrv := declaredCapabilitiesFixedReplyServer(t)
	openAICompatTransport := NewOpenAICompatibleTransport(&http.Client{}, 0)

	cases := []struct {
		name  string
		build func(NormalizedRequest) error
		decl  []Operation
	}{
		{
			// The real request-body construction for OpenAICompatibleTransport
			// is two inline chatCompletionRequestBody literals inside Execute
			// and Stream (openaicompat.go), not a standalone
			// buildOpenAICompatChatRequest function. Driving it through a real
			// httptest server via Execute exercises that literal directly —
			// the composition root, not a parallel test-owned copy of it.
			name: "openai_compatible",
			build: func(q NormalizedRequest) error {
				_, err := openAICompatTransport.Execute(context.Background(), newOpenAICompatTestRoute(openAICompatSrv.URL), q)
				return err
			},
			decl: (&OpenAICompatibleTransport{}).SupportedCapabilities(ResolvedRoute{}),
		},
		{
			// openAIChatCodec (native_oauth's clinepass wire) shares
			// buildChatMessages/buildChatTools/buildChatResponseFormat with
			// OpenAICompatibleTransport, but its own capabilities() declaration
			// is a separate method — exercise it through its own buildPayload,
			// the function the transport actually calls.
			name: "openai_chat_native_oauth",
			build: func(q NormalizedRequest) error {
				_, err := openAIChatCodec{}.buildPayload(ResolvedRoute{ModelID: "m"}, q, q.Stream)
				return err
			},
			decl: openAIChatCodec{}.capabilities(),
		},
		{
			name: "anthropic_messages",
			build: func(q NormalizedRequest) error {
				_, err := buildAnthropicRequest(q, "m", q.Stream)
				return err
			},
			decl: anthropicMessagesCodec{}.capabilities(),
		},
		{
			name: "google_generate_content",
			build: func(q NormalizedRequest) error {
				_, err := buildGeminiRequest(q)
				return err
			},
			decl: googleGenerateContentCodec{}.capabilities(),
		},
		{
			// NativeAPITransport declares its own SupportedCapabilities method
			// (nativeapi.go) even though it happens to share buildGeminiRequest
			// with the antigravity codec above — a distinct declaration site
			// must be exercised on its own, not assumed identical because the
			// list currently reads the same.
			name: "native_api",
			build: func(q NormalizedRequest) error {
				_, err := buildGeminiRequest(q)
				return err
			},
			decl: (&NativeAPITransport{}).SupportedCapabilities(ResolvedRoute{}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if len(tc.decl) == 0 {
				t.Fatalf("%s declares no capabilities", tc.name)
			}
			for _, op := range tc.decl {
				if op == OperationChat || op == OperationStreaming {
					continue // shape-independent; covered by the transport's own tests
				}
				if err := tc.build(requestFor(op)); errors.Is(err, ErrRequestFeatureUnsupported) {
					t.Fatalf("%s declares %q but its request builder fails closed on it", tc.name, op)
				}
			}
		})
	}
}
