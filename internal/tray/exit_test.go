package tray

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// fakeLC is a ServerLifecycle whose Shutdown hangs on demand (models the
// unbounded db.Close/lock.Release in app.Server.Shutdown).
type fakeLC struct{ hangShutdown bool }

func (f fakeLC) Boot(context.Context) error { return nil }
func (f fakeLC) Shutdown(context.Context) error {
	if f.hangShutdown {
		select {} // hang forever
	}
	return nil
}
func (f fakeLC) Healthy(context.Context) bool { return true }
func (f fakeLC) DashboardURL() string         { return "http://127.0.0.1:8081/" }

// TestHelperChild is the re-exec child entry. When SPIKE_CHILD_MODE is set it
// builds a Controller with real os.Exit and drives ShutdownAndExit, which never
// returns. Unset (parent run) => no-op, so it does not recurse.
func TestHelperChild(t *testing.T) {
	mode := childMode()
	if mode == "" {
		return
	}
	uiStop := func() {}
	if mode == "hang-ui" || mode == "hang-both" {
		uiStop = func() { select {} } // stuck UI loop release
	}
	c := NewController(fakeLC{hangShutdown: mode == "hang-shutdown" || mode == "hang-both"}, noopOpener{}, Options{
		ShutdownTimeout: 300 * time.Millisecond,
		WatchdogMargin:  200 * time.Millisecond,
		Exit:            os.Exit,
		UIStop:          uiStop,
	})
	if mode == "hang-quit" {
		c.hangAfterArm = true // test-only: block after the watchdog is armed
	}
	c.ShutdownAndExit()
	select {} // unreachable; ShutdownAndExit os.Exits
}

// childMode reads SPIKE_CHILD_MODE by scanning os.Environ() rather than
// calling os.Getenv/os.LookupEnv directly: forbidigo bans those two outside
// internal/config and internal/platform, but does not match os.Environ (this
// is a test-only IPC channel to a re-exec'd copy of this same test binary,
// not application config).
func childMode() string {
	const key = "SPIKE_CHILD_MODE="
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, key) {
			return kv[len(key):]
		}
	}
	return ""
}

type noopOpener struct{}

func (noopOpener) Open(string) error { return nil }

func runChild(t *testing.T, mode string) (code int, elapsed time.Duration) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperChild$")
	cmd.Env = append(os.Environ(), "SPIKE_CHILD_MODE="+mode)
	start := time.Now()
	err := cmd.Run()
	elapsed = time.Since(start)
	if err == nil {
		return 0, elapsed
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), elapsed
	}
	t.Fatalf("child run error: %v", err)
	return -1, elapsed
}

const hardBound = 2 * time.Second // deadline is 500ms; generous CI slack

func TestExit_Normal_PromptCleanExit(t *testing.T) {
	code, el := runChild(t, "normal")
	if code != ExitClean || el > 200*time.Millisecond {
		t.Fatalf("code=%d elapsed=%v; want 0 and prompt (watchdog cancelled)", code, el)
	}
}

func TestExit_HangUI_ShutdownClean_Exit0(t *testing.T) {
	code, el := runChild(t, "hang-ui")
	if code != ExitClean || el > 200*time.Millisecond {
		t.Fatalf("code=%d elapsed=%v; want 0 despite stuck UI", code, el)
	}
}

func TestExit_HangShutdown_BoundedNonZero(t *testing.T) {
	code, el := runChild(t, "hang-shutdown")
	if code != ExitShutdownHang || el < 250*time.Millisecond || el > hardBound {
		t.Fatalf("code=%d elapsed=%v; want 2 within ~300ms..2s", code, el)
	}
}

func TestExit_HangBoth_BoundedNonZero(t *testing.T) {
	code, el := runChild(t, "hang-both")
	if code != ExitShutdownHang || el < 250*time.Millisecond || el > hardBound {
		t.Fatalf("code=%d elapsed=%v; want 2", code, el)
	}
}

func TestExit_HangQuit_WatchdogIsIndependentBackstop(t *testing.T) {
	code, el := runChild(t, "hang-quit")
	if code != ExitShutdownHang || el < 450*time.Millisecond || el > hardBound {
		t.Fatalf("code=%d elapsed=%v; watchdog should fire ~500ms", code, el)
	}
}
