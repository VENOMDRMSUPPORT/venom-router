package httpapi

// benchmark_stream_test.go pins newBenchmarkStreamFn — the production
// benchmarkStreamFn (Task 4 of the local-benchmark-rating plan) that drives
// execution.Dispatcher.Stream (the seam Task 3 chose; see
// benchmark_engine.go's Step-0 comment) with a real *execution.
// OpenAICompatibleTransport behind an httptest SSE server wherever feasible,
// so these tests exercise the real wire codec, not a hand-rolled fake of it.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/execution"
)

// benchmarkFakeCredLister is the benchmarkCredentialLister fake: an account
// has either one active credential or none (or the lookup itself errors).
// Named distinctly from routingadapters_test.go's fakeCredLister (a
// different, narrower fake in the same package) to avoid a redeclaration.
type benchmarkFakeCredLister struct {
	credentialID string
	hasActive    bool
	err          error
}

func (f benchmarkFakeCredLister) ListForAccount(context.Context, string) ([]domain.Credential, error) {
	if f.err != nil {
		return nil, f.err
	}
	if !f.hasActive {
		return nil, nil
	}
	return []domain.Credential{{ID: f.credentialID, State: domain.CredentialActive}}, nil
}

// sseWrite writes one SSE "data: " frame and flushes it immediately, exactly
// like the openAISseServer helper in internal/execution/streaming_test.go
// (unexported there, so redeclared here for this package's own tests).
func sseWrite(w http.ResponseWriter, fl http.Flusher, event string) {
	_, _ = fmt.Fprintf(w, "data: %s\n\n", event)
	if fl != nil {
		fl.Flush()
	}
}

// TestBenchmarkStreamFn_MultiChunkSuccess is Task 4's required test 1: three
// content chunks at small, real delays, terminated by [DONE]. Assertions are
// ORDER-OF-MAGNITUDE only per the brief: TTFT must be at least the first
// delay (proving TTFT is measured from real elapsed time, not faked), and
// TokensPerSec must be positive.
func TestBenchmarkStreamFn_MultiChunkSuccess(t *testing.T) {
	const firstDelay = 30 * time.Millisecond
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		time.Sleep(firstDelay)
		sseWrite(w, fl, `{"choices":[{"delta":{"content":"one"},"finish_reason":null}]}`)
		time.Sleep(10 * time.Millisecond)
		sseWrite(w, fl, `{"choices":[{"delta":{"content":"two"},"finish_reason":null}]}`)
		time.Sleep(10 * time.Millisecond)
		sseWrite(w, fl, `{"choices":[{"delta":{"content":"three"},"finish_reason":"stop"}]}`)
		sseWrite(w, fl, `[DONE]`)
	}))
	t.Cleanup(srv.Close)

	tr := execution.NewOpenAICompatibleTransport(&http.Client{}, 5*time.Second)
	creds := benchmarkFakeCredLister{credentialID: "cred-1", hasActive: true}
	leaser := &fakeLeaser{key: "leased-secret"}
	streamFn := newBenchmarkStreamFn(tr, func(string) string { return srv.URL }, creds, leaser)

	sample, err := streamFn(context.Background(), "acct-1", "prov-1", "model-1", "hello", 64)
	if err != nil {
		t.Fatalf("streamFn() error = %v, want nil", err)
	}
	if !sample.OK {
		t.Fatalf("sample.OK = false, want true; sample = %+v", sample)
	}
	if sample.TTFT < firstDelay {
		t.Errorf("TTFT = %v, want >= %v (the server's own first-chunk delay)", sample.TTFT, firstDelay)
	}
	if sample.TokensPerSec <= 0 {
		t.Errorf("TokensPerSec = %v, want > 0", sample.TokensPerSec)
	}
	if !leaser.called {
		t.Error("credential was never leased")
	}
	if gotAuth != "Bearer leased-secret" {
		t.Errorf("Authorization header = %q, want %q (the leased key must reach the route)", gotAuth, "Bearer leased-secret")
	}
}

