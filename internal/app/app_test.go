package app

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRun_GracefulShutdown_WithinBound(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, NoopDrainer, ShutdownTimeout)
	}()

	select {
	case err := <-done:
		t.Fatalf("Run() returned before shutdown was requested, err = %v", err)
	case <-time.After(50 * time.Millisecond):
		// still running, as expected
	}

	begin := time.Now()
	cancel() // direct trigger, standing in for a delivered SIGINT/SIGTERM

	select {
	case err := <-done:
		elapsed := time.Since(begin)
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if elapsed > ShutdownTimeout {
			t.Fatalf("Run() took %v, want <= %v", elapsed, ShutdownTimeout)
		}
		t.Logf("graceful shutdown completed in %v (bound %v)", elapsed, ShutdownTimeout)
	case <-time.After(ShutdownTimeout + time.Second):
		t.Fatal("Run() did not return within bound + margin")
	}
}

func TestRun_GracefulShutdown_TimesOutWhenDrainBlocks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	blockingDrain := func(drainCtx context.Context) error {
		<-drainCtx.Done() // only stops once Run's bounded shutdownCtx expires
		return drainCtx.Err()
	}

	const timeout = 200 * time.Millisecond

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, blockingDrain, timeout)
	}()

	begin := time.Now()
	cancel()

	select {
	case err := <-done:
		elapsed := time.Since(begin)
		if !errors.Is(err, ErrShutdownTimedOut) {
			t.Fatalf("Run() error = %v, want ErrShutdownTimedOut", err)
		}
		if elapsed < timeout {
			t.Fatalf("Run() returned after %v, faster than bound %v", elapsed, timeout)
		}
		if elapsed > timeout+2*time.Second {
			t.Fatalf("Run() returned after %v, too far past bound %v", elapsed, timeout)
		}
		t.Logf("shutdown timed out after %v (bound %v)", elapsed, timeout)
	case <-time.After(timeout + 3*time.Second):
		t.Fatal("Run() did not return in time")
	}
}

func TestNotifyContext_ConstructsAndStopCancels(t *testing.T) {
	ctx, stop := NotifyContext(context.Background())

	select {
	case <-ctx.Done():
		t.Fatalf("ctx.Done() fired before any signal was delivered or stop() was called: %v", ctx.Err())
	default:
	}

	// Per signal.NotifyContext's documented semantics, calling stop()
	// itself marks ctx done (same effect as a delivered signal) in
	// addition to releasing the underlying signal handler.
	stop()

	select {
	case <-ctx.Done():
	default:
		t.Fatalf("ctx.Done() did not fire after stop()")
	}
	if err := ctx.Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("ctx.Err() = %v, want context.Canceled", err)
	}
}
