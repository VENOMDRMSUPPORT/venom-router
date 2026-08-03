package execution

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newNativeAPITestRoute(baseURL string) ResolvedRoute {
	return ResolvedRoute{
		Provider:   ProviderID("gemini-cli"),
		AccountID:  "acct_probe",
		Credential: StoredCredentials{Value: "AIza.canary-goog-api-key"},
		ModelID:    "gemini-2.0-flash",
		BaseURL:    baseURL,
	}
}

// nativeAPICaptureServer records the decoded request body, the request path,
// and the two candidate auth headers, then replies with a fixed
// single-candidate success response.
type nativeAPICapture struct {
	body        geminiGenerateReq
	path        string
	googKey     string
	authHeader  string
	rawAuthSeen bool
}

func nativeAPICaptureServer(t *testing.T, cap *nativeAPICapture) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&cap.body)
		cap.path = r.URL.Path
		cap.googKey = r.Header.Get("x-goog-api-key")
		cap.authHeader = r.Header.Get("Authorization")
		_, cap.rawAuthSeen = r.Header["Authorization"]
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(geminiGenerateResp{
			Candidates: []geminiCandidate{
				{Content: geminiContent{Role: "model", Parts: []geminiPart{{Text: strPtr("hello")}}}, FinishReason: "STOP"},
			},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestNativeAPI_GoogAPIKeyHeader_NoAuthorization is mutation row 1: the
// outgoing request must carry x-goog-api-key == credential and MUST NOT carry
// any Authorization header (Gemini's API-key auth is not Bearer).
func TestNativeAPI_GoogAPIKeyHeader_NoAuthorization(t *testing.T) {
	var cap nativeAPICapture
	srv := nativeAPICaptureServer(t, &cap)
	route := newNativeAPITestRoute(srv.URL)
	transport := NewNativeAPITransport(&http.Client{}, 5*time.Second)

	if _, err := transport.Execute(context.Background(), route, NormalizedRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if cap.googKey != route.Credential.Value {
		t.Fatalf("x-goog-api-key = %q, want %q", cap.googKey, route.Credential.Value)
	}
	if cap.rawAuthSeen {
		t.Fatalf("Authorization header present (%q), want ABSENT for native_api", cap.authHeader)
	}
}

// TestNativeAPI_Endpoints is mutation row 2: Execute hits :generateContent,
// Stream hits :streamGenerateContent?alt=sse.
func TestNativeAPI_Endpoints(t *testing.T) {
	t.Run("execute uses generateContent", func(t *testing.T) {
		var cap nativeAPICapture
		srv := nativeAPICaptureServer(t, &cap)
		transport := NewNativeAPITransport(&http.Client{}, 5*time.Second)
		if _, err := transport.Execute(context.Background(), newNativeAPITestRoute(srv.URL), NormalizedRequest{
			Messages: []Message{{Role: "user", Content: "hi"}},
		}); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !strings.HasSuffix(cap.path, "/models/gemini-2.0-flash:generateContent") {
			t.Fatalf("execute path = %q, want suffix /models/gemini-2.0-flash:generateContent", cap.path)
		}
	})

	t.Run("stream uses streamGenerateContent alt=sse", func(t *testing.T) {
		var gotPath, gotQuery string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotQuery = r.URL.RawQuery
			w.Header().Set("Content-Type", "text/event-stream")
			fl, _ := w.(http.Flusher)
			_, _ = w.Write([]byte("data: " + geminiChunkJSON("hi", "STOP") + "\n\n"))
			if fl != nil {
				fl.Flush()
			}
		}))
		t.Cleanup(srv.Close)
		transport := NewNativeAPITransport(&http.Client{}, 5*time.Second)
		ch, err := transport.Stream(context.Background(), newNativeAPITestRoute(srv.URL), NormalizedRequest{
			Messages: []Message{{Role: "user", Content: "hi"}},
		})
		if err != nil {
			t.Fatalf("Stream() error = %v", err)
		}
		for range ch { //nolint:revive // drain
		}
		if !strings.HasSuffix(gotPath, "/models/gemini-2.0-flash:streamGenerateContent") {
			t.Fatalf("stream path = %q, want suffix :streamGenerateContent", gotPath)
		}
		if gotQuery != "alt=sse" {
			t.Fatalf("stream query = %q, want alt=sse", gotQuery)
		}
	})
}

