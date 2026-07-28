package execution

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// drainChunks reads from ch until it closes (or Done/Err is seen) and
// returns all chunks. Fails the test if no close arrives within timeout.
func drainChunks(t *testing.T, ch <-chan Chunk, timeout time.Duration) []Chunk {
	t.Helper()
	var chunks []Chunk
	deadline := time.After(timeout)
	for {
		select {
		case c, ok := <-ch:
			if !ok {
				return chunks
			}
			chunks = append(chunks, c)
			if c.Done || c.Err != nil {
				for range ch {
				}
				return chunks
			}
		case <-deadline:
			t.Fatalf("drainChunks: timed out after %v; chunks so far: %+v", timeout, chunks)
			return nil
		}
	}
}

// openAISseServer starts an httptest server that serves a fixed sequence
// of SSE events as an OpenAI-compatible stream.
func openAISseServer(t *testing.T, events []string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		for _, ev := range events {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", ev)
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// geminiSseServer starts an httptest server that serves a fixed sequence
// of SSE events as a Gemini streamGenerateContent stream.
func geminiSseServer(t *testing.T, events []string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		for _, ev := range events {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", ev)
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// --- OpenAI-compatible streaming tests -------------------------------------

// TestStream_OpenAI_MultiChunkRoundTrip is mutation row S.1: a 3-chunk
// OpenAI SSE stream followed by [DONE] must deliver all deltas and one
// Done=true chunk in order. Mutation: drop a data line → RED; restore → GREEN.
func TestStream_OpenAI_MultiChunkRoundTrip(t *testing.T) {
	events := []string{
		`{"choices":[{"delta":{"content":"Hello"},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"content":" World"},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"content":"!"},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv := openAISseServer(t, events)

	tr := newOpenAICompatibleTransport(&http.Client{}, 5*time.Second, 2*time.Second, 2*time.Second)
	ch, err := tr.Stream(context.Background(), newOpenAICompatTestRoute(srv.URL), NormalizedRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}

	chunks := drainChunks(t, ch, 5*time.Second)
	if len(chunks) < 4 {
		t.Fatalf("got %d chunks, want ≥4 (3 deltas + Done); chunks = %+v", len(chunks), chunks)
	}
	if chunks[0].Delta != "Hello" {
		t.Errorf("chunks[0].Delta = %q, want %q", chunks[0].Delta, "Hello")
	}
	if chunks[1].Delta != " World" {
		t.Errorf("chunks[1].Delta = %q, want %q", chunks[1].Delta, " World")
	}
	if chunks[2].Delta != "!" {
		t.Errorf("chunks[2].Delta = %q, want %q", chunks[2].Delta, "!")
	}
	last := chunks[len(chunks)-1]
	if !last.Done {
		t.Errorf("last chunk Done = false, want true; chunk = %+v", last)
	}
}

// TestStream_OpenAI_PreFirstByteError is mutation row S.2: a 400 response
// must be returned as an error from Stream itself (not a channel chunk).
// Mutation: handle the 400 inside the goroutine → RED; restore → GREEN.
func TestStream_OpenAI_PreFirstByteError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"context_length_exceeded","message":"too long"}}`))
	}))
	t.Cleanup(srv.Close)

	tr := newOpenAICompatibleTransport(&http.Client{}, 5*time.Second, 2*time.Second, 2*time.Second)
	ch, err := tr.Stream(context.Background(), newOpenAICompatTestRoute(srv.URL), NormalizedRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("Stream() error = nil, want non-nil for 400 before any SSE data")
	}
	if ch != nil {
		t.Fatal("Stream() ch = non-nil, want nil when error is returned from Stream itself")
	}
	f := tr.Failure(err, newOpenAICompatTestRoute(srv.URL))
	if f.FailureClass != FailureClassInvalidRequest {
		t.Errorf("FailureClass = %q, want %q", f.FailureClass, FailureClassInvalidRequest)
	}
}

// TestStream_OpenAI_FirstByteTimeout is mutation row S.3: if the server
// accepts the connection (200 OK) but sends no SSE data before
// firstByteTimeout, Chunk{Err: ErrStreamFirstByteTimeout} must arrive.
// Mutation: mark firstByteSeen on any timer event → RED; restore → GREEN.
func TestStream_OpenAI_FirstByteTimeout(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		<-block
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(block) })

	const fbTimeout = 50 * time.Millisecond
	tr := newOpenAICompatibleTransport(&http.Client{}, 5*time.Second, fbTimeout, 2*time.Second)
	ch, err := tr.Stream(context.Background(), newOpenAICompatTestRoute(srv.URL), NormalizedRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v, want nil (200 was sent)", err)
	}

	chunks := drainChunks(t, ch, 2*time.Second)
	if len(chunks) == 0 {
		t.Fatal("got 0 chunks, want ErrStreamFirstByteTimeout chunk")
	}
	last := chunks[len(chunks)-1]
	if !errors.Is(last.Err, ErrStreamFirstByteTimeout) {
		t.Errorf("last chunk Err = %v, want ErrStreamFirstByteTimeout", last.Err)
	}
}

