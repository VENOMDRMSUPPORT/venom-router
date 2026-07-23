// Package app implements the process-lifecycle primitives shared by the
// cli run modes (real OS signal registration, bounded graceful shutdown)
// and the composition root (Boot, in boot.go) that wires them together
// with the rest of the approved P0 units into a real, listening server.
package app

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// ShutdownTimeout bounds how long graceful shutdown is allowed to take
// before the caller should treat it as timed out. internal/cli's real
// serve/bare path uses this bound around Server.Shutdown. 5 seconds
// remains a conservative bound for what Shutdown actually does today
// (HTTP server drain, DB close, lock release) — revisit if a future
// component genuinely needs longer.
const ShutdownTimeout = 5 * time.Second

// Drainer performs whatever work is needed to shut down cleanly.
// Server.Shutdown's signature already matches Drainer, so production
// code (internal/cli) passes it directly to Run; NoopDrainer (below)
// exists only as a minimal fixture for this package's own tests of Run's
// generic wait-then-bounded-drain behavior.
type Drainer func(ctx context.Context) error

// NoopDrainer is a Drainer that completes immediately. Used only by this
// package's own tests — production code passes a real Drainer (e.g.
// Server.Shutdown) to Run instead.
func NoopDrainer(_ context.Context) error {
	return nil
}

// ErrShutdownTimedOut is returned by Run when drain does not complete
// before timeout elapses.
var ErrShutdownTimedOut = errors.New("app: graceful shutdown timed out")

// Run blocks until ctx is done (a shutdown request — real SIGINT/SIGTERM
// in production via NotifyContext, or a direct cancellation in tests),
// then invokes drain bounded by timeout. If drain does not return before
// the bound elapses, Run returns ErrShutdownTimedOut immediately without
// waiting further for the (possibly still-running) drain goroutine; the
// caller is expected to force-exit in that case.
func Run(ctx context.Context, drain Drainer, timeout time.Duration) error {
	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- drain(shutdownCtx)
	}()

	select {
	case err := <-done:
		return err
	case <-shutdownCtx.Done():
		return ErrShutdownTimedOut
	}
}

// NotifyContext returns a context that is canceled when the process
// receives SIGINT or SIGTERM (Ctrl+C included), and a stop function the
// caller must call to release the underlying signal handler. This
// registers real OS signal handling; cmd/venom's main calls this to
// drive Dispatch's ctx — it must not construct its own cancellation
// source in place of it.
func NotifyContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}
