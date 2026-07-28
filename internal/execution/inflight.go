package execution

import (
	"bufio"
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

var (
	// ErrRequestNotInflight is returned by Cancel when the requestID is not
	// found in the registry — either it was never a cancellable stream, or
	// the stream finished before Cancel was called. Typed no-op: the caller
	// may log or ignore it; it never panics.
	ErrRequestNotInflight = errors.New("execution: request not in-flight or already finished")

	// ErrStreamFirstByteTimeout is sent as Chunk.Err when no SSE data
	// arrives within the transport's firstByteTimeout after the HTTP 200
	// response was established.
	ErrStreamFirstByteTimeout = errors.New("execution: stream: first byte not received before timeout")

	// ErrStreamIdleGapTimeout is sent as Chunk.Err when the idle gap
	// between consecutive SSE events exceeds the transport's idleGapTimeout.
	ErrStreamIdleGapTimeout = errors.New("execution: stream: idle gap timeout exceeded")
)

// sseLineEvent carries one SSE line from the scanner goroutine to the
// stream driver goroutine.
type sseLineEvent struct {
	line string
	err  error
}

// inflightRegistry is a mutex-protected store of in-flight stream
// CancelFuncs, keyed by RequestID. Each streaming transport instance
// owns exactly one registry; the transport's Cancel method delegates here.
type inflightRegistry struct {
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

func newInflightRegistry() *inflightRegistry {
	return &inflightRegistry{cancels: make(map[string]context.CancelFunc)}
}

// register stores cancel under id. No-op when id is empty.
func (r *inflightRegistry) register(id string, cancel context.CancelFunc) {
	if id == "" {
		return
	}
	r.mu.Lock()
	r.cancels[id] = cancel
	r.mu.Unlock()
}

// unregister removes id from the registry. No-op when id is empty.
func (r *inflightRegistry) unregister(id string) {
	if id == "" {
		return
	}
	r.mu.Lock()
	delete(r.cancels, id)
	r.mu.Unlock()
}

// cancel calls and removes the CancelFunc for id. Returns
// ErrRequestNotInflight when id is not found or already finished.
// The delete occurs inside the lock so a concurrent second cancel of the
// same id gets ErrRequestNotInflight, not a double-call.
func (r *inflightRegistry) cancel(id string) error {
	r.mu.Lock()
	fn, ok := r.cancels[id]
	if ok {
		delete(r.cancels, id)
	}
	r.mu.Unlock()
	if !ok {
		return ErrRequestNotInflight
	}
	fn()
	return nil
}

// resetTimer is the safe timer-reset pattern: drain a possibly-fired
// channel before resetting, so the next read of t.C cannot see a stale
// fire from before the reset.
func resetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}

// sseScanner starts a goroutine that reads lines from body and sends them
// to the returned channel. The goroutine honours ctx.Done() so it exits
// promptly when the stream context is cancelled (body is also closed by
// the caller's defer, which makes any blocked Read return an error).
func sseScanner(ctx context.Context, body io.Reader) <-chan sseLineEvent {
	lineCh := make(chan sseLineEvent, 32)
	go func() {
		defer close(lineCh)
		scanner := bufio.NewScanner(body)
		for scanner.Scan() {
			select {
			case lineCh <- sseLineEvent{line: scanner.Text()}:
			case <-ctx.Done():
				return
			}
		}
		if scanErr := scanner.Err(); scanErr != nil {
			select {
			case lineCh <- sseLineEvent{err: scanErr}:
			case <-ctx.Done():
			}
		}
	}()
	return lineCh
}
