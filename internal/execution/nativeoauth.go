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
	DefaultNativeOAuthTimeout          = 30 * time.Second
	DefaultNativeOAuthFirstByteTimeout = 10 * time.Second
	DefaultNativeOAuthIdleGapTimeout   = 30 * time.Second
)

// ErrUnsupportedWireSchema is returned by the native_oauth transport BEFORE any
// network call when a route carries an empty or unrecognized WireSchema. Fail
// closed: a native_oauth route with no schema is never defaulted to a
// particular provider's protocol (that would send one provider's request in
// another's wire format).
var ErrUnsupportedWireSchema = errors.New("execution: native-oauth transport: unsupported or missing wire schema")

// nativeOAuthHTTPError carries a non-2xx response's status/code/message across
// ALL wire schemas the transport serves. Error() omits message — raw provider
// text is only accessible through Failure's RawMessage (probe path).
type nativeOAuthHTTPError struct {
	status  int
	code    string
	message string
	scope   string
	headers http.Header
}

func (e *nativeOAuthHTTPError) Error() string {
	return fmt.Sprintf("execution: native-oauth transport: http %d", e.status)
}

// oauthWireCodec is the per-schema wire mapping the native_oauth transport
// selects by route.WireSchema (P7-EXEC-001 part 2). One OAuth-bearer transport,
// several protocols — chosen by a typed catalog value, never a slug switch. The
// request builders / decoders / SSE runners live in geminiwire.go,
// anthropicwire.go and openaicompat.go; a codec is a thin binder over them plus
// the schema's own endpoint, required headers, and credential formatting.
type oauthWireCodec interface {
	buildPayload(route ResolvedRoute, req NormalizedRequest, stream bool) ([]byte, error)
	executeURL(route ResolvedRoute) string
	streamURL(route ResolvedRoute) string
	applyHeaders(h http.Header, credential string)
	decodeSuccess(rawBody []byte, status int) (*NormalizedResponse, error)
	newHTTPError(status int, rawBody []byte, headers http.Header) *nativeOAuthHTTPError
	runSSE(streamCtx context.Context, cancel context.CancelFunc, resp *http.Response, requestID string, ch chan<- Chunk, inflights *inflightRegistry, firstByteTimeout, idleGapTimeout time.Duration)
	capabilities() []Operation
}

// NativeOAuthTransport is the InferenceTransport for the native_oauth type
// (01 §4.3): an OAuth-bearer transport that serves several wire schemas
// (Gemini generateContent, Anthropic Messages, OpenAI chat) selected by
// route.WireSchema. OAuth tokens are refreshed by the credential provider, not
// here (01 §4.5).
type NativeOAuthTransport struct {
	client           *http.Client
	timeout          time.Duration
	firstByteTimeout time.Duration
	idleGapTimeout   time.Duration
	inflights        *inflightRegistry
	codecs           map[WireSchema]oauthWireCodec
}

// NewNativeOAuthTransport builds a transport with default streaming timeouts.
func NewNativeOAuthTransport(client *http.Client, timeout time.Duration) *NativeOAuthTransport {
	return newNativeOAuthTransport(client, timeout,
		DefaultNativeOAuthFirstByteTimeout,
		DefaultNativeOAuthIdleGapTimeout)
}

// newNativeOAuthTransport is the internal constructor for test-controlled
// firstByteTimeout and idleGapTimeout values.
func newNativeOAuthTransport(client *http.Client, timeout, firstByteTimeout, idleGapTimeout time.Duration) *NativeOAuthTransport {
	if timeout <= 0 {
		timeout = DefaultNativeOAuthTimeout
	}
	if firstByteTimeout <= 0 {
		firstByteTimeout = DefaultNativeOAuthFirstByteTimeout
	}
	if idleGapTimeout <= 0 {
		idleGapTimeout = DefaultNativeOAuthIdleGapTimeout
	}
	return &NativeOAuthTransport{
		client:           client,
		timeout:          timeout,
		firstByteTimeout: firstByteTimeout,
		idleGapTimeout:   idleGapTimeout,
		inflights:        newInflightRegistry(),
		codecs: map[WireSchema]oauthWireCodec{
			WireSchemaGoogleGenerateContent: googleGenerateContentCodec{},
			WireSchemaAnthropicMessages:     anthropicMessagesCodec{},
			WireSchemaOpenAIChat:            openAIChatCodec{},
		},
	}
}