// TestStream_OpenAI_IdleGapTimeout is mutation row S.4: if the server
// sends one chunk then stops, Chunk{Err: ErrStreamIdleGapTimeout} must
// arrive after idleGapTimeout. Mutation: never reset the idle timer →
// RED; restore → GREEN.
func TestStream_OpenAI_IdleGapTimeout(t *testing.T) {
	started := make(chan struct{})
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"content":"hi"},"finish_reason":null}]}`)
		if fl != nil {
			fl.Flush()
		}
		close(started)
		<-block
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(block) })

	const idleTimeout = 50 * time.Millisecond
	tr := newOpenAICompatibleTransport(&http.Client{}, 5*time.Second, 2*time.Second, idleTimeout)
	ch, err := tr.Stream(context.Background(), newOpenAICompatTestRoute(srv.URL), NormalizedRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("server never started sending")
	}

	chunks := drainChunks(t, ch, 2*time.Second)
	var foundIdle bool
	for _, c := range chunks {
		if errors.Is(c.Err, ErrStreamIdleGapTimeout) {
			foundIdle = true
		}
	}
	if !foundIdle {
		t.Errorf("no ErrStreamIdleGapTimeout chunk; chunks = %+v", chunks)
	}
}

// TestStream_OpenAI_Cancel is mutation row S.5: Cancel with a registered
// RequestID must stop the stream; Cancel with an unknown ID must return
// ErrRequestNotInflight (typed no-op, never panic).
// Mutation: skip registry lookup in Cancel → RED; restore → GREEN.
func TestStream_OpenAI_Cancel(t *testing.T) {
	started := make(chan struct{}, 1)
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"content":"start"},"finish_reason":null}]}`)
		if fl != nil {
			fl.Flush()
		}
		started <- struct{}{}
		<-block
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(block) })

	const reqID = "req-stream-cancel-001"
	tr := newOpenAICompatibleTransport(&http.Client{}, 5*time.Second, 2*time.Second, 2*time.Second)
	route := newOpenAICompatTestRoute(srv.URL)
	ch, err := tr.Stream(context.Background(), route, NormalizedRequest{
		Messages:  []Message{{Role: "user", Content: "hi"}},
		RequestID: reqID,
	})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("stream never started")
	}

	if cancelErr := tr.Cancel(context.Background(), route, reqID); cancelErr != nil {
		t.Fatalf("Cancel() error = %v, want nil", cancelErr)
	}

	drainChunks(t, ch, 2*time.Second)

	// Unknown ID must be a typed no-op, never a panic.
	unknownErr := tr.Cancel(context.Background(), route, "no-such-id")
	if !errors.Is(unknownErr, ErrRequestNotInflight) {
		t.Fatalf("Cancel(unknown) = %v, want ErrRequestNotInflight", unknownErr)
	}
}

