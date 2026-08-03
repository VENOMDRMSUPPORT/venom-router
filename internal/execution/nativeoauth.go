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

// The Gemini generateContent request/response wire types, the request
// builder (buildGeminiRequest / geminiPartsFor / geminiRoleFor), the success
// decoder (decodeGeminiSuccess), and the SSE runner (parseGeminiSSEData /
// runGeminiSSE) live in geminiwire.go — shared verbatim with
// NativeAPITransport (P7-EXEC-001). This transport differs from that sibling
// in exactly one dimension: it authenticates with Authorization: Bearer,
// where the native_api sibling uses x-goog-api-key.

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

	return decodeGeminiSuccess(rawBody, resp.StatusCode)
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
	go runGeminiSSE(streamCtx, cancel, resp, req.RequestID, ch, t.inflights, t.firstByteTimeout, t.idleGapTimeout)
	return ch, nil
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
