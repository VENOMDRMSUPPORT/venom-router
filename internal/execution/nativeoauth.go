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

// nativeOAuthHTTPError carries a non-2xx Gemini response's status/code/
// message. Error() omits message — raw provider text is only accessible
// through Failure's RawMessage (probe path).
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

// Gemini generateContent request/response wire types ----------------------

type geminiPart struct {
	Text         *string           `json:"text,omitempty"`
	InlineData   *geminiInlineData `json:"inlineData,omitempty"`
	FunctionCall *geminiFnCall     `json:"functionCall,omitempty"`
}

// geminiInlineData carries an inline (base64) image for a multimodal part
// (P5-EXEC-004). Gemini's REST inlineData requires the bytes inline — a bare
// image URL is NOT expressible here, so a URL-only image fails closed.
type geminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiFnCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

// geminiFunctionDeclaration is one declared tool (P5-EXEC-004). Parameters is
// embedded raw JSON — the client's schema is forwarded verbatim.
type geminiFunctionDeclaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations"`
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
	Tools            []geminiTool            `json:"tools,omitempty"`
	GenerationConfig *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

// buildGeminiRequest maps a normalized request onto Gemini's generateContent
// shape, failing CLOSED (ErrRequestFeatureUnsupported) on anything it cannot
// faithfully express — a URL-only image (inlineData needs the bytes), an
// unknown content-part kind, or a tool_choice directive (Gemini's
// functionCallingConfig has no faithful mapping for an arbitrary OpenAI
// tool_choice string). A text-only request produces exactly the pre-P5-EXEC-004
// shape. The error names the FEATURE only, never the tool description or URL.
func buildGeminiRequest(req NormalizedRequest) (geminiGenerateReq, error) {
	if req.ToolChoice != "" {
		return geminiGenerateReq{}, fmt.Errorf("%w: tool_choice", ErrRequestFeatureUnsupported)
	}
	contents := make([]geminiContent, 0, len(req.Messages))
	for _, m := range req.Messages {
		parts, err := geminiPartsFor(m)
		if err != nil {
			return geminiGenerateReq{}, err
		}
		contents = append(contents, geminiContent{Role: geminiRoleFor(m.Role), Parts: parts})
	}
	out := geminiGenerateReq{Contents: contents}
	if len(req.Tools) > 0 {
		decls := make([]geminiFunctionDeclaration, 0, len(req.Tools))
		for _, td := range req.Tools {
			d := geminiFunctionDeclaration{Name: td.Name, Description: td.Description}
			if td.ParametersJSON != "" {
				d.Parameters = json.RawMessage(td.ParametersJSON)
			}
			decls = append(decls, d)
		}
		out.Tools = []geminiTool{{FunctionDeclarations: decls}}
	}
	if req.MaxTokens != nil {
		out.GenerationConfig = &geminiGenerationConfig{MaxOutputTokens: req.MaxTokens}
	}
	return out, nil
}

// geminiPartsFor maps one message's content to Gemini parts. A message with no
// Parts keeps the single-text-part shape used before P5-EXEC-004.
func geminiPartsFor(m Message) ([]geminiPart, error) {
	if len(m.Parts) == 0 {
		text := m.Content
		return []geminiPart{{Text: &text}}, nil
	}
	parts := make([]geminiPart, 0, len(m.Parts))
	for _, p := range m.Parts {
		switch p.Kind {
		case ContentPartText:
			text := p.Text
			parts = append(parts, geminiPart{Text: &text})
		case ContentPartImage:
			if p.ImageBase64 == "" || p.MediaType == "" {
				// A bare URL (or an image missing its media type) cannot be
				// expressed as inlineData — fail closed rather than drop it.
				return nil, fmt.Errorf("%w: image part requires inline base64 data and a media type", ErrRequestFeatureUnsupported)
			}
			parts = append(parts, geminiPart{InlineData: &geminiInlineData{MimeType: p.MediaType, Data: p.ImageBase64}})
		default:
			return nil, fmt.Errorf("%w: content part kind %q", ErrRequestFeatureUnsupported, p.Kind)
		}
	}
	return parts, nil
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
	Scope   string `json:"scope"`
}

type geminiErrBody struct {
	Error geminiErrDetail `json:"error"`
}

// NativeOAuthTransport is the InferenceTransport for the native_oauth
// type (01 §4.3): Gemini-style generateContent/streamGenerateContent
// against route.BaseURL with Authorization: Bearer.
type NativeOAuthTransport struct {
	client           *http.Client
	timeout          time.Duration
	firstByteTimeout time.Duration
	idleGapTimeout   time.Duration
	inflights        *inflightRegistry
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
	}
}

