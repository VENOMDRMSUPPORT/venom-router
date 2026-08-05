package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	DefaultOpenAICompatibleTimeout          = 30 * time.Second
	DefaultOpenAICompatibleFirstByteTimeout = 10 * time.Second
	DefaultOpenAICompatibleIdleGapTimeout   = 30 * time.Second
)

// ErrTransportTimeout is returned (wrapped) by OpenAICompatibleTransport.Execute
// when the request's own bounded timeout elapses before a response arrives.
var ErrTransportTimeout = errors.New("execution: openai-compatible transport: request timed out")

// ErrTransportNetwork is returned (wrapped) for any failure to complete
// the HTTP round trip that is not a timeout (connection refused, DNS
// failure, connection reset, etc.).
var ErrTransportNetwork = errors.New("execution: openai-compatible transport: network error")

// openAICompatHTTPError carries a non-2xx response's status/code/message.
// Error() omits message — raw provider text is readable ONLY through
// Failure's RawMessage (probe path), never through a generic error string.
type openAICompatHTTPError struct {
	status  int
	code    string
	message string
	scope   string      // explicit scope hint from the response body
	headers http.Header // response headers for rung-2 classification
}

func (e *openAICompatHTTPError) Error() string {
	return fmt.Sprintf("execution: openai-compatible transport: http %d", e.status)
}

// chatReqMessage's Content is `any` so it can be EITHER the plain string form
// (when a message has no Parts — byte-identical to the pre-P5-EXEC-004 wire
// body) OR the OpenAI array form (when Parts is non-empty). A JSON string and
// an `any` holding that same string marshal identically, so text-only requests
// are unchanged.
type chatReqMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// chatReqContentPart is one element of the OpenAI array content form.
type chatReqContentPart struct {
	Type     string           `json:"type"`
	Text     string           `json:"text,omitempty"`
	ImageURL *chatReqImageURL `json:"image_url,omitempty"`
}

type chatReqImageURL struct {
	URL string `json:"url"`
}

// chatReqTool is one element of the OpenAI tools array. Parameters is embedded
// as raw JSON (never re-encoded as a string), so the client's schema reaches
// the provider verbatim.
type chatReqTool struct {
	Type     string          `json:"type"`
	Function chatReqToolFunc `json:"function"`
}

type chatReqToolFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// chatCompletionRequestBody is the OpenAI-compatible chat completion
// request this transport sends. Stream, MaxTokens, Tools, ToolChoice, and
// ResponseFormat use omitempty so they are absent from the wire body when
// zero/nil/empty — a text-only request therefore serializes exactly as it
// did before P5-EXEC-004.
type chatCompletionRequestBody struct {
	Model          string              `json:"model"`
	Messages       []chatReqMessage    `json:"messages"`
	MaxTokens      *int                `json:"max_tokens,omitempty"`
	Stream         bool                `json:"stream,omitempty"`
	Tools          []chatReqTool       `json:"tools,omitempty"`
	ToolChoice     string              `json:"tool_choice,omitempty"`
	ResponseFormat *chatResponseFormat `json:"response_format,omitempty"`
}

// chatResponseFormat is OpenAI's response_format object. Only the type
// discriminator is expressed; a JSON Schema variant is not part of this seam.
type chatResponseFormat struct {
	Type string `json:"type"`
}

// buildChatResponseFormat maps the normalized value onto the wire object.
// Empty yields nil so the key is omitted entirely.
func buildChatResponseFormat(v string) (*chatResponseFormat, error) {
	if v == "" {
		return nil, nil
	}
	if v != "json_object" {
		return nil, fmt.Errorf("%w: response_format %q", ErrRequestFeatureUnsupported, v)
	}
	return &chatResponseFormat{Type: v}, nil
}

type chatCompletionToolCall struct {
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

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

type chatCompletionErrorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Scope   string `json:"scope"` // Venom extension: "account","model","offering"
	} `json:"error"`
}

// chatCompletionStreamChunk is one delta event in an OpenAI-compatible
// SSE stream.
type chatCompletionStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

// OpenAICompatibleTransport is the InferenceTransport for the
// openai_compatible transport type (01 §4.3): a direct net/http POST to
// route.BaseURL + "/chat/completions".
type OpenAICompatibleTransport struct {
	client           *http.Client
	timeout          time.Duration
	firstByteTimeout time.Duration
	idleGapTimeout   time.Duration
	inflights        *inflightRegistry
}

