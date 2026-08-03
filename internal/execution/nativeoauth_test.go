package execution

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newNativeOAuthTestRoute(baseURL string) ResolvedRoute {
	return ResolvedRoute{
		Provider:   ProviderID("antigravity"),
		AccountID:  "acct_probe",
		Credential: StoredCredentials{Value: "ya29.canary-access-token"},
		ModelID:    "gemini-1.5-pro",
		BaseURL:    baseURL,
		WireSchema: WireSchemaGoogleGenerateContent,
	}
}

// nativeOAuthSingleCandidateServer starts a server that records every
// decoded request body into dst and replies with a fixed single-candidate
// Gemini generateContent success response.
func nativeOAuthSingleCandidateServer(t *testing.T, dst *geminiGenerateReq) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(dst)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(geminiGenerateResp{
			Candidates: []geminiCandidate{
				{
					Content: geminiContent{
						Role:  "model",
						Parts: []geminiPart{{Text: strPtr("hello")}},
					},
					FinishReason: "STOP",
				},
			},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func strPtr(s string) *string { return &s }

// TestNativeOAuth_SchemaRoundTrip_UserMessageMapsToUserRole is mutation
// row 1.1: "user" must arrive at the provider as role "user"; "assistant"
// must become "model" (Gemini's role vocabulary); omitting or swapping the
// mapping breaks the provider schema.
func TestNativeOAuth_SchemaRoundTrip_UserMessageMapsToUserRole(t *testing.T) {
	var got geminiGenerateReq
	srv := nativeOAuthSingleCandidateServer(t, &got)
	transport := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)

	_, err := transport.Execute(context.Background(), newNativeOAuthTestRoute(srv.URL), NormalizedRequest{
		Messages: []Message{
			{Role: "user", Content: "ping"},
			{Role: "assistant", Content: "pong"},
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(got.Contents) != 2 {
		t.Fatalf("contents length = %d, want 2", len(got.Contents))
	}
	if got.Contents[0].Role != "user" {
		t.Fatalf("contents[0].role = %q, want %q", got.Contents[0].Role, "user")
	}
	if got.Contents[1].Role != "model" {
		t.Fatalf("contents[1].role = %q, want %q (assistant must become model)", got.Contents[1].Role, "model")
	}
}

// TestNativeOAuth_SchemaRoundTrip_MaxTokensInGenerationConfig is mutation
// row 1.2: a non-nil MaxTokens must appear inside generationConfig.
// maxOutputTokens; nil must omit the key entirely (never 0).
func TestNativeOAuth_SchemaRoundTrip_MaxTokensInGenerationConfig(t *testing.T) {
	t.Run("sends when set", func(t *testing.T) {
		var got geminiGenerateReq
		srv := nativeOAuthSingleCandidateServer(t, &got)
		transport := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)
		maxTokens := 512

		_, err := transport.Execute(context.Background(), newNativeOAuthTestRoute(srv.URL), NormalizedRequest{
			Messages:  []Message{{Role: "user", Content: "hi"}},
			MaxTokens: &maxTokens,
		})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if got.GenerationConfig == nil {
			t.Fatalf("generationConfig is nil, want present (maxOutputTokens = 512)")
		}
		if got.GenerationConfig.MaxOutputTokens == nil || *got.GenerationConfig.MaxOutputTokens != 512 {
			t.Fatalf("generationConfig.maxOutputTokens = %v, want 512", got.GenerationConfig.MaxOutputTokens)
		}
	})

	t.Run("omits when nil", func(t *testing.T) {
		var got geminiGenerateReq
		srv := nativeOAuthSingleCandidateServer(t, &got)
		transport := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)

		_, err := transport.Execute(context.Background(), newNativeOAuthTestRoute(srv.URL), NormalizedRequest{
			Messages: []Message{{Role: "user", Content: "hi"}},
		})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if got.GenerationConfig != nil && got.GenerationConfig.MaxOutputTokens != nil {
			t.Fatalf("generationConfig.maxOutputTokens = %v, want absent (nil MaxTokens)", got.GenerationConfig.MaxOutputTokens)
		}
	})
}

// TestNativeOAuth_BearerHeader is mutation row 1.3: the Authorization
// header must carry "Bearer <route.Credential.Value>" verbatim.
// Any other value (API-key style header, empty, or wrong credential)
// breaks the Gemini auth contract.
func TestNativeOAuth_BearerHeader(t *testing.T) {
	var gotAuthHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(geminiGenerateResp{
			Candidates: []geminiCandidate{
				{Content: geminiContent{Role: "model", Parts: []geminiPart{{Text: strPtr("ok")}}}, FinishReason: "STOP"},
			},
		})
	}))
	t.Cleanup(srv.Close)

	route := newNativeOAuthTestRoute(srv.URL)
	transport := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)
	_, err := transport.Execute(context.Background(), route, NormalizedRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := "Bearer " + route.Credential.Value
	if gotAuthHeader != want {
		t.Fatalf("Authorization header = %q, want %q", gotAuthHeader, want)
	}
}