// geminiChunkJSON builds one Gemini SSE candidate payload.
func geminiChunkJSON(text, finish string) string {
	b, _ := json.Marshal(geminiGenerateResp{Candidates: []geminiCandidate{
		{Content: geminiContent{Role: "model", Parts: []geminiPart{{Text: strPtr(text)}}}, FinishReason: finish},
	}})
	return string(b)
}

// TestNativeAPI_RequestMapping_SharedBuilder proves the request goes through
// the SHARED buildGeminiRequest: plain text, an inline base64 image, and tool
// definitions all round-trip on the decoded JSON.
func TestNativeAPI_RequestMapping_SharedBuilder(t *testing.T) {
	var cap nativeAPICapture
	srv := nativeAPICaptureServer(t, &cap)
	transport := NewNativeAPITransport(&http.Client{}, 5*time.Second)

	if _, err := transport.Execute(context.Background(), newNativeAPITestRoute(srv.URL), NormalizedRequest{
		Messages: []Message{{Role: "user", Parts: []ContentPart{
			{Kind: ContentPartText, Text: "what is this?"},
			{Kind: ContentPartImage, ImageBase64: "aGVsbG8=", MediaType: "image/png"},
		}}},
		Tools: []ToolDefinition{{Name: "get_weather", Description: "look up", ParametersJSON: `{"type":"object"}`}},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(cap.body.Contents) != 1 || len(cap.body.Contents[0].Parts) != 2 {
		t.Fatalf("contents = %+v, want 1 content with 2 parts", cap.body.Contents)
	}
	img := cap.body.Contents[0].Parts[1].InlineData
	if img == nil || img.MimeType != "image/png" || img.Data != "aGVsbG8=" {
		t.Fatalf("inlineData = %+v, want image/png + base64", img)
	}
	if len(cap.body.Tools) != 1 || len(cap.body.Tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("tools = %+v, want one functionDeclarations entry", cap.body.Tools)
	}
	if cap.body.Tools[0].FunctionDeclarations[0].Name != "get_weather" {
		t.Fatalf("tool name = %q, want get_weather", cap.body.Tools[0].FunctionDeclarations[0].Name)
	}
}

// TestNativeAPI_SystemMessageGoesToSystemInstruction is the native_api half of
// mutation row 3: the shared builder routes a system message into
// systemInstruction, not contents, and omits systemInstruction when none.
func TestNativeAPI_SystemMessageGoesToSystemInstruction(t *testing.T) {
	t.Run("system routed to systemInstruction", func(t *testing.T) {
		var cap nativeAPICapture
		srv := nativeAPICaptureServer(t, &cap)
		transport := NewNativeAPITransport(&http.Client{}, 5*time.Second)
		if _, err := transport.Execute(context.Background(), newNativeAPITestRoute(srv.URL), NormalizedRequest{
			Messages: []Message{{Role: "system", Content: "be terse"}, {Role: "user", Content: "hi"}},
		}); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if len(cap.body.Contents) != 1 || cap.body.Contents[0].Role != "user" {
			t.Fatalf("contents = %+v, want only the user turn", cap.body.Contents)
		}
		if cap.body.SystemInstruction == nil || len(cap.body.SystemInstruction.Parts) != 1 ||
			cap.body.SystemInstruction.Parts[0].Text == nil || *cap.body.SystemInstruction.Parts[0].Text != "be terse" {
			t.Fatalf("systemInstruction = %+v, want 'be terse'", cap.body.SystemInstruction)
		}
	})

	t.Run("omitted when no system message", func(t *testing.T) {
		var cap nativeAPICapture
		srv := nativeAPICaptureServer(t, &cap)
		transport := NewNativeAPITransport(&http.Client{}, 5*time.Second)
		if _, err := transport.Execute(context.Background(), newNativeAPITestRoute(srv.URL), NormalizedRequest{
			Messages: []Message{{Role: "user", Content: "hi"}},
		}); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if cap.body.SystemInstruction != nil {
			t.Fatalf("systemInstruction = %+v, want nil", cap.body.SystemInstruction)
		}
	})
}

// TestNativeAPI_Streaming proves chunks arrive in order and a terminal chunk
// carries Done, and that a mid-stream provider error surfaces as Chunk.Err.
func TestNativeAPI_Streaming(t *testing.T) {
	t.Run("ordered deltas then done", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fl, _ := w.(http.Flusher)
			for _, part := range []string{"Hel", "lo"} {
				_, _ = w.Write([]byte("data: " + geminiChunkJSON(part, "") + "\n\n"))
				if fl != nil {
					fl.Flush()
				}
			}
			_, _ = w.Write([]byte("data: " + geminiChunkJSON("!", "STOP") + "\n\n"))
			if fl != nil {
				fl.Flush()
			}
		}))
		t.Cleanup(srv.Close)
		transport := NewNativeAPITransport(&http.Client{}, 5*time.Second)
		ch, err := transport.Stream(context.Background(), newNativeAPITestRoute(srv.URL), NormalizedRequest{
			Messages: []Message{{Role: "user", Content: "hi"}}, RequestID: "r1",
		})
		if err != nil {
			t.Fatalf("Stream() error = %v", err)
		}
		var deltas []string
		var sawDone bool
		for c := range ch {
			if c.Err != nil {
				t.Fatalf("unexpected chunk error: %v", c.Err)
			}
			if c.Delta != "" {
				deltas = append(deltas, c.Delta)
			}
			if c.Done {
				sawDone = true
			}
		}
		if strings.Join(deltas, "") != "Hello!" {
			t.Fatalf("deltas = %v, want to join to 'Hello!'", deltas)
		}
		if !sawDone {
			t.Fatal("stream never signalled Done")
		}
	})

	t.Run("mid-stream malformed chunk surfaces as Chunk.Err", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fl, _ := w.(http.Flusher)
			_, _ = w.Write([]byte("data: " + geminiChunkJSON("part", "") + "\n\n"))
			if fl != nil {
				fl.Flush()
			}
			_, _ = w.Write([]byte("data: {not json\n\n"))
			if fl != nil {
				fl.Flush()
			}
		}))
		t.Cleanup(srv.Close)
		transport := NewNativeAPITransport(&http.Client{}, 5*time.Second)
		ch, err := transport.Stream(context.Background(), newNativeAPITestRoute(srv.URL), NormalizedRequest{
			Messages: []Message{{Role: "user", Content: "hi"}}, RequestID: "r2",
		})
		if err != nil {
			t.Fatalf("Stream() error = %v", err)
		}
		var sawErr bool
		for c := range ch {
			if c.Err != nil {
				sawErr = true
			}
		}
		if !sawErr {
			t.Fatal("a malformed mid-stream chunk must surface as Chunk.Err, never a silent truncation")
		}
	})
}

