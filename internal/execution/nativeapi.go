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

const (
	DefaultNativeAPITimeout          = 30 * time.Second
	DefaultNativeAPIFirstByteTimeout = 10 * time.Second
	DefaultNativeAPIIdleGapTimeout   = 30 * time.Second
)

// nativeAPIHTTPError carries a non-2xx Gemini response's status/code/message.
// Error() omits message — raw provider text is only accessible through
// Failure's RawMessage (probe path). It is a sibling of nativeOAuthHTTPError:
// the same shape, but a distinct type so each transport's Failure matches its
// own errors and never accidentally classifies the other's.
type nativeAPIHTTPError struct {
	status  int
	code    string
	message string
	scope   string
	headers http.Header
}

func (e *nativeAPIHTTPError) Error() string {
	return fmt.Sprintf("execution: native-api transport: http %d", e.status)
}

// NativeAPITransport is the InferenceTransport for the native_api type
// (01 §4.3): Gemini-style generateContent/streamGenerateContent against
// route.BaseURL, authenticated with the Google API-key header
// `x-goog-api-key` and NO Authorization header (03 §3 gemini-cli). It shares
// the entire Gemini wire mapping with NativeOAuthTransport (geminiwire.go);
// the ONLY difference between the two transports is this header.
type NativeAPITransport struct {
	client           *http.Client
	timeout          time.Duration
	firstByteTimeout time.Duration
	idleGapTimeout   time.Duration
	inflights        *inflightRegistry
}

// NewNativeAPITransport builds a transport with default streaming timeouts.
func NewNativeAPITransport(client *http.Client, timeout time.Duration) *NativeAPITransport {
	return newNativeAPITransport(client, timeout,
		DefaultNativeAPIFirstByteTimeout,
		DefaultNativeAPIIdleGapTimeout)
}

// newNativeAPITransport is the internal constructor for test-controlled
// firstByteTimeout and idleGapTimeout values.
func newNativeAPITransport(client *http.Client, timeout, firstByteTimeout, idleGapTimeout time.Duration) *NativeAPITransport {
	if timeout <= 0 {
		timeout = DefaultNativeAPITimeout
	}
	if firstByteTimeout <= 0 {
		firstByteTimeout = DefaultNativeAPIFirstByteTimeout
	}
	if idleGapTimeout <= 0 {
		idleGapTimeout = DefaultNativeAPIIdleGapTimeout
	}
	return &NativeAPITransport{
		client:           client,
		timeout:          timeout,
		firstByteTimeout: firstByteTimeout,
		idleGapTimeout:   idleGapTimeout,
		inflights:        newInflightRegistry(),
	}
}

// setGoogAuth applies the single authentication difference from the
// native_oauth sibling: the Google API-key header, and no Authorization.
func setGoogAuth(h http.Header, credential string) {
	h.Set("Content-Type", "application/json")
	h.Set("x-goog-api-key", credential)
}

// Execute sends one non-streamed generateContent request.
func (t *NativeAPITransport) Execute(ctx context.Context, route ResolvedRoute, req NormalizedRequest) (*NormalizedResponse, error) {
	reqCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	genReq, err := buildGeminiRequest(req)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(genReq)
	if err != nil {
		return nil, fmt.Errorf("execution: native-api transport: encode request: %w", err)
	}

	url := route.BaseURL + "/models/" + route.ModelID + ":generateContent"
	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("execution: native-api transport: build request: %w", err)
	}
	setGoogAuth(httpReq.Header, route.Credential.Value)

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
		return nil, t.newHTTPError(resp.StatusCode, rawBody, resp.Header)
	}

	return decodeGeminiSuccess(rawBody, resp.StatusCode)
}

