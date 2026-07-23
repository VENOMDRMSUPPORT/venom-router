package cli

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/app"
)

// setTestDataDir points platform's data-dir resolution (used by
// app.Boot's lock/DB) at a fresh temp directory for the duration of the
// test, so real-boot tests never touch the real user profile.
func setTestDataDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	t.Setenv("XDG_DATA_HOME", dir)
}

// freeLoopbackAddr reserves a real free port via a throwaway listener,
// closes it immediately, and returns the address — used so tests that
// need to know the exact bind address up front (to dial it later) don't
// have to hardcode a port that might already be in use.
func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve free port: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close temp listener: %v", err)
	}
	return addr
}

// waitForListener polls addr until something accepts a TCP connection
// there (or the deadline elapses), since app.Boot does real work (lock,
// DB open, migrate) before its listener comes up.
func waitForListener(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for a listener at %q", addr)
}

func TestDispatch_Version(t *testing.T) {
	var stdout, stderr bytes.Buffer

	err := Dispatch(context.Background(), []string{"version"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != version {
		t.Fatalf("stdout = %q, want %q", got, version)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestDispatch_Help(t *testing.T) {
	var stdout, stderr bytes.Buffer

	err := Dispatch(context.Background(), []string{"help"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "venom serve") {
		t.Fatalf("stdout = %q, want usage text mentioning modes", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestDispatch_Unrecognized(t *testing.T) {
	var stdout, stderr bytes.Buffer

	err := Dispatch(context.Background(), []string{"bogus"}, &stdout, &stderr)
	if !errors.Is(err, ErrUnrecognizedMode) {
		t.Fatalf("Dispatch() error = %v, want ErrUnrecognizedMode", err)
	}
	if stderr.Len() == 0 {
		t.Fatalf("stderr is empty, want an error message")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty (unrecognized mode must not run any known mode)", stdout.String())
	}
}

func TestDispatch_Serve_BlocksThenShutsDownWithinBound(t *testing.T) {
	setTestDataDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	var stdout, stderr bytes.Buffer

	done := make(chan error, 1)
	go func() {
		// -bind 127.0.0.1:0 (ephemeral): this test only checks dispatch
		// timing and the shutdown message, never dials the listener, so
		// the actual resolved port doesn't matter — it just must not be
		// the hardcoded default (which could collide with something else
		// already using it).
		done <- Dispatch(ctx, []string{"serve", "-bind", "127.0.0.1:0"}, &stdout, &stderr)
	}()

	select {
	case err := <-done:
		t.Fatalf("Dispatch() returned before shutdown was requested, err = %v", err)
	case <-time.After(200 * time.Millisecond):
		// still blocking on the run loop, as expected — real Boot (lock,
		// DB open, migrate) takes a little longer than the old stub did
	}

	begin := time.Now()
	cancel()

	select {
	case err := <-done:
		elapsed := time.Since(begin)
		if err != nil {
			t.Fatalf("Dispatch() error = %v", err)
		}
		if elapsed > app.ShutdownTimeout {
			t.Fatalf("shutdown took %v, want <= %v", elapsed, app.ShutdownTimeout)
		}
		t.Logf("serve mode shut down in %v (bound %v)", elapsed, app.ShutdownTimeout)
	case <-time.After(app.ShutdownTimeout + time.Second):
		t.Fatal("Dispatch() did not return within bound after cancel")
	}

	if !strings.Contains(stdout.String(), "shutdown complete") {
		t.Fatalf("stdout = %q, want shutdown-complete message", stdout.String())
	}
}

func TestDispatch_Bare_BlocksThenShutsDownWithinBound(t *testing.T) {
	setTestDataDir(t)
	// Bare mode takes no subcommand word, so it has no flags of its own;
	// override the bind via the env var config.Load already supports so
	// this test doesn't fight over the hardcoded default port either.
	t.Setenv("VENOM_BIND", "127.0.0.1:0")

	ctx, cancel := context.WithCancel(context.Background())
	var stdout, stderr bytes.Buffer

	done := make(chan error, 1)
	go func() {
		done <- Dispatch(ctx, nil, &stdout, &stderr)
	}()

	select {
	case err := <-done:
		t.Fatalf("Dispatch() returned before shutdown was requested, err = %v", err)
	case <-time.After(200 * time.Millisecond):
		// still blocking on the run loop, as expected — real Boot (lock,
		// DB open, migrate) takes a little longer than the old stub did
	}

	begin := time.Now()
	cancel()

	select {
	case err := <-done:
		elapsed := time.Since(begin)
		if err != nil {
			t.Fatalf("Dispatch() error = %v", err)
		}
		if elapsed > app.ShutdownTimeout {
			t.Fatalf("shutdown took %v, want <= %v", elapsed, app.ShutdownTimeout)
		}
		t.Logf("bare mode shut down in %v (bound %v)", elapsed, app.ShutdownTimeout)
	case <-time.After(app.ShutdownTimeout + time.Second):
		t.Fatal("Dispatch() did not return within bound after cancel")
	}
}

// TestDispatch_Serve_EndToEnd_BootsHealthAndShutsDownCleanly is the P0
// gate's deliverable proof: `venom serve` genuinely boots the real
// composition root (not the old NoopDrainer stub), a real HTTP client
// gets a 200 from the gated /health handler, and a shutdown signal
// (context cancellation, standing in for real SIGINT/SIGTERM — see
// app.NotifyContext) brings it down cleanly within app.ShutdownTimeout,
// after which the listener is genuinely gone.
func TestDispatch_Serve_EndToEnd_BootsHealthAndShutsDownCleanly(t *testing.T) {
	setTestDataDir(t)

	// A real, pre-reserved free port (not the hardcoded default, and not
	// bare ":0" — this test needs to know the exact address up front so
	// it can dial /health once Boot's listener comes up).
	bind := freeLoopbackAddr(t)

	ctx, cancel := context.WithCancel(context.Background())
	var stdout, stderr bytes.Buffer

	done := make(chan error, 1)
	go func() {
		done <- Dispatch(ctx, []string{"serve", "-bind", bind}, &stdout, &stderr)
	}()

	waitForListener(t, bind)

	req, err := http.NewRequest(http.MethodGet, "http://"+bind+"/health", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Host = bind // exact configured bind — the network gate's Host-allowlist accepts this literally

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /health error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /health status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	begin := time.Now()
	cancel() // simulated shutdown signal

	select {
	case err := <-done:
		elapsed := time.Since(begin)
		if err != nil {
			t.Fatalf("Dispatch() error = %v", err)
		}
		if elapsed > app.ShutdownTimeout {
			t.Fatalf("shutdown took %v, want <= %v", elapsed, app.ShutdownTimeout)
		}
		t.Logf("end-to-end serve shut down in %v (bound %v)", elapsed, app.ShutdownTimeout)
	case <-time.After(app.ShutdownTimeout + 2*time.Second):
		t.Fatal("Dispatch() did not return within bound after cancel")
	}

	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty on a clean run", stderr.String())
	}
	if !strings.Contains(stdout.String(), "shutdown complete") {
		t.Fatalf("stdout = %q, want shutdown-complete message", stdout.String())
	}

	if conn, dialErr := net.DialTimeout("tcp", bind, 200*time.Millisecond); dialErr == nil {
		_ = conn.Close()
		t.Fatalf("dial to %q succeeded after shutdown — listener should be closed", bind)
	}
}