// TestNativeAPI_ErrorClassification is mutation row 6 (and the status ladder):
// 401->auth, 404->not_found, 429->rate_limit, 500->server; RawMessage carries
// the provider text while SafeMessage never does (and never the credential).
func TestNativeAPI_ErrorClassification(t *testing.T) {
	const rawMsg = "PERMISSION_DENIED: raw provider detail with AIza.canary-goog-api-key"
	cases := []struct {
		status  int
		status2 string
		want    FailureClass
	}{
		{http.StatusUnauthorized, "UNAUTHENTICATED", FailureClassAuth},
		{http.StatusNotFound, "NOT_FOUND", FailureClassNotFound},
		{http.StatusTooManyRequests, "RESOURCE_EXHAUSTED", FailureClassRateLimit},
		{http.StatusInternalServerError, "INTERNAL", FailureClassServer},
	}
	for _, tc := range cases {
		t.Run(tc.status2, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(geminiErrBody{Error: geminiErrDetail{Code: tc.status, Status: tc.status2, Message: rawMsg}})
			}))
			t.Cleanup(srv.Close)
			transport := NewNativeAPITransport(&http.Client{}, 5*time.Second)
			route := newNativeAPITestRoute(srv.URL)
			_, err := transport.Execute(context.Background(), route, NormalizedRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
			if err == nil {
				t.Fatalf("Execute() error = nil, want non-nil for %d", tc.status)
			}
			f := transport.Failure(err, route)
			if f.FailureClass != tc.want {
				t.Fatalf("FailureClass = %q, want %q", f.FailureClass, tc.want)
			}
			if f.RawMessage != rawMsg {
				t.Fatalf("RawMessage = %q, want the provider text", f.RawMessage)
			}
			if f.SafeMessage == rawMsg || strings.Contains(f.SafeMessage, "canary-goog-api-key") {
				t.Fatalf("SafeMessage = %q leaks raw provider text / credential", f.SafeMessage)
			}
		})
	}
}