// Stream sends a streaming request against the Gemini streamGenerateContent
// endpoint (?alt=sse). Pre-first-byte errors are returned from Stream;
// post-first-byte failures arrive as Chunk{Err: ...}.
func (t *NativeAPITransport) Stream(ctx context.Context, route ResolvedRoute, req NormalizedRequest) (<-chan Chunk, error) {
	streamCtx, cancel := context.WithCancel(ctx)

	genReq, err := buildGeminiRequest(req)
	if err != nil {
		cancel()
		return nil, err
	}
	payload, err := json.Marshal(genReq)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("execution: native-api transport: encode stream request: %w", err)
	}

	streamURL := route.BaseURL + "/models/" + route.ModelID + ":streamGenerateContent?alt=sse"
	httpReq, err := http.NewRequestWithContext(streamCtx, http.MethodPost, streamURL, bytes.NewReader(payload))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("execution: native-api transport: build stream request: %w", err)
	}
	setGoogAuth(httpReq.Header, route.Credential.Value)

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
		return nil, t.newHTTPError(resp.StatusCode, rawBody, resp.Header)
	}

	t.inflights.register(req.RequestID, cancel)

	ch := make(chan Chunk, 8)
	go runGeminiSSE(streamCtx, cancel, resp, req.RequestID, ch, t.inflights, t.firstByteTimeout, t.idleGapTimeout)
	return ch, nil
}

// newHTTPError decodes a non-2xx Gemini error body into the transport's typed
// error. The body text is carried ONLY as the (probe-path) message; it never
// enters Error().
func (t *NativeAPITransport) newHTTPError(status int, rawBody []byte, headers http.Header) *nativeAPIHTTPError {
	var errBody geminiErrBody
	_ = json.Unmarshal(rawBody, &errBody)
	return &nativeAPIHTTPError{
		status:  status,
		code:    errBody.Error.Status,
		message: errBody.Error.Message,
		scope:   errBody.Error.Scope,
		headers: headers.Clone(),
	}
}

// Cancel aborts an in-flight stream. Returns ErrRequestNotInflight when the ID
// is unknown or the stream already finished.
func (t *NativeAPITransport) Cancel(_ context.Context, _ ResolvedRoute, requestID string) error {
	return t.inflights.cancel(requestID)
}

// NormalizeError derives the stable VenomError from the SAME classification
// Failure performs, so the two shapes can never disagree (P4-EXEC-002).
func (t *NativeAPITransport) NormalizeError(err error, route ResolvedRoute) VenomError {
	f := t.Failure(err, route)
	return VenomError{Code: string(f.FailureClass), Message: f.SafeMessage, Retryable: f.Retryable}
}

// Failure classifies err using the shared 4-rung ladder. Timeout/network
// sentinels bypass the ladder (HTTPStatus stays 0). HTTP rejections carry
// RawMessage for the probe path; it is never placed in SafeMessage.
func (t *NativeAPITransport) Failure(err error, _ ResolvedRoute) TypedFailure {
	if errors.Is(err, ErrTransportTimeout) {
		return TypedFailure{FailureClass: FailureClassNetwork, Scope: FailureScopeTransientTransport, Retryable: true, SafeMessage: safeMessageFor(FailureClassNetwork)}
	}
	if errors.Is(err, ErrTransportNetwork) {
		return TypedFailure{FailureClass: FailureClassNetwork, Scope: FailureScopeTransientTransport, Retryable: true, SafeMessage: safeMessageFor(FailureClassNetwork)}
	}
	var httpErr *nativeAPIHTTPError
	if errors.As(err, &httpErr) {
		f := ClassifyFailure(httpErr.code, httpErr.scope, httpErr.headers, nil, httpErr.status)
		f.RawMessage = httpErr.message
		return f
	}
	return TypedFailure{FailureClass: FailureClassServer, SafeMessage: "an internal error occurred"}
}

// SupportedCapabilities reports chat, streaming, tools and vision — the
// operations buildGeminiRequest genuinely serializes. Vision is expressible
// only in the inline base64 + media-type form: buildGeminiRequest (via
// geminiPartsFor) requires ImageBase64 and MediaType and fails closed with
// ErrRequestFeatureUnsupported on a URL-only image part. Structured output
// is deliberately absent too: buildGeminiRequest fails CLOSED on a set
// ResponseFormat, as it does on any other request feature it cannot express
// (a tool_choice directive) — never silently dropped.
func (t *NativeAPITransport) SupportedCapabilities(_ ResolvedRoute) []Operation {
	return []Operation{OperationChat, OperationStreaming, OperationTools, OperationVision}
}

// Compile-time proof NativeAPITransport satisfies InferenceTransport.
var _ InferenceTransport = (*NativeAPITransport)(nil)