// TestNativeOAuth_URLPattern is mutation row 1.4: the path must be
// "/models/<ModelID>:generateContent" appended to BaseURL. If ModelID or
// the colon-verb suffix is missing, the provider rejects the request.
func TestNativeOAuth_URLPattern(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(geminiGenerateResp{
			Candidates: []geminiCandidate{
				{Content: geminiContent{Role: "model", Parts: []geminiPart{{Text: strPtr("ok")}}}, FinishReason: "STOP"},
			},
		})
	}))
	t.Cleanup(srv.Close)

	route := newNativeOAuthTestRoute(srv.URL) // ModelID = "gemini-1.5-pro"
	transport := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)
	_, err := transport.Execute(context.Background(), route, NormalizedRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.HasSuffix(gotPath, "/models/gemini-1.5-pro:generateContent") {
		t.Fatalf("request path = %q, want suffix %q", gotPath, "/models/gemini-1.5-pro:generateContent")
	}
}

// TestNativeOAuth_FunctionCallToolCall is mutation row 1.5: a Gemini
// functionCall part must map to ToolCall.Name and ToolCall.ArgumentsJSON.
// A transport that silently drops functionCall parts loses probe-path
// witness classification.
func TestNativeOAuth_FunctionCallToolCall(t *testing.T) {
	argsJSON := `{"city":"Paris"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(geminiGenerateResp{
			Candidates: []geminiCandidate{
				{
					Content: geminiContent{
						Role: "model",
						Parts: []geminiPart{
							{FunctionCall: &geminiFnCall{Name: "get_weather", Args: json.RawMessage(argsJSON)}},
						},
					},
					FinishReason: "STOP",
				},
			},
		})
	}))
	t.Cleanup(srv.Close)

	transport := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)
	resp, err := transport.Execute(context.Background(), newNativeOAuthTestRoute(srv.URL), NormalizedRequest{
		Messages: []Message{{Role: "user", Content: "weather in Paris?"}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("ToolCalls length = %d, want 1", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "get_weather" {
		t.Fatalf("ToolCalls[0].Name = %q, want %q", resp.ToolCalls[0].Name, "get_weather")
	}
	if resp.ToolCalls[0].ArgumentsJSON != argsJSON {
		t.Fatalf("ToolCalls[0].ArgumentsJSON = %q, want %q", resp.ToolCalls[0].ArgumentsJSON, argsJSON)
	}
}

// TestNativeOAuth_FailureCarriesStatusAndRawMessage is mutation row 1.6:
// a 400 rejection must carry HTTPStatus + ProviderCode + RawMessage (for
// the probe path), but SafeMessage must never equal the raw provider text.
func TestNativeOAuth_FailureCarriesStatusAndRawMessage(t *testing.T) {
	const rawMsg = "RESOURCE_EXHAUSTED: quota exceeded for account"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(geminiErrBody{Error: geminiErrDetail{
			Code:    http.StatusTooManyRequests,
			Status:  "RESOURCE_EXHAUSTED",
			Message: rawMsg,
		}})
	}))
	t.Cleanup(srv.Close)

	transport := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)
	route := newNativeOAuthTestRoute(srv.URL)
	_, err := transport.Execute(context.Background(), route, NormalizedRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("Execute() error = nil, want non-nil for 429")
	}

	failure := transport.Failure(err, route)
	if failure.HTTPStatus != http.StatusTooManyRequests {
		t.Fatalf("HTTPStatus = %d, want %d", failure.HTTPStatus, http.StatusTooManyRequests)
	}
	if failure.ProviderCode != "RESOURCE_EXHAUSTED" {
		t.Fatalf("ProviderCode = %q, want %q", failure.ProviderCode, "RESOURCE_EXHAUSTED")
	}
	if failure.RawMessage != rawMsg {
		t.Fatalf("RawMessage = %q, want %q", failure.RawMessage, rawMsg)
	}
	if failure.SafeMessage == rawMsg {
		t.Fatal("SafeMessage must never equal the raw provider text")
	}
}

// TestNativeOAuth_Failure401MapsToAuthError is mutation row 1.7: a 401
// response must classify as FailureClassAuth; misclassifying it as
// FailureClassRateLimit or FailureClassServer would send the tier engine
// down the wrong recovery path.
func TestNativeOAuth_Failure401MapsToAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(geminiErrBody{Error: geminiErrDetail{
			Code: http.StatusUnauthorized, Status: "UNAUTHENTICATED", Message: "invalid credentials",
		}})
	}))
	t.Cleanup(srv.Close)

	transport := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)
	route := newNativeOAuthTestRoute(srv.URL)
	_, err := transport.Execute(context.Background(), route, NormalizedRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("Execute() error = nil, want non-nil for 401")
	}
	failure := transport.Failure(err, route)
	if failure.FailureClass != FailureClassAuth {
		t.Fatalf("FailureClass = %q, want %q", failure.FailureClass, FailureClassAuth)
	}
}

// TestNativeOAuth_NormalizeError_NeverLeaksCredential is a canary: the
// string produced by NormalizeError must not contain the credential value.
func TestNativeOAuth_NormalizeError_NeverLeaksCredential(t *testing.T) {
	transport := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)
	route := newNativeOAuthTestRoute("http://127.0.0.1")
	ve := transport.NormalizeError(errors.New("some internal error"), route)
	if strings.Contains(ve.Message, route.Credential.Value) {
		t.Fatalf("NormalizeError.Message contains the credential value — SECRET-SAFETY violation")
	}
}

// TestNativeOAuth_TimeoutAndNetworkAreTyped is mutation row 1.8: a hung
// server maps to ErrTransportTimeout, a closed port to ErrTransportNetwork.
// Failure.HTTPStatus must be 0 for both (never a fabricated status code).
func TestNativeOAuth_TimeoutAndNetworkAreTyped(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		block := make(chan struct{})
		hung := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-block
		}))
		t.Cleanup(hung.Close)
		t.Cleanup(func() { close(block) })

		transport := NewNativeOAuthTransport(&http.Client{}, 100*time.Millisecond)
		route := newNativeOAuthTestRoute(hung.URL)
		_, err := transport.Execute(context.Background(), route, NormalizedRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
		if !errors.Is(err, ErrTransportTimeout) {
			t.Fatalf("Execute() error = %v, want ErrTransportTimeout", err)
		}
		failure := transport.Failure(err, route)
		if failure.HTTPStatus != 0 {
			t.Fatalf("HTTPStatus = %d, want 0 for timeout", failure.HTTPStatus)
		}
	})

	t.Run("network", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("net.Listen: %v", err)
		}
		closedPortURL := "http://" + ln.Addr().String()
		if err := ln.Close(); err != nil {
			t.Fatalf("close listener: %v", err)
		}

		transport := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)
		route := newNativeOAuthTestRoute(closedPortURL)
		_, execErr := transport.Execute(context.Background(), route, NormalizedRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
		if !errors.Is(execErr, ErrTransportNetwork) {
			t.Fatalf("Execute() error = %v, want ErrTransportNetwork", execErr)
		}
		failure := transport.Failure(execErr, route)
		if failure.HTTPStatus != 0 {
			t.Fatalf("HTTPStatus = %d, want 0 for network failure", failure.HTTPStatus)
		}
	})
}

// TestNativeOAuth_SupportedCapabilities_IncludesChat is mutation row 1.9:
// SupportedCapabilities must include OperationChat; excluding it would
// prevent any chat offering from reaching certified state for this transport.
func TestNativeOAuth_SupportedCapabilities_IncludesChat(t *testing.T) {
	transport := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)
	caps := transport.SupportedCapabilities(newNativeOAuthTestRoute("http://127.0.0.1"))
	for _, c := range caps {
		if c == OperationChat {
			return
		}
	}
	t.Fatalf("SupportedCapabilities = %v, want to include OperationChat", caps)
}

// TestNativeOAuth_SystemMessageGoesToSystemInstruction proves a system
// message is routed into Gemini's top-level systemInstruction field and NOT
// sent as a `contents` turn (where geminiRoleFor would label it "model",
// making the model appear to have said the system prompt), and that a request
// WITHOUT a system message emits no systemInstruction at all. The builder is
// shared, so the same guarantee is asserted on the native_api side too.
func TestNativeOAuth_SystemMessageGoesToSystemInstruction(t *testing.T) {
	t.Run("system routed to systemInstruction, not contents", func(t *testing.T) {
		var got geminiGenerateReq
		srv := nativeOAuthSingleCandidateServer(t, &got)
		transport := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)

		_, err := transport.Execute(context.Background(), newNativeOAuthTestRoute(srv.URL), NormalizedRequest{
			Messages: []Message{
				{Role: "system", Content: "be terse"},
				{Role: "user", Content: "hi"},
			},
		})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if len(got.Contents) != 1 {
			t.Fatalf("contents length = %d, want 1 (only the user turn; system must not be a content turn)", len(got.Contents))
		}
		if got.Contents[0].Role != "user" {
			t.Fatalf("contents[0].role = %q, want %q", got.Contents[0].Role, "user")
		}
		if got.SystemInstruction == nil || len(got.SystemInstruction.Parts) != 1 || got.SystemInstruction.Parts[0].Text == nil || *got.SystemInstruction.Parts[0].Text != "be terse" {
			t.Fatalf("systemInstruction = %+v, want the system text 'be terse'", got.SystemInstruction)
		}
	})

	t.Run("no system message omits systemInstruction", func(t *testing.T) {
		var got geminiGenerateReq
		srv := nativeOAuthSingleCandidateServer(t, &got)
		transport := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)

		_, err := transport.Execute(context.Background(), newNativeOAuthTestRoute(srv.URL), NormalizedRequest{
			Messages: []Message{{Role: "user", Content: "hi"}},
		})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if got.SystemInstruction != nil {
			t.Fatalf("systemInstruction = %+v, want nil (no system message present)", got.SystemInstruction)
		}
	})
}

// --- P5-EXEC-004: tools + multimodal content ------------------------------

// nativeOAuthCountingServer counts every request it receives (any path), so a
// fail-closed test can assert ZERO upstream calls.
func nativeOAuthCountingServer(t *testing.T) (*httptest.Server, *int64) {
	t.Helper()
	var count int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&count, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(geminiGenerateResp{Candidates: []geminiCandidate{{Content: geminiContent{Role: "model", Parts: []geminiPart{{Text: strPtr("x")}}}, FinishReason: "STOP"}}})
	}))
	t.Cleanup(srv.Close)
	return srv, &count
}

// TestNativeOAuth_ToolsMapToFunctionDeclarations proves tools reach Gemini as
// functionDeclarations with the parameters embedded as a JSON object.
func TestNativeOAuth_ToolsMapToFunctionDeclarations(t *testing.T) {
	var got geminiGenerateReq
	srv := nativeOAuthSingleCandidateServer(t, &got)

	transport := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)
	if _, err := transport.Execute(context.Background(), newNativeOAuthTestRoute(srv.URL), NormalizedRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
		Tools:    []ToolDefinition{{Name: "get_weather", Description: "look up", ParametersJSON: `{"type":"object"}`}},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(got.Tools) != 1 || len(got.Tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("tools = %+v, want one functionDeclarations entry", got.Tools)
	}
	decl := got.Tools[0].FunctionDeclarations[0]
	if decl.Name != "get_weather" || decl.Description != "look up" {
		t.Fatalf("declaration = %+v, want name/description round-tripped", decl)
	}
	var params map[string]any
	if err := json.Unmarshal(decl.Parameters, &params); err != nil || params["type"] != "object" {
		t.Fatalf("parameters must be embedded JSON object, got %s (err=%v)", decl.Parameters, err)
	}
}

// TestNativeOAuth_InlineImagePart proves a base64 image maps to inlineData with
// its media type and data.
func TestNativeOAuth_InlineImagePart(t *testing.T) {
	var got geminiGenerateReq
	srv := nativeOAuthSingleCandidateServer(t, &got)

	transport := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)
	if _, err := transport.Execute(context.Background(), newNativeOAuthTestRoute(srv.URL), NormalizedRequest{
		Messages: []Message{{Role: "user", Parts: []ContentPart{
			{Kind: ContentPartText, Text: "what is this?"},
			{Kind: ContentPartImage, ImageBase64: "aGVsbG8=", MediaType: "image/png"},
		}}},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(got.Contents) != 1 || len(got.Contents[0].Parts) != 2 {
		t.Fatalf("contents = %+v, want 1 content with 2 parts", got.Contents)
	}
	img := got.Contents[0].Parts[1].InlineData
	if img == nil || img.MimeType != "image/png" || img.Data != "aGVsbG8=" {
		t.Fatalf("inlineData = %+v, want image/png + base64 data", img)
	}
}

// TestNativeOAuth_URLImageFailsClosed proves a URL-only image (inexpressible as
// Gemini inlineData) is rejected with the typed error and NO request is sent.
//
// Mutation E4-M5: silently skip the inexpressible image part instead of
// returning the typed error → Execute proceeds and the upstream is called → RED.
// (The prompt's "drop tools silently" maps here to the image half of "silently
// dropping a tool definition OR an image is a rejectable defect"; native-OAuth
// DOES express tools via functionDeclarations, so the inexpressible feature
// tested is the URL image — see the report's ambiguity note.)
func TestNativeOAuth_URLImageFailsClosed(t *testing.T) {
	srv, count := nativeOAuthCountingServer(t)
	transport := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)

	_, err := transport.Execute(context.Background(), newNativeOAuthTestRoute(srv.URL), NormalizedRequest{
		Messages: []Message{{Role: "user", Parts: []ContentPart{{Kind: ContentPartImage, ImageURL: "https://example.com/cat.png"}}}},
	})
	if !errors.Is(err, ErrRequestFeatureUnsupported) {
		t.Fatalf("Execute() error = %v, want ErrRequestFeatureUnsupported", err)
	}
	if atomic.LoadInt64(count) != 0 {
		t.Fatalf("upstream received %d requests, want 0", atomic.LoadInt64(count))
	}
}

// TestNativeOAuth_ToolChoiceFailsClosed proves a tool_choice directive, which
// has no faithful Gemini mapping, is rejected with the typed error and no call.
func TestNativeOAuth_ToolChoiceFailsClosed(t *testing.T) {
	srv, count := nativeOAuthCountingServer(t)
	transport := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)

	_, err := transport.Execute(context.Background(), newNativeOAuthTestRoute(srv.URL), NormalizedRequest{
		Messages:   []Message{{Role: "user", Content: "hi"}},
		ToolChoice: "required",
	})
	if !errors.Is(err, ErrRequestFeatureUnsupported) {
		t.Fatalf("Execute() error = %v, want ErrRequestFeatureUnsupported", err)
	}
	if atomic.LoadInt64(count) != 0 {
		t.Fatalf("upstream received %d requests, want 0", atomic.LoadInt64(count))
	}
}