// geminiRoleFor maps Venom's role vocabulary onto Gemini's.
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

	genReq, err := buildGeminiRequest(req)
	if err != nil {
		return nil, err
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

// Stream sends a streaming request against the Gemini streamGenerateContent
// endpoint (?alt=sse). Pre-first-byte errors are returned from Stream;
// post-first-byte failures arrive as Chunk{Err: ...}.
func (t *NativeOAuthTransport) Stream(ctx context.Context, route ResolvedRoute, req NormalizedRequest) (<-chan Chunk, error) {
	streamCtx, cancel := context.WithCancel(ctx)

	genReq, err := buildGeminiRequest(req)
	if err != nil {
		cancel()
		return nil, err
	}
	payload, err := json.Marshal(genReq)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("execution: native-oauth transport: encode stream request: %w", err)
	}

	streamURL := route.BaseURL + "/models/" + route.ModelID + ":streamGenerateContent?alt=sse"
	httpReq, err := http.NewRequestWithContext(streamCtx, http.MethodPost, streamURL, bytes.NewReader(payload))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("execution: native-oauth transport: build stream request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+route.Credential.Value)

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

	t.inflights.register(req.RequestID, cancel)

	ch := make(chan Chunk, 8)
	go t.runNativeOAuthSSE(streamCtx, cancel, resp, req.RequestID, ch)
	return ch, nil
}

// parseGeminiSSEData decodes one "data: ..." SSE payload into a delta
// string and a done flag. Branchy JSON + parts-walk logic is isolated
// here so runNativeOAuthSSE stays within the gocyclo limit.
func parseGeminiSSEData(data string) (delta string, done bool, err error) {
	var sc geminiGenerateResp
	if jsonErr := json.Unmarshal([]byte(data), &sc); jsonErr != nil {
		return "", false, fmt.Errorf("execution: native-oauth transport: decode stream chunk: %w", jsonErr)
	}
	if len(sc.Candidates) == 0 {
		return "", false, nil
	}
	candidate := sc.Candidates[0]
	for _, part := range candidate.Content.Parts {
		if part.Text != nil {
			delta += *part.Text
		}
	}
	done = candidate.FinishReason != "" && candidate.FinishReason != "FINISH_REASON_UNSPECIFIED"
	return delta, done, nil
}

// runNativeOAuthSSE is the goroutine body for Stream. Extracting it into
// a named method keeps (*NativeOAuthTransport).Stream's cyclomatic
// complexity within the project's gocyclo limit.
func (t *NativeOAuthTransport) runNativeOAuthSSE(
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
				// Natural EOF — Gemini closes the connection when done.
				select {
				case ch <- Chunk{Done: true}:
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
			delta, done, parseErr := parseGeminiSSEData(strings.TrimPrefix(ev.line, "data: "))
			if parseErr != nil {
				select {
				case ch <- Chunk{Err: parseErr}:
				case <-streamCtx.Done():
				}
				return
			}
			if delta != "" {
				select {
				case ch <- Chunk{Delta: delta}:
				case <-streamCtx.Done():
					return
				}
			}
			if done {
				select {
				case ch <- Chunk{Done: true}:
				case <-streamCtx.Done():
				}
				return
			}
		}
	}
}

// Cancel aborts an in-flight stream. Returns ErrRequestNotInflight when
// the ID is unknown or the stream already finished.
func (t *NativeOAuthTransport) Cancel(_ context.Context, _ ResolvedRoute, requestID string) error {
	return t.inflights.cancel(requestID)
}

// NormalizeError derives the stable VenomError from the SAME
// classification Failure performs, so the two shapes can never disagree
// (P4-EXEC-002): code is the FailureClass, message the Venom-authored
// SafeMessage, retryable the taxonomy's verdict.
func (t *NativeOAuthTransport) NormalizeError(err error, route ResolvedRoute) VenomError {
	f := t.Failure(err, route)
	return VenomError{Code: string(f.FailureClass), Message: f.SafeMessage, Retryable: f.Retryable}
}

// Failure classifies err using the 4-rung ladder. RawMessage is set for
// the probe path; it is never placed in SafeMessage.
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

// SupportedCapabilities reports chat and streaming.
func (t *NativeOAuthTransport) SupportedCapabilities(_ ResolvedRoute) []Operation {
	return []Operation{OperationChat, OperationStreaming}
}

// Compile-time proof NativeOAuthTransport satisfies InferenceTransport.
var _ InferenceTransport = (*NativeOAuthTransport)(nil)
