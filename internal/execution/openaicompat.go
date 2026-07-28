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

// DefaultOpenAICompatibleTimeout bounds every request this transport
// sends when the caller does not override it — long enough for a real
// provider round trip, short enough that a hung server does not block a
// probe attempt forever.
const DefaultOpenAICompatibleTimeout = 30 * time.Second

// ErrOpenAICompatibleStreamingUnsupported is returned by Stream and
// Cancel: this transport is probe-path only (01 §4.3's openai_compatible
// transport type, minimal build) — the probe path never streams, so
// neither method has a real implementation to give.
var ErrOpenAICompatibleStreamingUnsupported = errors.New("execution: openai-compatible transport does not support streaming")

// ErrTransportTimeout is returned (wrapped) by OpenAICompatibleTransport.Execute
// when the request's own bounded timeout elapses before a response
// arrives — never confused with ErrTransportNetwork, which is any other
// failure to complete the round trip.
var ErrTransportTimeout = errors.New("execution: openai-compatible transport: request timed out")

// ErrTransportNetwork is returned (wrapped) by OpenAICompatibleTransport.Execute
// for any failure to complete the HTTP round trip that is not a timeout
// (connection refused, DNS failure, connection reset, etc.).
var ErrTransportNetwork = errors.New("execution: openai-compatible transport: network error")

// openAICompatHTTPError carries a non-2xx response's status/code/message.
// Its Error() string deliberately omits message — the raw provider text
// is readable ONLY through Failure's RawMessage field (the probe path's
// one sanctioned exception), never through a generic error string that
// might end up logged elsewhere.
type openAICompatHTTPError struct {
	status  int
	code    string
	message string
}

func (e *openAICompatHTTPError) Error() string {
	return fmt.Sprintf("execution: openai-compatible transport: http %d", e.status)
}

// chatReqMessage is one message in the wire request body.
type chatReqMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatCompletionRequestBody is the OpenAI-compatible chat completion
// request this transport sends. MaxTokens is a pointer with omitempty
// specifically so a nil NormalizedRequest.MaxTokens is OMITTED from the
// wire body entirely, never sent as a literal 0 (04 §2's probes rely on
// this: a probe that wants no cap must not accidentally send one).
type chatCompletionRequestBody struct {
	Model     string           `json:"model"`
	Messages  []chatReqMessage `json:"messages"`
	MaxTokens *int             `json:"max_tokens,omitempty"`
}