// TestStream_OpenAI_PostFirstByteError is mutation row S.6: an error that
// occurs AFTER the first SSE byte must arrive as Chunk{Err: ...} through
// the channel, not as an error from Stream itself.
// Mutation: return the parse error from Stream → RED; restore → GREEN.
func TestStream_OpenAI_PostFirstByteError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"content":"hi"},"finish_reason":null}]}`)
		if fl != nil {
			fl.Flush()
		}
		// Malformed second event triggers a post-first-byte parse error.
		_, _ = fmt.Fprintf(w, "data: %s\n\n", `{INVALID}`)
		if fl != nil {
			fl.Flush()
		}
	}))
	t.Cleanup(srv.Close)

	tr := newOpenAICompatibleTransport(&http.Client{}, 5*time.Second, 2*time.Second, 2*time.Second)
	ch, err := tr.Stream(context.Background(), newOpenAICompatTestRoute(srv.URL), NormalizedRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v, want nil (200 was established)", err)
	}

	chunks := drainChunks(t, ch, 5*time.Second)
	if len(chunks) == 0 {
		t.Fatal("got 0 chunks, want at least one delta then an error")
	}
	if chunks[0].Delta != "hi" {
		t.Errorf("chunks[0].Delta = %q, want %q", chunks[0].Delta, "hi")
	}
	var foundErr bool
	for _, c := range chunks {
		if c.Err != nil {
			foundErr = true
		}
	}
	if !foundErr {
		t.Errorf("no error chunk after malformed event; chunks = %+v", chunks)
	}
}

// --- NativeOAuth streaming tests -------------------------------------------

// TestStream_NativeOAuth_MultiChunkRoundTrip is mutation row S.7: a
// 2-event Gemini SSE stream with finishReason on the last must deliver
// all deltas and a Done=true chunk. Mutation: skip finishReason check →
// RED; restore → GREEN.
func TestStream_NativeOAuth_MultiChunkRoundTrip(t *testing.T) {
	events := []string{
		`{"candidates":[{"content":{"parts":[{"text":"Hello"}],"role":"model"},"finishReason":""}]}`,
		`{"candidates":[{"content":{"parts":[{"text":" World"}],"role":"model"},"finishReason":"STOP"}]}`,
	}
	srv := geminiSseServer(t, events)

	tr := newNativeOAuthTransport(&http.Client{}, 5*time.Second, 2*time.Second, 2*time.Second)
	route := newNativeOAuthTestRoute(srv.URL)
	ch, err := tr.Stream(context.Background(), route, NormalizedRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}

	chunks := drainChunks(t, ch, 5*time.Second)
	var text string
	var foundDone bool
	for _, c := range chunks {
		if c.Err != nil {
			t.Fatalf("unexpected error chunk: %v", c.Err)
		}
		text += c.Delta
		if c.Done {
			foundDone = true
		}
	}
	if text != "Hello World" {
		t.Errorf("combined delta = %q, want %q", text, "Hello World")
	}
	if !foundDone {
		t.Errorf("no Done=true chunk; chunks = %+v", chunks)
	}
}

// TestStream_NativeOAuth_PreFirstByteError is mutation row S.8: a 403
// response must be returned from Stream itself (pre-first-byte boundary).
func TestStream_NativeOAuth_PreFirstByteError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":403,"status":"PERMISSION_DENIED","message":"no access"}}`))
	}))
	t.Cleanup(srv.Close)

	tr := newNativeOAuthTransport(&http.Client{}, 5*time.Second, 2*time.Second, 2*time.Second)
	ch, err := tr.Stream(context.Background(), newNativeOAuthTestRoute(srv.URL), NormalizedRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("Stream() error = nil, want non-nil for 403")
	}
	if ch != nil {
		t.Fatal("Stream() ch = non-nil, want nil for pre-first-byte error")
	}
	f := tr.Failure(err, newNativeOAuthTestRoute(srv.URL))
	if f.FailureClass != FailureClassAuth {
		t.Errorf("FailureClass = %q, want %q", f.FailureClass, FailureClassAuth)
	}
}

// --- Inflight registry tests ------------------------------------------------

// TestInflight_Cancel_UnknownID_IsTypedNoOp is mutation row S.9: Cancel on
// a never-registered ID must return ErrRequestNotInflight, never panic.
// Mutation: return nil for unknown IDs → RED; restore → GREEN.
func TestInflight_Cancel_UnknownID_IsTypedNoOp(t *testing.T) {
	reg := newInflightRegistry()
	err := reg.cancel("no-such-id")
	if !errors.Is(err, ErrRequestNotInflight) {
		t.Fatalf("cancel(unknown) = %v, want ErrRequestNotInflight", err)
	}
}

// TestInflight_Cancel_DoubleCancel_IsTypedNoOp is mutation row S.10: a
// second Cancel for the same ID (after the first already fired and
// removed the entry) must return ErrRequestNotInflight, and the cancel
// func must not be called a second time.
func TestInflight_Cancel_DoubleCancel_IsTypedNoOp(t *testing.T) {
	reg := newInflightRegistry()
	called := 0
	reg.register("req-1", func() { called++ })

	if err := reg.cancel("req-1"); err != nil {
		t.Fatalf("first cancel() = %v, want nil", err)
	}
	if called != 1 {
		t.Fatalf("cancel func called %d times after first cancel, want 1", called)
	}

	if err := reg.cancel("req-1"); !errors.Is(err, ErrRequestNotInflight) {
		t.Fatalf("second cancel() = %v, want ErrRequestNotInflight", err)
	}
	if called != 1 {
		t.Fatalf("cancel func called %d times after double cancel, want 1", called)
	}
}