// codecFor returns the codec for schema, or ErrUnsupportedWireSchema (fail
// closed) for an empty or unrecognized schema — never a default.
func (t *NativeOAuthTransport) codecFor(schema WireSchema) (oauthWireCodec, error) {
	c, ok := t.codecs[schema]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedWireSchema, schema)
	}
	return c, nil
}

// Execute sends one non-streamed request in the route's declared wire schema.
func (t *NativeOAuthTransport) Execute(ctx context.Context, route ResolvedRoute, req NormalizedRequest) (*NormalizedResponse, error) {
	codec, err := t.codecFor(route.WireSchema)
	if err != nil {
		return nil, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	payload, err := codec.buildPayload(route, req, false)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, codec.executeURL(route), bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("execution: native-oauth transport: build request: %w", err)
	}
	codec.applyHeaders(httpReq.Header, route.Credential.Value)

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
		return nil, codec.newHTTPError(resp.StatusCode, rawBody, resp.Header)
	}
	return codec.decodeSuccess(rawBody, resp.StatusCode)
}

// Stream sends a streaming request in the route's declared wire schema.
// Pre-first-byte errors are returned from Stream; post-first-byte failures
// arrive as Chunk{Err: ...}.
func (t *NativeOAuthTransport) Stream(ctx context.Context, route ResolvedRoute, req NormalizedRequest) (<-chan Chunk, error) {
	codec, err := t.codecFor(route.WireSchema)
	if err != nil {
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(ctx)

	payload, err := codec.buildPayload(route, req, true)
	if err != nil {
		cancel()
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(streamCtx, http.MethodPost, codec.streamURL(route), bytes.NewReader(payload))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("execution: native-oauth transport: build stream request: %w", err)
	}
	codec.applyHeaders(httpReq.Header, route.Credential.Value)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := t.client.Do(httpReq)
	if err != nil {
		cancel()
		if errors.Is(streamCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("%w: %v", ErrTransportTimeout, err)
		}
		return nil, fmt.Errorf("%w: %v", ErrTransportNetwork, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		rawBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		cancel()
		return nil, codec.newHTTPError(resp.StatusCode, rawBody, resp.Header)
	}

	t.inflights.register(req.RequestID, cancel)
	ch := make(chan Chunk, 8)
	go codec.runSSE(streamCtx, cancel, resp, req.RequestID, ch, t.inflights, t.firstByteTimeout, t.idleGapTimeout)
	return ch, nil
}

// Cancel aborts an in-flight stream. Returns ErrRequestNotInflight when the ID
// is unknown or the stream already finished.
func (t *NativeOAuthTransport) Cancel(_ context.Context, _ ResolvedRoute, requestID string) error {
	return t.inflights.cancel(requestID)
}

// NormalizeError derives the stable VenomError from the SAME classification
// Failure performs (P4-EXEC-002).
func (t *NativeOAuthTransport) NormalizeError(err error, route ResolvedRoute) VenomError {
	f := t.Failure(err, route)
	return VenomError{Code: string(f.FailureClass), Message: f.SafeMessage, Retryable: f.Retryable}
}

// Failure classifies err using the 4-rung ladder, over the SHARED
// nativeOAuthHTTPError every codec produces. RawMessage is set for the probe
// path; it is never placed in SafeMessage.
func (t *NativeOAuthTransport) Failure(err error, _ ResolvedRoute) TypedFailure {
	if errors.Is(err, ErrTransportTimeout) {
		return TypedFailure{FailureClass: FailureClassNetwork, Scope: FailureScopeTransientTransport, Retryable: true, SafeMessage: safeMessageFor(FailureClassNetwork)}
	}
	if errors.Is(err, ErrTransportNetwork) {
		return TypedFailure{FailureClass: FailureClassNetwork, Scope: FailureScopeTransientTransport, Retryable: true, SafeMessage: safeMessageFor(FailureClassNetwork)}
	}
	var httpErr *nativeOAuthHTTPError
	if errors.As(err, &httpErr) {
		f := ClassifyFailure(httpErr.code, httpErr.scope, httpErr.headers, nil, httpErr.status)
		f.RawMessage = httpErr.message
		return f
	}
	return TypedFailure{FailureClass: FailureClassServer, SafeMessage: "an internal error occurred"}
}

// SupportedCapabilities reports what the route's schema can express; an
// unsupported schema reports nothing (fail closed).
func (t *NativeOAuthTransport) SupportedCapabilities(route ResolvedRoute) []Operation {
	codec, err := t.codecFor(route.WireSchema)
	if err != nil {
		return nil
	}
	return codec.capabilities()
}

// Compile-time proof NativeOAuthTransport satisfies InferenceTransport.
var _ InferenceTransport = (*NativeOAuthTransport)(nil)

// --- google_generate_content codec (antigravity) --------------------------

type googleGenerateContentCodec struct{}

func (googleGenerateContentCodec) buildPayload(_ ResolvedRoute, req NormalizedRequest, _ bool) ([]byte, error) {
	genReq, err := buildGeminiRequest(req)
	if err != nil {
		return nil, err
	}
	return json.Marshal(genReq)
}

func (googleGenerateContentCodec) executeURL(route ResolvedRoute) string {
	return route.BaseURL + "/models/" + route.ModelID + ":generateContent"
}
func (googleGenerateContentCodec) streamURL(route ResolvedRoute) string {
	return route.BaseURL + "/models/" + route.ModelID + ":streamGenerateContent?alt=sse"
}
func (googleGenerateContentCodec) applyHeaders(h http.Header, credential string) {
	h.Set("Content-Type", "application/json")
	h.Set("Authorization", "Bearer "+credential)
}
func (googleGenerateContentCodec) decodeSuccess(rawBody []byte, status int) (*NormalizedResponse, error) {
	return decodeGeminiSuccess(rawBody, status)
}
func (googleGenerateContentCodec) newHTTPError(status int, rawBody []byte, headers http.Header) *nativeOAuthHTTPError {
	var e geminiErrBody
	_ = json.Unmarshal(rawBody, &e)
	return &nativeOAuthHTTPError{status: status, code: e.Error.Status, message: e.Error.Message, scope: e.Error.Scope, headers: headers.Clone()}
}
func (googleGenerateContentCodec) runSSE(streamCtx context.Context, cancel context.CancelFunc, resp *http.Response, requestID string, ch chan<- Chunk, inflights *inflightRegistry, firstByteTimeout, idleGapTimeout time.Duration) {
	runGeminiSSE(streamCtx, cancel, resp, requestID, ch, inflights, firstByteTimeout, idleGapTimeout)
}
func (googleGenerateContentCodec) capabilities() []Operation {
	return []Operation{OperationChat, OperationStreaming}
}

// --- anthropic_messages codec (claude-code) -------------------------------

type anthropicMessagesCodec struct{}

func (anthropicMessagesCodec) buildPayload(route ResolvedRoute, req NormalizedRequest, stream bool) ([]byte, error) {
	msgReq, err := buildAnthropicRequest(req, route.ModelID, stream)
	if err != nil {
		return nil, err
	}
	return json.Marshal(msgReq)
}

func (anthropicMessagesCodec) executeURL(route ResolvedRoute) string {
	return route.BaseURL + "/v1/messages"
}
func (anthropicMessagesCodec) streamURL(route ResolvedRoute) string {
	return route.BaseURL + "/v1/messages"
}

// applyHeaders sets the headers claude-code requires on EVERY call (03 §3):
// missing any of them is a 429, so this is correctness, not decoration.
func (anthropicMessagesCodec) applyHeaders(h http.Header, credential string) {
	h.Set("Content-Type", "application/json")
	h.Set("Authorization", "Bearer "+credential)
	h.Set("anthropic-version", anthropicVersionHeader)
	h.Set("anthropic-beta", anthropicBetaHeader)
	h.Set("X-App", anthropicAppHeader)
	h.Set("User-Agent", anthropicUserAgentHeader)
}
func (anthropicMessagesCodec) decodeSuccess(rawBody []byte, status int) (*NormalizedResponse, error) {
	return decodeAnthropicSuccess(rawBody, status)
}
func (anthropicMessagesCodec) newHTTPError(status int, rawBody []byte, headers http.Header) *nativeOAuthHTTPError {
	return newAnthropicHTTPError(status, rawBody, headers)
}
func (anthropicMessagesCodec) runSSE(streamCtx context.Context, cancel context.CancelFunc, resp *http.Response, requestID string, ch chan<- Chunk, inflights *inflightRegistry, firstByteTimeout, idleGapTimeout time.Duration) {
	runAnthropicSSE(streamCtx, cancel, resp, requestID, ch, inflights, firstByteTimeout, idleGapTimeout)
}
func (anthropicMessagesCodec) capabilities() []Operation {
	return []Operation{OperationChat, OperationStreaming}
}

// --- openai_chat codec (clinepass) ----------------------------------------
//
// This codec reuses openaicompat.go's request/response/stream types and the
// buildChatMessages / buildChatTools mappers, so there is exactly ONE OpenAI
// body mapping in the package. Its header set and credential formatting reflect
// its sole current consumer, clinepass: the `workos:` Bearer prefix is applied
// HERE (a wire concern — the stored token stays raw), and the three cline
// headers are required on every call. When a second openai_chat provider with
// different wire decoration appears, this must become per-provider rather than
// per-schema (see the batch report's forward notes).

const (
	clinepassReferer     = "https://cline.bot"
	clinepassTitle       = "Cline"
	clinepassClientType  = "venom-router"
	clinepassTokenPrefix = "workos:"
)

type openAIChatCodec struct{}

func (openAIChatCodec) buildPayload(route ResolvedRoute, req NormalizedRequest, stream bool) ([]byte, error) {
	messages, err := buildChatMessages(req.Messages)
	if err != nil {
		return nil, err
	}
	return json.Marshal(chatCompletionRequestBody{
		Model:      route.ModelID,
		Messages:   messages,
		MaxTokens:  req.MaxTokens,
		Stream:     stream,
		Tools:      buildChatTools(req.Tools),
		ToolChoice: req.ToolChoice,
	})
}

func (openAIChatCodec) executeURL(route ResolvedRoute) string {
	return route.BaseURL + "/chat/completions"
}
func (openAIChatCodec) streamURL(route ResolvedRoute) string {
	return route.BaseURL + "/chat/completions"
}

func (openAIChatCodec) applyHeaders(h http.Header, credential string) {
	h.Set("Content-Type", "application/json")
	h.Set("Authorization", "Bearer "+workosPrefixed(credential))
	h.Set("HTTP-Referer", clinepassReferer)
	h.Set("X-Title", clinepassTitle)
	h.Set("X-CLIENT-TYPE", clinepassClientType)
}

// workosPrefixed applies clinepass's `workos:` token prefix idempotently — a
// wire concern, so the stored credential stays the raw provider token. Applying
// it twice (double-prefix) is a defect the idempotent-prefix test pins.
func workosPrefixed(credential string) string {
	if strings.HasPrefix(credential, clinepassTokenPrefix) {
		return credential
	}
	return clinepassTokenPrefix + credential
}

func (openAIChatCodec) decodeSuccess(rawBody []byte, status int) (*NormalizedResponse, error) {
	var body chatCompletionResponseBody
	if err := json.Unmarshal(rawBody, &body); err != nil {
		return nil, fmt.Errorf("execution: openai-chat transport: decode response: %w", err)
	}
	if len(body.Choices) == 0 {
		// clinepass wraps the NON-STREAM completion in its standard
		// {success, data} envelope (data.choices[0].message — legacy wire
		// reference, docs/evidence/clinepass-legacy-wire-reference.md §7);
		// only the SSE stream is bare. Unwrap before failing.
		var enveloped struct {
			Success bool                        `json:"success"`
			Data    *chatCompletionResponseBody `json:"data"`
		}
		if err := json.Unmarshal(rawBody, &enveloped); err == nil && enveloped.Data != nil {
			body = *enveloped.Data
		}
	}
	if len(body.Choices) == 0 {
		return nil, errors.New("execution: openai-chat transport: no choices in response")
	}
	choice := body.Choices[0]
	toolCalls := make([]ToolCall, 0, len(choice.Message.ToolCalls))
	for _, tc := range choice.Message.ToolCalls {
		toolCalls = append(toolCalls, ToolCall{Name: tc.Function.Name, ArgumentsJSON: tc.Function.Arguments})
	}
	return &NormalizedResponse{
		Message:      Message{Role: choice.Message.Role, Content: choice.Message.Content},
		ToolCalls:    toolCalls,
		HTTPStatus:   status,
		FinishReason: choice.FinishReason,
	}, nil
}

func (openAIChatCodec) newHTTPError(status int, rawBody []byte, headers http.Header) *nativeOAuthHTTPError {
	var e chatCompletionErrorBody
	_ = json.Unmarshal(rawBody, &e)
	if e.Error.Message == "" {
		// clinepass envelope errors carry a plain string: {success:false,
		// error:"..."} (legacy ClineEnvelope). Read it so the failure
		// classifier sees the provider's wording, not an empty message.
		var enveloped struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(rawBody, &enveloped); err == nil && enveloped.Error != "" {
			e.Error.Message = enveloped.Error
		}
	}
	return &nativeOAuthHTTPError{status: status, code: e.Error.Code, message: e.Error.Message, scope: e.Error.Scope, headers: headers.Clone()}
}

func (openAIChatCodec) runSSE(streamCtx context.Context, cancel context.CancelFunc, resp *http.Response, requestID string, ch chan<- Chunk, inflights *inflightRegistry, firstByteTimeout, idleGapTimeout time.Duration) {
	runOpenAIChatOAuthSSE(streamCtx, cancel, resp, requestID, ch, inflights, firstByteTimeout, idleGapTimeout)
}
func (openAIChatCodec) capabilities() []Operation {
	return []Operation{OperationChat, OperationStreaming}
}

// runOpenAIChatOAuthSSE drives an OpenAI-compatible SSE stream over the
// native_oauth transport. It cannot reuse OpenAICompatibleTransport's private
// method, so it re-implements the same [DONE]/truncation contract using the
// shared chatCompletionStreamChunk type: a clean EOF WITHOUT the [DONE] marker
// is a truncation (ErrStreamTruncated), never a silent close.
func runOpenAIChatOAuthSSE(
	streamCtx context.Context,
	cancel context.CancelFunc,
	resp *http.Response,
	requestID string,
	ch chan<- Chunk,
	inflights *inflightRegistry,
	firstByteTimeout, idleGapTimeout time.Duration,
) {
	defer func() {
		inflights.unregister(requestID)
		_ = resp.Body.Close()
		cancel()
		close(ch)
	}()

	lineCh := sseScanner(streamCtx, resp.Body)
	firstByteTimer := time.NewTimer(firstByteTimeout)
	defer firstByteTimer.Stop()
	idleTimer := time.NewTimer(idleGapTimeout)
	defer idleTimer.Stop()
	firstByteSeen := false

	for {
		select {
		case <-streamCtx.Done():
			return
		case <-firstByteTimer.C:
			if !firstByteSeen {
				trySendChunk(ch, streamCtx, Chunk{Err: ErrStreamFirstByteTimeout})
				return
			}
		case <-idleTimer.C:
			trySendChunk(ch, streamCtx, Chunk{Err: ErrStreamIdleGapTimeout})
			return
		case ev, ok := <-lineCh:
			if !ok {
				trySendChunk(ch, streamCtx, Chunk{Err: ErrStreamTruncated})
				return
			}
			if ev.err != nil {
				trySendChunk(ch, streamCtx, Chunk{Err: fmt.Errorf("%w: %v", ErrTransportNetwork, ev.err)})
				return
			}
			if ev.line != "" {
				if !firstByteSeen {
					firstByteSeen = true
					firstByteTimer.Stop()
				}
				resetTimer(idleTimer, idleGapTimeout)
			}
			if !strings.HasPrefix(ev.line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(ev.line, "data: ")
			if data == "[DONE]" {
				trySendChunk(ch, streamCtx, Chunk{Done: true})
				return
			}
			var sc chatCompletionStreamChunk
			if jsonErr := json.Unmarshal([]byte(data), &sc); jsonErr != nil {
				trySendChunk(ch, streamCtx, Chunk{Err: fmt.Errorf("execution: openai-chat transport: decode stream chunk: %w", jsonErr)})
				return
			}
			if len(sc.Choices) == 0 {
				continue
			}
			if delta := sc.Choices[0].Delta.Content; delta != "" {
				if !trySendChunk(ch, streamCtx, Chunk{Delta: delta}) {
					return
				}
			}
		}
	}
}
