package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DefaultNativeOAuthTimeout bounds every request this transport sends
// when the caller does not provide one.
const DefaultNativeOAuthTimeout = 30 * time.Second

// ErrNativeOAuthStreamingUnsupported is returned by Stream and Cancel in
// this unit — streaming is implemented in P4-EXEC-003.
var ErrNativeOAuthStreamingUnsupported = errors.New("execution: native-oauth transport streaming not yet implemented (P4-EXEC-003)")

// nativeOAuthHTTPError carries a non-2xx Gemini response's status/code/
// message. Error() omits message — raw provider text is only accessible
// through Failure's RawMessage (the probe-path exception).
type nativeOAuthHTTPError struct {
	status  int
	code    string
	message string
	scope   string      // from body (Venom extension: "account" | "model" | "offering")
	headers http.Header // response headers for rung-2 classification
}

func (e *nativeOAuthHTTPError) Error() string {
	return fmt.Sprintf("execution: native-oauth transport: http %d", e.status)
}

// Gemini generateContent request/response wire types ----------------------

type geminiPart struct {
	Text         *string       `json:"text,omitempty"`
	FunctionCall *geminiFnCall `json:"functionCall,omitempty"`
}

type geminiFnCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

type geminiGenerationConfig struct {
	MaxOutputTokens *int `json:"maxOutputTokens,omitempty"`
}

type geminiGenerateReq struct {
	Contents         []geminiContent         `json:"contents"`
	GenerationConfig *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}

type geminiGenerateResp struct {
	Candidates []geminiCandidate `json:"candidates"`
}

type geminiErrDetail struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
	Scope   string `json:"scope"` // Venom extension for scope classification
}

type geminiErrBody struct {
	Error geminiErrDetail `json:"error"`
}

// NativeOAuthTransport is the InferenceTransport implementation for
// 01 §4.3's native_oauth type: a Gemini-style generateContent call
// against route.BaseURL + "/models/" + route.ModelID + ":generateContent"
// with Authorization: Bearer <route.Credential.Value>.
// Credential refresh is the credential provider's responsibility (01 §4.5)
// — this transport receives a fresh credential on every call.
type NativeOAuthTransport struct {
	client  *http.Client
	timeout time.Duration
}

// NewNativeOAuthTransport builds a transport over the injected client
// and timeout. timeout <= 0 defaults to DefaultNativeOAuthTimeout.
func NewNativeOAuthTransport(client *http.Client, timeout time.Duration) *NativeOAuthTransport {
	if timeout <= 0 {
		timeout = DefaultNativeOAuthTimeout
	}
	return &NativeOAuthTransport{client: client, timeout: timeout}
}

// geminiRoleFor maps Venom's role vocabulary onto Gemini's:
// "user" stays "user"; anything else (including "assistant") becomes
// "model" (Gemini's role for the AI turn).
func geminiRoleFor(role string) string {
	if role == "user" {
		return "user"
	}
	return "model"
}