// NewOpenAICompatibleTransport builds a transport with default streaming
// timeouts. timeout <= 0 defaults to DefaultOpenAICompatibleTimeout.
func NewOpenAICompatibleTransport(client *http.Client, timeout time.Duration) *OpenAICompatibleTransport {
	return newOpenAICompatibleTransport(client, timeout,
		DefaultOpenAICompatibleFirstByteTimeout,
		DefaultOpenAICompatibleIdleGapTimeout)
}

// newOpenAICompatibleTransport is the internal constructor that exposes
// firstByteTimeout and idleGapTimeout for test-controlled short values.
func newOpenAICompatibleTransport(client *http.Client, timeout, firstByteTimeout, idleGapTimeout time.Duration) *OpenAICompatibleTransport {
	if timeout <= 0 {
		timeout = DefaultOpenAICompatibleTimeout
	}
	if firstByteTimeout <= 0 {
		firstByteTimeout = DefaultOpenAICompatibleFirstByteTimeout
	}
	if idleGapTimeout <= 0 {
		idleGapTimeout = DefaultOpenAICompatibleIdleGapTimeout
	}
	return &OpenAICompatibleTransport{
		client:           client,
		timeout:          timeout,
		firstByteTimeout: firstByteTimeout,
		idleGapTimeout:   idleGapTimeout,
		inflights:        newInflightRegistry(),
	}
}

// buildChatMessages maps normalized messages to the wire form, failing CLOSED
// (ErrRequestFeatureUnsupported) on any content part it cannot express — never
// dropping one. A message with no Parts keeps the plain-string content form
// exactly as before P5-EXEC-004.
func buildChatMessages(msgs []Message) ([]chatReqMessage, error) {
	out := make([]chatReqMessage, 0, len(msgs))
	for _, m := range msgs {
		content, err := chatContentFor(m)
		if err != nil {
			return nil, err
		}
		out = append(out, chatReqMessage{Role: m.Role, Content: content})
	}
	return out, nil
}

// chatContentFor returns m's content as either the plain string (no Parts) or
// the OpenAI array form (Parts present).
func chatContentFor(m Message) (any, error) {
	if len(m.Parts) == 0 {
		return m.Content, nil
	}
	parts := make([]chatReqContentPart, 0, len(m.Parts))
	for _, p := range m.Parts {
		switch p.Kind {
		case ContentPartText:
			parts = append(parts, chatReqContentPart{Type: "text", Text: p.Text})
		case ContentPartImage:
			url, err := imageURLFor(p)
			if err != nil {
				return nil, err
			}
			parts = append(parts, chatReqContentPart{Type: "image_url", ImageURL: &chatReqImageURL{URL: url}})
		default:
			// Fail closed on an unknown kind — never silently skip it. The
			// message names the KIND only, never any content.
			return nil, fmt.Errorf("%w: content part kind %q", ErrRequestFeatureUnsupported, p.Kind)
		}
	}
	return parts, nil
}

// imageURLFor derives the OpenAI image_url URL: a direct URL when set, else a
// data: URL from base64 + media type. It fails closed when neither is
// expressible — the error names the missing field, never the URL itself.
func imageURLFor(p ContentPart) (string, error) {
	switch {
	case p.ImageURL != "":
		return p.ImageURL, nil
	case p.ImageBase64 != "" && p.MediaType != "":
		return "data:" + p.MediaType + ";base64," + p.ImageBase64, nil
	default:
		return "", fmt.Errorf("%w: image part missing url or (base64 + media type)", ErrRequestFeatureUnsupported)
	}
}

// buildChatTools maps tool definitions to the OpenAI tools array, embedding
// each parameters schema as raw JSON. Returns nil for no tools so the wire body
// is unchanged.
func buildChatTools(tools []ToolDefinition) []chatReqTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]chatReqTool, 0, len(tools))
	for _, td := range tools {
		fn := chatReqToolFunc{Name: td.Name, Description: td.Description}
		if td.ParametersJSON != "" {
			fn.Parameters = json.RawMessage(td.ParametersJSON)
		}
		out = append(out, chatReqTool{Type: "function", Function: fn})
	}
	return out
}