// TestBenchmarkStreamFn_NonTwoXX_OKFalseErrNil is Task 4's required test 2: a
// non-2xx response is a PROVIDER REJECTION, a failed sample — not a Go error.
func TestBenchmarkStreamFn_NonTwoXX_OKFalseErrNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"context_length_exceeded","message":"too long"}}`))
	}))
	t.Cleanup(srv.Close)

	tr := execution.NewOpenAICompatibleTransport(&http.Client{}, 5*time.Second)
	creds := benchmarkFakeCredLister{credentialID: "cred-1", hasActive: true}
	leaser := &fakeLeaser{key: "leased-secret"}
	streamFn := newBenchmarkStreamFn(tr, func(string) string { return srv.URL }, creds, leaser)

	sample, err := streamFn(context.Background(), "acct-1", "prov-1", "model-1", "hello", 64)
	if err != nil {
		t.Fatalf("streamFn() error = %v, want nil (a provider rejection is a failed sample, not a transport error)", err)
	}
	if sample.OK {
		t.Errorf("sample.OK = true, want false for a 400 response; sample = %+v", sample)
	}
}

// TestBenchmarkStreamFn_MidStreamDrop_ErrNonNil is Task 4's required test 3:
// an abrupt connection kill AFTER content has started streaming must surface
// as a non-nil error (a connection drop is a transport error, not a sample).
func TestBenchmarkStreamFn_MidStreamDrop_ErrNonNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		sseWrite(w, fl, `{"choices":[{"delta":{"content":"hi"},"finish_reason":null}]}`)
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("httptest server does not support hijacking")
			return
		}
		conn, _, hijackErr := hj.Hijack()
		if hijackErr != nil {
			t.Errorf("hijack: %v", hijackErr)
			return
		}
		_ = conn.Close() // abrupt kill mid-stream
	}))
	t.Cleanup(srv.Close)

	tr := execution.NewOpenAICompatibleTransport(&http.Client{}, 5*time.Second)
	creds := benchmarkFakeCredLister{credentialID: "cred-1", hasActive: true}
	leaser := &fakeLeaser{key: "leased-secret"}
	streamFn := newBenchmarkStreamFn(tr, func(string) string { return srv.URL }, creds, leaser)

	sample, err := streamFn(context.Background(), "acct-1", "prov-1", "model-1", "hello", 64)
	if err == nil {
		t.Fatalf("streamFn() error = nil, want non-nil after a mid-stream connection drop; sample = %+v", sample)
	}
}

// TestBenchmarkStreamFn_NoActiveCredential_ReturnsError proves the credential
// lookup is real, not a hand-waved success path: an account with no active
// credential must fail before any dispatch is attempted.
func TestBenchmarkStreamFn_NoActiveCredential_ReturnsError(t *testing.T) {
	creds := benchmarkFakeCredLister{hasActive: false}
	leaser := &fakeLeaser{key: "leased-secret"}
	streamFn := newBenchmarkStreamFn(nil, func(string) string { return "http://unused" }, creds, leaser)

	_, err := streamFn(context.Background(), "acct-1", "prov-1", "model-1", "hello", 64)
	if err == nil {
		t.Fatal("streamFn() error = nil, want non-nil for an account with no active credential")
	}
	if leaser.called {
		t.Error("credential was leased despite no active credential id being found")
	}
}

// TestBenchmarkStreamFn_CredentialLeaseError_ReturnsError proves a lease
// (decrypt) failure is surfaced as a transport error, not silently turned
// into a failed-but-OK-shaped sample.
func TestBenchmarkStreamFn_CredentialLeaseError_ReturnsError(t *testing.T) {
	creds := benchmarkFakeCredLister{credentialID: "cred-1", hasActive: true}
	leaseErr := errors.New("boom: decrypt failed")
	leaser := &fakeLeaser{err: leaseErr}
	streamFn := newBenchmarkStreamFn(nil, func(string) string { return "http://unused" }, creds, leaser)

	sample, err := streamFn(context.Background(), "acct-1", "prov-1", "model-1", "hello", 64)
	if !errors.Is(err, leaseErr) {
		t.Fatalf("streamFn() error = %v, want %v", err, leaseErr)
	}
	if sample.OK {
		t.Errorf("sample.OK = true after a lease error, want zero-value sample; sample = %+v", sample)
	}
}