// TestNativeAPI_NeverLeaksCredential proves no method returns the credential
// value in an error string.
func TestNativeAPI_NeverLeaksCredential(t *testing.T) {
	transport := NewNativeAPITransport(&http.Client{}, 5*time.Second)
	route := newNativeAPITestRoute("http://127.0.0.1:0")
	// A network failure path.
	_, err := transport.Execute(context.Background(), route, NormalizedRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil && strings.Contains(err.Error(), route.Credential.Value) {
		t.Fatalf("Execute error leaks the credential: %v", err)
	}
	ve := transport.NormalizeError(err, route)
	if strings.Contains(ve.Message, route.Credential.Value) {
		t.Fatalf("NormalizeError.Message leaks the credential")
	}
}

// TestNativeAPI_DispatchReachability is mutation row 5: a route whose provider
// declares native_api reaches NativeAPITransport through the Dispatcher. With
// the impls entry removed the dispatcher fails closed (ErrUnresolvableRoute).
func TestNativeAPI_DispatchReachability(t *testing.T) {
	var cap nativeAPICapture
	srv := nativeAPICaptureServer(t, &cap)
	transport := NewNativeAPITransport(&http.Client{}, 5*time.Second)

	dispatcher := NewDispatcher(
		fixedResolver{transportType: TransportTypeNativeAPI},
		map[TransportType]InferenceTransport{TransportTypeNativeAPI: transport},
	)
	if _, err := dispatcher.Execute(context.Background(), newNativeAPITestRoute(srv.URL), NormalizedRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	}); err != nil {
		t.Fatalf("dispatcher.Execute() error = %v, want the native_api transport to be reached", err)
	}
	if !strings.HasSuffix(cap.path, ":generateContent") {
		t.Fatalf("dispatch did not reach NativeAPITransport (path %q)", cap.path)
	}
}

// TestNativeAPI_SupportedCapabilities_IncludesChat proves chat is reported so
// a chat offering can certify for this transport.
func TestNativeAPI_SupportedCapabilities_IncludesChat(t *testing.T) {
	transport := NewNativeAPITransport(&http.Client{}, 5*time.Second)
	for _, c := range transport.SupportedCapabilities(newNativeAPITestRoute("http://127.0.0.1")) {
		if c == OperationChat {
			return
		}
	}
	t.Fatal("SupportedCapabilities must include OperationChat")
}