// Execute sends one non-streamed chat completion request.
func (t *OpenAICompatibleTransport) Execute(ctx context.Context, route ResolvedRoute, req NormalizedRequest) (*NormalizedResponse, error) {
	reqCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	messages, err := buildChatMessages(req.Messages)
	if err != nil {
		return nil, err
	}
	responseFormat, err := buildChatResponseFormat(req.ResponseFormat)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(chatCompletionRequestBody{
		Model:          route.ModelID,
		Messages:       messages,
		MaxTokens:      req.MaxTokens,
		Tools:          buildChatTools(req.Tools),
		ToolChoice:     req.ToolChoice,
		ResponseFormat: responseFormat,
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
		_ = json.Unmarshal(rawBody, &errBody)
		return nil, &openAICompatHTTPError{
			status:  resp.StatusCode,
			code:    errBody.Error.Code,
			message: errBody.Error.Message,
			scope:   errBody.Error.Scope,
			headers: resp.Header.Clone(),
		}
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

// Stream sends a streaming request and returns a channel of Chunk values.
// Pre-first-byte errors (non-2xx response, connect failure) are returned
// directly from Stream. Post-first-byte errors arrive as Chunk{Err: ...}.
func (t *OpenAICompatibleTransport) Stream(ctx context.Context, route ResolvedRoute, req NormalizedRequest) (<-chan Chunk, error) {
	streamCtx, cancel := context.WithCancel(ctx)

	messages, err := buildChatMessages(req.Messages)
	if err != nil {
		cancel()
		return nil, err
	}
	responseFormat, err := buildChatResponseFormat(req.ResponseFormat)
	if err != nil {
		cancel()
		return nil, err
	}
	payload, err := json.Marshal(chatCompletionRequestBody{
		Model:          route.ModelID,
		Messages:       messages,
		MaxTokens:      req.MaxTokens,
		Stream:         true,
		Tools:          buildChatTools(req.Tools),
		ToolChoice:     req.ToolChoice,
		ResponseFormat: responseFormat,
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("execution: openai-compatible transport: encode stream request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(streamCtx, http.MethodPost, route.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("execution: openai-compatible transport: build stream request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+route.Credential.Value)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := t.client.Do(httpReq)
	if err != nil {
		cancel()
		if errors.Is(streamCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("%w: %v", ErrTransportTimeout, err)
		}
		return nil, fmt.Errorf("%w: %v", ErrTransportNetwork, err)
	}

	// Pre-first-byte boundary: non-2xx before any SSE data.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		rawBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		cancel()
		var errBody chatCompletionErrorBody
		_ = json.Unmarshal(rawBody, &errBody)
		return nil, &openAICompatHTTPError{
			status:  resp.StatusCode,
			code:    errBody.Error.Code,
			message: errBody.Error.Message,
			scope:   errBody.Error.Scope,
			headers: resp.Header.Clone(),
		}
	}

	// 2xx: past the pre-first-byte boundary. Register for Cancel.
	t.inflights.register(req.RequestID, cancel)

	ch := make(chan Chunk, 8)
	go t.runOpenAISSE(streamCtx, cancel, resp, req.RequestID, ch)
	return ch, nil
}

// runOpenAISSE is the goroutine body for Stream. Extracting it into a
// named method keeps (*OpenAICompatibleTransport).Stream's cyclomatic
// complexity within the project's gocyclo limit.
func (t *OpenAICompatibleTransport) runOpenAISSE(
	streamCtx context.Context,
	cancel context.CancelFunc,
	resp *http.Response,
	requestID string,
	ch chan<- Chunk,
) {
	defer func() {
		t.inflights.unregister(requestID)
		_ = resp.Body.Close()
		cancel()
		close(ch)
	}()

	lineCh := sseScanner(streamCtx, resp.Body)

	firstByteTimer := time.NewTimer(t.firstByteTimeout)
	defer firstByteTimer.Stop()
	idleTimer := time.NewTimer(t.idleGapTimeout)
	defer idleTimer.Stop()
	firstByteSeen := false

	for {
		select {
		case <-streamCtx.Done():
			return

		case <-firstByteTimer.C:
			if !firstByteSeen {
				select {
				case ch <- Chunk{Err: ErrStreamFirstByteTimeout}:
				case <-streamCtx.Done():
				}
				return
			}

		case <-idleTimer.C:
			select {
			case ch <- Chunk{Err: ErrStreamIdleGapTimeout}:
			case <-streamCtx.Done():
			}
			return

		case ev, ok := <-lineCh:
			if !ok {
				// Clean EOF WITHOUT [DONE]: the OpenAI SSE contract's
				// completion marker never arrived, so this is a truncated
				// response, not a completed one — the consumer must be able
				// to tell the difference (05 §3 partial consumption).
				select {
				case ch <- Chunk{Err: ErrStreamTruncated}:
				case <-streamCtx.Done():
				}
				return
			}
			if ev.err != nil {
				select {
				case ch <- Chunk{Err: fmt.Errorf("%w: %v", ErrTransportNetwork, ev.err)}:
				case <-streamCtx.Done():
				}
				return
			}

			if ev.line != "" {
				if !firstByteSeen {
					firstByteSeen = true
					firstByteTimer.Stop()
				}
				resetTimer(idleTimer, t.idleGapTimeout)
			}

			if !strings.HasPrefix(ev.line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(ev.line, "data: ")
			if data == "[DONE]" {
				select {
				case ch <- Chunk{Done: true}:
				case <-streamCtx.Done():
				}
				return
			}
			var sc chatCompletionStreamChunk
			if jsonErr := json.Unmarshal([]byte(data), &sc); jsonErr != nil {
				select {
				case ch <- Chunk{Err: fmt.Errorf("execution: openai-compatible transport: decode stream chunk: %w", jsonErr)}:
				case <-streamCtx.Done():
				}
				return
			}
			if len(sc.Choices) == 0 {
				continue
			}
			delta := sc.Choices[0].Delta.Content
			if delta != "" {
				select {
				case ch <- Chunk{Delta: delta}:
				case <-streamCtx.Done():
					return
				}
			}
		}
	}
}

// Cancel aborts an in-flight stream identified by requestID. Returns
// ErrRequestNotInflight when the ID is unknown or the stream already
// finished — a typed no-op, never a panic.
func (t *OpenAICompatibleTransport) Cancel(_ context.Context, _ ResolvedRoute, requestID string) error {
	return t.inflights.cancel(requestID)
}

// NormalizeError derives the stable VenomError from the SAME
// classification Failure performs, so the two shapes can never disagree
// (P4-EXEC-002): code is the FailureClass, message the Venom-authored
// SafeMessage, retryable the taxonomy's verdict. Raw provider text never
// appears by construction — RawMessage is not consulted here.
func (t *OpenAICompatibleTransport) NormalizeError(err error, route ResolvedRoute) VenomError {
	f := t.Failure(err, route)
	return VenomError{Code: string(f.FailureClass), Message: f.SafeMessage, Retryable: f.Retryable}
}

// Failure classifies err using the 4-rung ladder. Timeout and network
// sentinels bypass the ladder (HTTPStatus stays 0). HTTP rejections carry
// RawMessage for the probe path; it is never placed in SafeMessage.
func (t *OpenAICompatibleTransport) Failure(err error, _ ResolvedRoute) TypedFailure {
	if errors.Is(err, ErrTransportTimeout) {
		return TypedFailure{FailureClass: FailureClassNetwork, Scope: FailureScopeTransientTransport, Retryable: true, SafeMessage: safeMessageFor(FailureClassNetwork)}
	}
	if errors.Is(err, ErrTransportNetwork) {
		return TypedFailure{FailureClass: FailureClassNetwork, Scope: FailureScopeTransientTransport, Retryable: true, SafeMessage: safeMessageFor(FailureClassNetwork)}
	}
	var httpErr *openAICompatHTTPError
	if errors.As(err, &httpErr) {
		f := ClassifyFailure(httpErr.code, httpErr.scope, httpErr.headers, nil, httpErr.status)
		f.RawMessage = httpErr.message
		return f
	}
	return TypedFailure{FailureClass: FailureClassServer, SafeMessage: "an internal error occurred"}
}

// SupportedCapabilities reports chat and streaming.
func (t *OpenAICompatibleTransport) SupportedCapabilities(_ ResolvedRoute) []Operation {
	return []Operation{OperationChat, OperationStreaming}
}