// chatCompletionToolCall is one tool_calls entry in a chat completion
// response message.
type chatCompletionToolCall struct {
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// chatCompletionResponseBody is the OpenAI-compatible chat completion
// success response this transport parses.
type chatCompletionResponseBody struct {
	Choices []struct {
		Message struct {
			Role      string                   `json:"role"`
			Content   string                   `json:"content"`
			ToolCalls []chatCompletionToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

// chatCompletionErrorBody is the OpenAI-compatible error envelope this
// transport parses out of a non-2xx response, best-effort: a body that
// does not match this shape simply yields an empty code/message rather
// than an additional decode error.
type chatCompletionErrorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// OpenAICompatibleTransport is the InferenceTransport implementation for
// 01 §4.3's openai_compatible transport type: a direct net/http POST to
// route.BaseURL + "/chat/completions" (route.BaseURL already carries
// whatever version path segment the provider needs — e.g. opencode-zen's
// wired BaseURL includes "/v1" — this transport never appends one
// itself). It is a minimal, probe-path-only build: Stream/Cancel are not
// implemented (the probe path never streams).
type OpenAICompatibleTransport struct {
	client  *http.Client
	timeout time.Duration
}

// NewOpenAICompatibleTransport builds a transport over the given client
// (never http.DefaultClient — the caller always injects one) and a
// per-request timeout; timeout <= 0 defaults to
// DefaultOpenAICompatibleTimeout.
func NewOpenAICompatibleTransport(client *http.Client, timeout time.Duration) *OpenAICompatibleTransport {
	if timeout <= 0 {
		timeout = DefaultOpenAICompatibleTimeout
	}
	return &OpenAICompatibleTransport{client: client, timeout: timeout}
}

func toChatReqMessages(msgs []Message) []chatReqMessage {
	out := make([]chatReqMessage, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, chatReqMessage(m))
	}
	return out
}

// Execute sends one non-streamed chat completion request.
func (t *OpenAICompatibleTransport) Execute(ctx context.Context, route ResolvedRoute, req NormalizedRequest) (*NormalizedResponse, error) {
	reqCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	payload, err := json.Marshal(chatCompletionRequestBody{
		Model:     route.ModelID,
		Messages:  toChatReqMessages(req.Messages),
		MaxTokens: req.MaxTokens,
	})
	if err != nil {
		return nil, fmt.Errorf("execution: openai-compatible transport: encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, route.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("execution: openai-compatible transport: build request: %w", err)
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
		var errBody chatCompletionErrorBody
		_ = json.Unmarshal(rawBody, &errBody) // best-effort; zero value on a non-matching body
		return nil, &openAICompatHTTPError{status: resp.StatusCode, code: errBody.Error.Code, message: errBody.Error.Message}
	}

	var okBody chatCompletionResponseBody
	if err := json.Unmarshal(rawBody, &okBody); err != nil {
		return nil, fmt.Errorf("execution: openai-compatible transport: decode response: %w", err)
	}
	if len(okBody.Choices) == 0 {
		return nil, errors.New("execution: openai-compatible transport: no choices in response")
	}
	choice := okBody.Choices[0]

	toolCalls := make([]ToolCall, 0, len(choice.Message.ToolCalls))
	for _, tc := range choice.Message.ToolCalls {
		toolCalls = append(toolCalls, ToolCall{Name: tc.Function.Name, ArgumentsJSON: tc.Function.Arguments})
	}

	return &NormalizedResponse{
		Message:      Message{Role: choice.Message.Role, Content: choice.Message.Content},
		ToolCalls:    toolCalls,
		HTTPStatus:   resp.StatusCode,
		FinishReason: choice.FinishReason,
	}, nil
}

// Stream is not implemented — the probe path never streams (see the
// type's own doc comment).
func (t *OpenAICompatibleTransport) Stream(_ context.Context, _ ResolvedRoute, _ NormalizedRequest) (<-chan Chunk, error) {
	return nil, ErrOpenAICompatibleStreamingUnsupported
}

// Cancel is not implemented — see Stream's doc comment.
func (t *OpenAICompatibleTransport) Cancel(_ context.Context, _ ResolvedRoute, _ string) error {
	return ErrOpenAICompatibleStreamingUnsupported
}

// NormalizeError returns a minimal, generically-safe VenomError,
// mirroring BifrostTransport's own — the richer, typed classification
// lives in Failure below; this signature and contract are unchanged so no
// existing caller on the ordinary routing path is affected.
func (t *OpenAICompatibleTransport) NormalizeError(_ error, _ ResolvedRoute) VenomError {
	return VenomError{Code: "internal", Message: "an internal error occurred", Retryable: false}
}

// Failure classifies err into the richer TypedFailure envelope: a timeout
// or network failure (HTTPStatus stays 0 — never a fabricated status),
// or an HTTP-level rejection carrying the real status/code/RawMessage.
func (t *OpenAICompatibleTransport) Failure(err error, _ ResolvedRoute) TypedFailure {
	switch {
	case errors.Is(err, ErrTransportTimeout):
		return TypedFailure{FailureClass: FailureClassNetwork, Scope: FailureScopeTransientTransport, Retryable: true, SafeMessage: "the request timed out"}
	case errors.Is(err, ErrTransportNetwork):
		return TypedFailure{FailureClass: FailureClassNetwork, Scope: FailureScopeTransientTransport, Retryable: true, SafeMessage: "a network error occurred"}
	}
	var httpErr *openAICompatHTTPError
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

// SupportedCapabilities reports plain chat only, mirroring
// BifrostTransport's own honest minimal claim.
func (t *OpenAICompatibleTransport) SupportedCapabilities(_ ResolvedRoute) []Operation {
	return []Operation{OperationChat}
}