// Execute sends one non-streamed generateContent request.
func (t *NativeOAuthTransport) Execute(ctx context.Context, route ResolvedRoute, req NormalizedRequest) (*NormalizedResponse, error) {
	reqCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	contents := make([]geminiContent, 0, len(req.Messages))
	for _, m := range req.Messages {
		text := m.Content
		contents = append(contents, geminiContent{
			Role:  geminiRoleFor(m.Role),
			Parts: []geminiPart{{Text: &text}},
		})
	}

	genReq := geminiGenerateReq{Contents: contents}
	if req.MaxTokens != nil {
		genReq.GenerationConfig = &geminiGenerationConfig{MaxOutputTokens: req.MaxTokens}
	}

	payload, err := json.Marshal(genReq)
	if err != nil {
		return nil, fmt.Errorf("execution: native-oauth transport: encode request: %w", err)
	}

	url := route.BaseURL + "/models/" + route.ModelID + ":generateContent"
	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("execution: native-oauth transport: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+route.Credential.Value)

	resp, err := t.client.Do(httpReq)
	if err != nil {
		if reqCtx.Err() != nil && errors.Is(reqCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("%w: %v", ErrTransportTimeout, err)
		}
		return nil, fmt.Errorf("%w: %v", ErrTransportNetwork, err)
	}
	defer func() { _ = resp.Body.Close() }()

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTransportNetwork, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errBody geminiErrBody
		_ = json.Unmarshal(rawBody, &errBody)
		return nil, &nativeOAuthHTTPError{
			status:  resp.StatusCode,
			code:    errBody.Error.Status,
			message: errBody.Error.Message,
			scope:   errBody.Error.Scope,
			headers: resp.Header.Clone(),
		}
	}

	var okBody geminiGenerateResp
	if err := json.Unmarshal(rawBody, &okBody); err != nil {
		return nil, fmt.Errorf("execution: native-oauth transport: decode response: %w", err)
	}
	if len(okBody.Candidates) == 0 {
		return nil, errors.New("execution: native-oauth transport: no candidates in response")
	}
	candidate := okBody.Candidates[0]

	var textContent string
	var toolCalls []ToolCall
	for _, part := range candidate.Content.Parts {
		if part.Text != nil {
			textContent += *part.Text
		}
		if part.FunctionCall != nil {
			argsJSON := string(part.FunctionCall.Args)
			toolCalls = append(toolCalls, ToolCall{Name: part.FunctionCall.Name, ArgumentsJSON: argsJSON})
		}
	}

	return &NormalizedResponse{
		Message:      Message{Role: candidate.Content.Role, Content: textContent},
		ToolCalls:    toolCalls,
		HTTPStatus:   resp.StatusCode,
		FinishReason: candidate.FinishReason,
	}, nil
}

// Stream is not yet implemented — real SSE streaming arrives in P4-EXEC-003.
func (t *NativeOAuthTransport) Stream(_ context.Context, _ ResolvedRoute, _ NormalizedRequest) (<-chan Chunk, error) {
	return nil, ErrNativeOAuthStreamingUnsupported
}

// Cancel is not yet implemented — in-flight registry arrives in P4-EXEC-003.
func (t *NativeOAuthTransport) Cancel(_ context.Context, _ ResolvedRoute, _ string) error {
	return ErrNativeOAuthStreamingUnsupported
}

// NormalizeError returns a minimal, safe VenomError. The richer
// classification (01 §4.2 full ladder) is in Failure; NormalizeError
// never inspects raw provider text.
func (t *NativeOAuthTransport) NormalizeError(_ error, _ ResolvedRoute) VenomError {
	return VenomError{Code: "internal", Message: "an internal error occurred", Retryable: false}
}

// Failure classifies err into a TypedFailure: timeout/network errors stay
// at their typed sentinels; HTTP rejections carry status/code/RawMessage.
// This is the minimal rung-4 build; the full ladder arrives in P4-EXEC-002.
func (t *NativeOAuthTransport) Failure(err error, _ ResolvedRoute) TypedFailure {
	if errors.Is(err, ErrTransportTimeout) {
		return TypedFailure{FailureClass: FailureClassNetwork, Scope: FailureScopeTransientTransport, Retryable: true, SafeMessage: "the request timed out"}
	}
	if errors.Is(err, ErrTransportNetwork) {
		return TypedFailure{FailureClass: FailureClassNetwork, Scope: FailureScopeTransientTransport, Retryable: true, SafeMessage: "a network error occurred"}
	}
	var httpErr *nativeOAuthHTTPError
	if errors.As(err, &httpErr) {
		return TypedFailure{
			FailureClass: classifyHTTPStatus(httpErr.status),
			HTTPStatus:   httpErr.status,
			ProviderCode: httpErr.code,
			SafeMessage:  "the provider rejected the request",
			RawMessage:   httpErr.message,
		}
	}
	return TypedFailure{FailureClass: FailureClassServer, SafeMessage: "an internal error occurred"}
}

// SupportedCapabilities reports chat only for this unit; vision/tools
// certification is per-offering and not a transport-level claim.
func (t *NativeOAuthTransport) SupportedCapabilities(_ ResolvedRoute) []Operation {
	return []Operation{OperationChat}
}

// Compile-time proof NativeOAuthTransport satisfies InferenceTransport.
var _ InferenceTransport = (*NativeOAuthTransport)(nil)
