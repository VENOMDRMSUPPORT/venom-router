# Venom System Tray (P6-FND-001) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn bare `venom` (no args) into a Windows system-tray mode that runs the control-plane server in-process and exposes Open Dashboard / Status / Restart / View Logs / Quit — with a guaranteed bounded process exit.

**Architecture:** Single binary, single process (per `docs/01-architecture.md:39`). A platform-neutral `internal/tray.Controller` owns the exit machine (absolute watchdog armed first, then bounded graceful shutdown, then `os.Exit`) and the in-process `ServerLifecycle` (wrapping `app.Boot`/`Server.Shutdown`). The systray UI is a thin Windows-only adapter; a non-Windows stub falls back to headless. `internal/cli` bare mode calls the new `runTrayLoop`; `venom serve` is untouched.

**Tech Stack:** Go 1.26, `fyne.io/systray v1.12.2` (Windows-only), `golang.org/x/sys/windows`, `log/slog` via `internal/observability`.

**Design source of truth:** `docs/superpowers/specs/2026-07-25-venom-tray-p6-fnd-001-design.md` (r5). Read it before starting.

## Global Constraints

- **Go toolchain 1.26.x**; production builds are `CGO_ENABLED=0` on **both** Windows and Linux. Never break this.
- **One process, one executable** `cmd/venom`. Bare `venom` → tray mode; `venom serve` → headless (behavior unchanged).
- **Code boundaries:** only `internal/tray` (new) and `internal/cli` (modified), plus `go.mod`/`go.sum` and `Taskfile.yml`. **Do not modify `internal/app`** (`Server.Shutdown` stays as-is; the tray layer compensates for its unbounded `db.Close()`/`lock.Release()`).
- **forbidigo** (`.golangci.yml`): no `fmt.Print*`, no `panic`, no `os.Getenv`/`os.LookupEnv` outside `internal/config` & `internal/platform`. Use `internal/observability` for logging, `internal/config` for the bind, `internal/platform` for the data dir. `os.Exit`, `os/exec`, `os.OpenFile` are allowed.
- **English only** in every repo file (code, comments, commit messages).
- **systray isolation:** `fyne.io/systray` may be imported **only** under `//go:build windows`. A `//go:build !windows` stub keeps `go build/vet/test ./...` and `golangci-lint` green on Linux at `CGO_ENABLED=0`.
- **Append-only log** (owner condition 3): open the tray log `O_APPEND|O_CREATE`; never truncate or rotate.
- **Restart safety** (owner condition 2): if the pre-Boot `Shutdown` during Restart times out or errors, **skip the new Boot** and enter `Error`.
- **Tests in the repo** (owner condition 1): the five bounded-exit child-process tests (spec Appendix A.2) live in `internal/tray` and pass under `task gate` on both OSes.
- **Completion gate:** `task gate` green on both OSes + existing headless cli/serve tests green + Windows tray manual-evidence recording — before any "done" claim.

---

### Task 1: `internal/tray` core — ports, Controller, exit machine, bounded-exit tests

The platform-neutral heart. No systray import; runs in CI on both OSes. This task ports spec Appendix A.2 into the repo.

**Files:**
- Create: `internal/tray/tray.go` (ports, types, `Controller`, exit machine)
- Test: `internal/tray/exit_test.go` (child-process bounded-exit tests)
- Test: `internal/tray/controller_test.go` (Restart / Status unit tests)

**Interfaces:**
- Produces:
  - `type ServerLifecycle interface { Boot(context.Context) error; Shutdown(context.Context) error; Healthy(context.Context) bool; DashboardURL() string }`
  - `type Opener interface { Open(target string) error }`
  - `type State int` with `StateStopped, StateRunning, StateError`
  - `type StatusView struct { State State; Detail string }`
  - `type Options struct { ShutdownTimeout, WatchdogMargin time.Duration; Logger *observability.Logger; LogPath string; Exit func(int); UIStop func() }`
  - `func NewController(lc ServerLifecycle, op Opener, opts Options) *Controller`
  - `func (c *Controller) MarkRunning()`, `Status() StatusView`, `Refresh(context.Context)`, `OpenDashboard()`, `OpenLogs()`, `Restart(context.Context)`, `ShutdownAndExit()`, `SetUIStop(func())`
  - Exit codes `ExitClean = 0`, `ExitShutdownHang = 2`; `var ErrShutdownTimedOut`

- [ ] **Step 1: Write the failing bounded-exit child-process tests**

Create `internal/tray/exit_test.go`:

```go
package tray

import (
	"context"
	"errors"
	"os"
	"os/exec"
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
	mode := os.Getenv("SPIKE_CHILD_MODE")
	if mode == "" {
		return
	}
	c := NewController(fakeLC{hangShutdown: mode == "hang-shutdown" || mode == "hang-both"}, noopOpener{}, Options{
		ShutdownTimeout: 300 * time.Millisecond,
		WatchdogMargin:  200 * time.Millisecond,
		Exit:            os.Exit,
		UIStop:          func() {}, // hang-ui/hang-both: UI stuck is irrelevant to exit
	})
	if mode == "hang-quit" {
		c.hangAfterArm = true // test-only: block after the watchdog is armed
	}
	c.ShutdownAndExit()
	select {} // unreachable; ShutdownAndExit os.Exits
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tray/ -run TestExit -v`
Expected: FAIL — `tray.go` does not exist yet (undefined `NewController`, `Controller`, etc.).

- [ ] **Step 3: Implement `internal/tray/tray.go`**

```go
// Package tray implements P6-FND-001 bare-`venom` tray mode: a system-tray UI
// on top of an in-process control-plane server, with a bounded-exit guarantee.
// This file is the platform-neutral core (no systray import); the Windows UI
// lives in tray_windows.go and a headless fallback in tray_other.go.
package tray

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/observability"
)

// Process exit codes for tray mode.
const (
	ExitClean        = 0
	ExitShutdownHang = 2
)

// ErrShutdownTimedOut is returned by the bounded shutdown wrapper when graceful
// shutdown does not complete within ShutdownTimeout.
var ErrShutdownTimedOut = errors.New("tray: graceful shutdown timed out")

// ServerLifecycle is the tray's view of the in-process server.
type ServerLifecycle interface {
	Boot(ctx context.Context) error
	// Shutdown MAY hang past ctx: app.Server.Shutdown bounds only http.Shutdown;
	// db.Close/lock.Release are unbounded (design spec 4.5). The Controller
	// compensates by wrapping this in a bounded select and an absolute watchdog.
	Shutdown(ctx context.Context) error
	Healthy(ctx context.Context) bool
	DashboardURL() string
}

// Opener opens a URL or file path with the OS default handler.
type Opener interface {
	Open(target string) error
}

// State is the coarse tray state shown to the owner.
type State int

const (
	StateStopped State = iota
	StateRunning
	StateError
)

// StatusView is an immutable status snapshot for the UI.
type StatusView struct {
	State  State
	Detail string
}

// Options configures a Controller. Zero values fall back to defaults.
type Options struct {
	ShutdownTimeout time.Duration
	WatchdogMargin  time.Duration
	Logger          *observability.Logger
	LogPath         string
	// Exit terminates the process. Defaults to os.Exit; tests inject a fake.
	Exit func(code int)
	// UIStop asks the native UI loop to stop. Defaults to no-op; set by the
	// Windows adapter to systray.Quit + PostThreadMessageW(WM_QUIT).
	UIStop func()
}

// Controller owns the tray lifecycle and the bounded-exit machine.
type Controller struct {
	lc  ServerLifecycle
	op  Opener
	log *observability.Logger

	shutdownTimeout time.Duration
	watchdogMargin  time.Duration
	logPath         string

	exit func(int)

	mu     sync.Mutex
	state  State
	detail string
	uiStop func()

	shutdownClean int32 // atomic
	exitOnce      sync.Once
	restartMu     sync.Mutex

	// hangAfterArm is test-only: when true, ShutdownAndExit blocks forever right
	// after arming the watchdog, proving the watchdog is independent of cleanup.
	hangAfterArm bool
}

// NewController builds a Controller, filling defaults.
func NewController(lc ServerLifecycle, op Opener, opts Options) *Controller {
	c := &Controller{
		lc:              lc,
		op:              op,
		log:             opts.Logger,
		shutdownTimeout: opts.ShutdownTimeout,
		watchdogMargin:  opts.WatchdogMargin,
		logPath:         opts.LogPath,
		exit:            opts.Exit,
		uiStop:          opts.UIStop,
		state:           StateStopped,
	}
	if c.log == nil {
		c.log = observability.Default()
	}
	if c.shutdownTimeout <= 0 {
		c.shutdownTimeout = 5 * time.Second // mirrors app.ShutdownTimeout
	}
	if c.watchdogMargin <= 0 {
		c.watchdogMargin = 2 * time.Second
	}
	if c.exit == nil {
		c.exit = os.Exit
	}
	if c.uiStop == nil {
		c.uiStop = func() {}
	}
	return c
}

// SetUIStop lets the native UI adapter install its loop-release hook after it
// has captured the message-loop thread id.
func (c *Controller) SetUIStop(fn func()) {
	c.mu.Lock()
	c.uiStop = fn
	c.mu.Unlock()
}

// MarkRunning records that the initial Boot succeeded.
func (c *Controller) MarkRunning() { c.setState(StateRunning, c.lc.DashboardURL()) }

// Status returns the current snapshot.
func (c *Controller) Status() StatusView {
	c.mu.Lock()
	defer c.mu.Unlock()
	return StatusView{State: c.state, Detail: c.detail}
}

func (c *Controller) setState(s State, detail string) {
	c.mu.Lock()
	c.state, c.detail = s, detail
	c.mu.Unlock()
}

// Refresh updates state from a health probe (called by the UI ticker).
func (c *Controller) Refresh(ctx context.Context) {
	if c.lc.Healthy(ctx) {
		c.setState(StateRunning, c.lc.DashboardURL())
		return
	}
	// Do not clobber an explicit Error; otherwise show Stopped.
	if c.Status().State != StateError {
		c.setState(StateStopped, "not responding")
	}
}

// OpenDashboard opens the dashboard URL in the default browser.
func (c *Controller) OpenDashboard() {
	if err := c.op.Open(c.lc.DashboardURL()); err != nil {
		c.log.Error("tray: open dashboard failed", observability.String("err", err.Error()))
	}
}

// OpenLogs opens the append-only log file in the default editor.
func (c *Controller) OpenLogs() {
	if c.logPath == "" {
		return
	}
	if err := c.op.Open(c.logPath); err != nil {
		c.log.Error("tray: open logs failed", observability.String("err", err.Error()))
	}
}

// Restart performs a re-runnable Shutdown-then-Boot. If Shutdown times out or
// errors, the new Boot is SKIPPED and the controller enters Error (owner
// condition 2 / spec 4.2): a dirty shutdown must not be followed by a Boot that
// could race a still-held lock or open DB.
func (c *Controller) Restart(ctx context.Context) {
	c.restartMu.Lock()
	defer c.restartMu.Unlock()

	if err := c.boundedShutdown(); err != nil {
		c.setState(StateError, "restart aborted: "+err.Error())
		c.log.Error("tray: restart aborted; shutdown unclean, not re-booting",
			observability.String("err", err.Error()))
		return
	}
	if err := c.lc.Boot(ctx); err != nil {
		c.setState(StateError, "boot failed: "+err.Error())
		c.log.Error("tray: restart boot failed", observability.String("err", err.Error()))
		return
	}
	c.setState(StateRunning, c.lc.DashboardURL())
}

// boundedShutdown runs ServerLifecycle.Shutdown in a goroutine and returns when
// it finishes OR shutdownTimeout elapses (tolerating a leaked goroutine if it
// hangs).
func (c *Controller) boundedShutdown() error {
	sctx, cancel := context.WithTimeout(context.Background(), c.shutdownTimeout)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.lc.Shutdown(sctx) }()
	select {
	case err := <-done:
		return err
	case <-sctx.Done():
		return ErrShutdownTimedOut
	}
}

// ShutdownAndExit is the single teardown path (menu Quit / external signal /
// onExit all cancel the root ctx, whose watcher calls this). It NEVER returns.
//
// Order (spec 4.5):
//  1. Arm the absolute watchdog BEFORE any cleanup — depends on nothing that can
//     hang; os.Exit -> ExitProcess ends even a goroutine stuck in db.Close.
//  2. Bounded graceful shutdown.
//  3. Best-effort native loop release.
//  4. Exit: 0 if shutdown was clean, else ExitShutdownHang.
func (c *Controller) ShutdownAndExit() {
	go func() {
		time.Sleep(c.shutdownTimeout + c.watchdogMargin)
		if atomic.LoadInt32(&c.shutdownClean) == 1 {
			c.doExit(ExitClean)
			return
		}
		c.doExit(ExitShutdownHang)
	}()

	if c.hangAfterArm { // test-only
		select {}
	}

	err := c.boundedShutdown()
	if err == nil {
		atomic.StoreInt32(&c.shutdownClean, 1)
		c.setState(StateStopped, "")
	} else {
		c.log.Error("tray: graceful shutdown did not complete",
			observability.String("err", err.Error()))
	}

	c.mu.Lock()
	stop := c.uiStop
	c.mu.Unlock()
	stop()

	if err != nil {
		c.doExit(ExitShutdownHang)
		return
	}
	c.doExit(ExitClean)
}

func (c *Controller) doExit(code int) { c.exitOnce.Do(func() { c.exit(code) }) }
```

- [ ] **Step 4: Run the bounded-exit tests to verify they pass**

Run: `go test ./internal/tray/ -run TestExit -v`
Expected: PASS — 5 tests; hang-shutdown/hang-both exit 2 ~300ms, hang-quit exit 2 ~500ms, normal/hang-ui exit 0 promptly.

- [ ] **Step 5: Write the Restart unit test**

Create `internal/tray/controller_test.go`:

```go
package tray

import (
	"context"
	"errors"
	"testing"
	"time"
)

type scriptLC struct {
	shutdownErr   error
	shutdownHang  bool
	bootErr       error
	bootCalled    int
}

func (s *scriptLC) Boot(context.Context) error { s.bootCalled++; return s.bootErr }
func (s *scriptLC) Shutdown(context.Context) error {
	if s.shutdownHang {
		select {}
	}
	return s.shutdownErr
}
func (s *scriptLC) Healthy(context.Context) bool { return true }
func (s *scriptLC) DashboardURL() string         { return "http://127.0.0.1:8081/" }

func newTestController(lc ServerLifecycle) *Controller {
	return NewController(lc, noopOpener{}, Options{
		ShutdownTimeout: 100 * time.Millisecond,
		WatchdogMargin:  50 * time.Millisecond,
		Exit:            func(int) {}, // never kill the test process
		UIStop:          func() {},
	})
}

func TestRestart_CleanShutdown_RebootsAndRuns(t *testing.T) {
	lc := &scriptLC{}
	c := newTestController(lc)
	c.Restart(context.Background())
	if lc.bootCalled != 1 {
		t.Fatalf("bootCalled=%d want 1", lc.bootCalled)
	}
	if c.Status().State != StateRunning {
		t.Fatalf("state=%v want Running", c.Status().State)
	}
}

func TestRestart_ShutdownError_SkipsBoot_EntersError(t *testing.T) {
	lc := &scriptLC{shutdownErr: errors.New("db close failed")}
	c := newTestController(lc)
	c.Restart(context.Background())
	if lc.bootCalled != 0 {
		t.Fatalf("bootCalled=%d want 0 (dirty shutdown must skip Boot)", lc.bootCalled)
	}
	if c.Status().State != StateError {
		t.Fatalf("state=%v want Error", c.Status().State)
	}
}

func TestRestart_ShutdownHang_TimesOut_SkipsBoot(t *testing.T) {
	lc := &scriptLC{shutdownHang: true}
	c := newTestController(lc)
	c.Restart(context.Background())
	if lc.bootCalled != 0 {
		t.Fatalf("bootCalled=%d want 0 (timeout must skip Boot)", lc.bootCalled)
	}
	if c.Status().State != StateError {
		t.Fatalf("state=%v want Error", c.Status().State)
	}
}
```

- [ ] **Step 6: Run the Restart tests**

Run: `go test ./internal/tray/ -run TestRestart -v`
Expected: PASS — clean reboots; error and hang both skip Boot and enter Error.

- [ ] **Step 7: Commit**

```bash
git add internal/tray/tray.go internal/tray/exit_test.go internal/tray/controller_test.go
git commit -m "feat(tray): P6-FND-001 controller + bounded-exit machine (core, tests)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Real `ServerLifecycle` adapter

Wrap `app.Boot`/`Server.Shutdown`, a loopback `/health` probe, and the dashboard URL.

**Files:**
- Create: `internal/tray/lifecycle.go`
- Test: `internal/tray/lifecycle_test.go`

**Interfaces:**
- Consumes: `tray.ServerLifecycle` (Task 1); `app.Boot`, `app.Server`, `app.BootConfig`, `app.ShutdownTimeout` (`internal/app`); `config.Config`.
- Produces: `func NewServerLifecycle(bind string, logger *observability.Logger) *ServerAdapter` implementing `ServerLifecycle`.

- [ ] **Step 1: Write the failing test (DashboardURL + Healthy probe)**

```go
package tray

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServerAdapter_DashboardURL(t *testing.T) {
	a := NewServerLifecycle("127.0.0.1:8081", nil)
	if got := a.DashboardURL(); got != "http://127.0.0.1:8081/" {
		t.Fatalf("DashboardURL=%q", got)
	}
}

func TestServerAdapter_Healthy_ProbesHealthEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	bind := strings.TrimPrefix(srv.URL, "http://")
	a := NewServerLifecycle(bind, nil)
	if !a.Healthy(context.Background()) {
		t.Fatalf("Healthy=false, want true against a live /health")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tray/ -run TestServerAdapter -v`
Expected: FAIL — `NewServerLifecycle` undefined.

- [ ] **Step 3: Implement `internal/tray/lifecycle.go`**

```go
package tray

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/app"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/observability"
)

// ServerAdapter is the production ServerLifecycle backed by internal/app.
type ServerAdapter struct {
	bind   string
	logger *observability.Logger

	mu  sync.Mutex
	srv *app.Server
}

// NewServerLifecycle returns a ServerAdapter for the given loopback bind.
func NewServerLifecycle(bind string, logger *observability.Logger) *ServerAdapter {
	return &ServerAdapter{bind: bind, logger: logger}
}

func (a *ServerAdapter) Boot(ctx context.Context) error {
	srv, err := app.Boot(ctx, app.BootConfig{Bind: a.bind, Logger: a.logger})
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.srv = srv
	a.mu.Unlock()
	return nil
}

func (a *ServerAdapter) Shutdown(ctx context.Context) error {
	a.mu.Lock()
	srv := a.srv
	a.srv = nil
	a.mu.Unlock()
	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}

// Healthy probes GET http://<bind>/health with a short timeout. The Host header
// is the bind, which the control plane's network gate accepts.
func (a *ServerAdapter) Healthy(ctx context.Context) bool {
	cctx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, "http://"+a.bind+"/health", nil)
	if err != nil {
		return false
	}
	client := &http.Client{Timeout: 1 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode == http.StatusOK
}

func (a *ServerAdapter) DashboardURL() string {
	return "http://" + a.bind + "/"
}

// compile-time guards
var (
	_ ServerLifecycle = (*ServerAdapter)(nil)
	_ = net.JoinHostPort
)
```

Note: remove the unused `net` import if the guard line is dropped; keep imports clean for `goimports`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tray/ -run TestServerAdapter -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tray/lifecycle.go internal/tray/lifecycle_test.go
git commit -m "feat(tray): P6-FND-001 app-backed ServerLifecycle adapter

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Non-Windows stub (headless fallback)

Keeps `go build/test ./...` green on Linux at `CGO_ENABLED=0` and satisfies "tray failure falls back to headless".

**Files:**
- Create: `internal/tray/tray_other.go` (`//go:build !windows`)
- Test: `internal/tray/tray_other_test.go` (`//go:build !windows`)

**Interfaces:**
- Produces: `func RunNativeUI(ctx context.Context, cancel context.CancelFunc, c *Controller) error`; `func NewOpener() Opener` (non-Windows: returns an opener that reports unsupported).

- [ ] **Step 1: Write the failing test**

```go
//go:build !windows

package tray

import (
	"context"
	"testing"
	"time"
)

func TestRunNativeUI_Other_BlocksUntilCtxThenReturns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunNativeUI(ctx, cancel, NewController(fakeLC{}, noopOpener{}, Options{Exit: func(int) {}})) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunNativeUI did not return after ctx cancel")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tray/ -run TestRunNativeUI_Other -v`
Expected: FAIL — `RunNativeUI` / `NewOpener` undefined for this build.

- [ ] **Step 3: Implement `internal/tray/tray_other.go`**

```go
//go:build !windows

package tray

import (
	"context"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/observability"
)

// RunNativeUI on non-Windows has no tray: it logs and blocks until ctx is
// cancelled (headless fallback). The server booted by the caller keeps running.
func RunNativeUI(ctx context.Context, _ context.CancelFunc, c *Controller) error {
	c.log.Info("tray: system tray unavailable on this platform; running headless",
		observability.String("mode", "headless-fallback"))
	<-ctx.Done()
	return nil
}

// NewOpener on non-Windows reports unsupported (dashboard/log opening is a
// desktop affordance not needed on a headless host).
func NewOpener() Opener { return unsupportedOpener{} }

type unsupportedOpener struct{}

func (unsupportedOpener) Open(string) error { return nil }
```

- [ ] **Step 4: Run tests (on Linux/WSL if available; otherwise via cross-build check)**

Run: `go test ./internal/tray/ -run TestRunNativeUI_Other -v` (on a non-Windows host)
Also verify Linux build isolation: `GOOS=linux CGO_ENABLED=0 go build ./...`
Expected: PASS / build succeeds with no systray in the graph.

- [ ] **Step 5: Commit**

```bash
git add internal/tray/tray_other.go internal/tray/tray_other_test.go
git commit -m "feat(tray): P6-FND-001 non-Windows headless-fallback stub

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Windows systray adapter

The only file importing `fyne.io/systray`. Adds the dependency to `go.mod`. Console-hide is ownership-checked; loop release uses WM_QUIT to the captured thread id.

**Files:**
- Create: `internal/tray/tray_windows.go` (`//go:build windows`)
- Create: `internal/tray/winapi_windows.go` (`//go:build windows`) — LazyDLL procs
- Create: `internal/tray/icon_windows.go` (`//go:build windows`) — `//go:embed`
- Create: `internal/tray/assets/venom.ico` (placeholder brand icon; reuse a `Design_System/` asset if suitable — spec 10.3)
- Test: `internal/tray/winapi_windows_test.go` (`//go:build windows`) — ownership-check logic
- Modify: `go.mod`, `go.sum` (adds `fyne.io/systray`)

**Interfaces:**
- Consumes: `Controller` (Task 1), `windows.GetCurrentThreadId`, LazyDLL procs.
- Produces: `func RunNativeUI(ctx context.Context, cancel context.CancelFunc, c *Controller) error`; `func NewOpener() Opener` (Windows ShellExecute).

- [ ] **Step 1: Add the dependency**

Run:
```bash
go get fyne.io/systray@v1.12.2
```
Expected: `go.mod` gains `require fyne.io/systray v1.12.2`.

- [ ] **Step 2: Write the failing ownership-check test**

Create `internal/tray/winapi_windows_test.go`:

```go
//go:build windows

package tray

import "testing"

// The test binary is normally launched by `go test` sharing the runner's
// console, so process count is > 1 => we must NOT hide.
func TestShouldHideConsole_SharedConsole_ReturnsFalse(t *testing.T) {
	if shouldHideConsole() {
		t.Fatal("shouldHideConsole()=true while sharing the test runner console; must be false")
	}
}
```

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./internal/tray/ -run TestShouldHideConsole -v` (on Windows)
Expected: FAIL — `shouldHideConsole` undefined.

- [ ] **Step 4: Implement `internal/tray/winapi_windows.go`**

```go
//go:build windows

package tray

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32               = windows.NewLazySystemDLL("kernel32.dll")
	user32                 = windows.NewLazySystemDLL("user32.dll")
	shell32                = windows.NewLazySystemDLL("shell32.dll")
	procGetConsoleWindow   = kernel32.NewProc("GetConsoleWindow")
	procGetConsoleProcList = kernel32.NewProc("GetConsoleProcessList")
	procShowWindow         = user32.NewProc("ShowWindow")
	procPostThreadMessageW = user32.NewProc("PostThreadMessageW")
	procShellExecuteW      = shell32.NewProc("ShellExecuteW")
)

const (
	swHide  = 0
	wmQuit  = 0x0012
)

// shouldHideConsole reports whether venom owns its console exclusively (the
// Explorer double-click case, where Windows allocated a private console). When
// more than one process is attached (launched from an existing PowerShell/cmd),
// hiding would hide the user's terminal, so return false. (spec 5)
func shouldHideConsole() bool {
	hwnd, _, _ := procGetConsoleWindow.Call()
	if hwnd == 0 {
		return false // no console at all
	}
	var pids [4]uint32
	n, _, _ := procGetConsoleProcList.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	return n == 1
}

// hideConsoleIfOwned hides the console window only when solely owned.
func hideConsoleIfOwned() {
	if !shouldHideConsole() {
		return
	}
	hwnd, _, _ := procGetConsoleWindow.Call()
	if hwnd != 0 {
		_, _, _ = procShowWindow.Call(hwnd, uintptr(swHide))
	}
}

// postQuit posts WM_QUIT to the message-loop thread id, releasing systray.Run.
func postQuit(tid uint32) {
	_, _, _ = procPostThreadMessageW.Call(uintptr(tid), uintptr(wmQuit), 0, 0)
}

// shellOpen opens a URL or file with the OS default handler (no console flash).
func shellOpen(target string) error {
	verb, _ := syscall.UTF16PtrFromString("open")
	file, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	// ShellExecuteW(hwnd=0, "open", target, params=nil, dir=nil, SW_SHOWNORMAL=1)
	r, _, callErr := procShellExecuteW.Call(0, uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)), 0, 0, 1)
	if r <= 32 { // ShellExecute returns >32 on success
		return callErr
	}
	return nil
}
```

- [ ] **Step 5: Run the ownership test to verify it passes**

Run: `go test ./internal/tray/ -run TestShouldHideConsole -v` (on Windows)
Expected: PASS (test runner shares a console → count > 1 → false).

- [ ] **Step 6: Implement `internal/tray/icon_windows.go`**

```go
//go:build windows

package tray

import _ "embed"

//go:embed assets/venom.ico
var trayIcon []byte
```

Provide `internal/tray/assets/venom.ico` (a real `.ico`; reuse a `Design_System/` brand asset if one fits, else a simple placeholder icon). This is an asset-provisioning step, not code.

- [ ] **Step 7: Implement `internal/tray/tray_windows.go`**

```go
//go:build windows

package tray

import (
	"context"
	"runtime"
	"time"

	"fyne.io/systray"
	"golang.org/x/sys/windows"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/observability"
)

// NewOpener returns the Windows ShellExecute-backed opener.
func NewOpener() Opener { return winOpener{} }

type winOpener struct{}

func (winOpener) Open(target string) error { return shellOpen(target) }

// RunNativeUI runs the systray event loop on the current (main) goroutine. The
// caller must have already booted the server and started the ctx.Done() watcher
// that calls Controller.ShutdownAndExit. This function locks the OS thread,
// captures its id for WM_QUIT, installs the UIStop hook, hides the console when
// solely owned, and blocks in systray.Run until Quit/onExit cancel ctx.
func RunNativeUI(ctx context.Context, cancel context.CancelFunc, c *Controller) error {
	runtime.LockOSThread()
	tid := windows.GetCurrentThreadId()
	c.SetUIStop(func() {
		systray.Quit()
		postQuit(tid)
	})
	hideConsoleIfOwned()

	onReady := func() {
		systray.SetIcon(trayIcon)
		systray.SetTitle("Venom Router")
		systray.SetTooltip("Venom Router")

		mStatus := systray.AddMenuItem("Status: starting…", "")
		mStatus.Disable()
		systray.AddSeparator()
		mOpen := systray.AddMenuItem("Open Dashboard", "Open the dashboard in your browser")
		mRestart := systray.AddMenuItem("Restart", "Restart the server")
		mLogs := systray.AddMenuItem("View Logs", "Open the log file")
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("Quit", "Stop the server and exit")

		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-mOpen.ClickedCh:
					c.OpenDashboard()
				case <-mRestart.ClickedCh:
					go c.Restart(ctx) // don't block the UI goroutine
				case <-mLogs.ClickedCh:
					c.OpenLogs()
				case <-mQuit.ClickedCh:
					cancel() // funnel into the single ctx.Done() watcher
					return
				case <-ticker.C:
					c.Refresh(ctx)
					mStatus.SetTitle(statusTitle(c.Status()))
					systray.SetTooltip(statusTitle(c.Status()))
				}
			}
		}()
	}

	onExit := func() { cancel() }

	c.log.Info("tray: starting system tray", observability.String("mode", "tray"))
	systray.Run(onReady, onExit)

	// If Run returned but ctx was never cancelled (init failure that returned),
	// fall back to headless: the server is still up.
	select {
	case <-ctx.Done():
	default:
		c.log.Warn("tray: systray loop ended without init; continuing headless")
		<-ctx.Done()
	}
	return nil
}

func statusTitle(s StatusView) string {
	switch s.State {
	case StateRunning:
		return "Status: running — " + s.Detail
	case StateError:
		return "Status: error (see Logs)"
	default:
		return "Status: stopped"
	}
}
```

- [ ] **Step 8: Build for Windows and vet**

Run:
```bash
go build ./... && go vet ./...
GOOS=linux CGO_ENABLED=0 go build ./...   # prove Linux still builds with no systray
```
Expected: both succeed.

- [ ] **Step 9: Commit**

```bash
git add go.mod go.sum internal/tray/tray_windows.go internal/tray/winapi_windows.go internal/tray/icon_windows.go internal/tray/winapi_windows_test.go internal/tray/assets/venom.ico
git commit -m "feat(tray): P6-FND-001 Windows systray adapter (menu, console-hide, WM_QUIT)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Wire `internal/cli` bare mode to tray

Bare `venom` → `runTrayLoop`; `venom serve` unchanged. Append-only file logger.

**Files:**
- Modify: `internal/cli/cli.go` (`case "":` → `runTrayLoop`; add `runTrayLoop`, `openTrayLog`)
- Modify: `internal/cli/cli_test.go`

**Interfaces:**
- Consumes: `tray.NewController`, `tray.NewServerLifecycle`, `tray.NewOpener`, `tray.RunNativeUI`, `tray.Options`; `config.Load`; `platform.EnsureDataDir`; `observability.New`; `app.ShutdownTimeout`.

- [ ] **Step 1: Write the failing test (bare mode dispatches to tray loop, not serve)**

Add to `internal/cli/cli_test.go`. Because tray mode on a headless CI Linux box behaves like headless serve (blocks until ctx), assert that bare mode boots and shuts down within bound when ctx is cancelled — the same contract the existing bare test used, now via `runTrayLoop`:

```go
func TestDispatch_BareMode_BootsAndShutsDown(t *testing.T) {
	bind := freeLoopbackAddr(t) // existing helper
	t.Setenv("VENOM_BIND", bind)
	ctx, cancel := context.WithCancel(context.Background())
	var stdout, stderr strings.Builder
	done := make(chan error, 1)
	go func() { done <- Dispatch(ctx, nil, &stdout, &stderr) }()
	time.Sleep(300 * time.Millisecond) // allow Boot + listener
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("bare/tray mode did not shut down within bound")
	}
}
```

Note: on Windows CI this exercises the systray path's headless fallback; on Linux it exercises `tray_other.go`. Both must return after cancel.

- [ ] **Step 2: Run to verify it fails / behaves wrong**

Run: `go test ./internal/cli/ -run TestDispatch_BareMode -v`
Expected: FAIL or hang until timeout (bare mode still calls the old `runServeLoop`, or the new path is not wired).

- [ ] **Step 3: Implement `runTrayLoop` and rewire bare mode in `internal/cli/cli.go`**

Change the dispatch:

```go
	case "":
		// Bare mode is tray mode (P6-FND-001): tray UI on Windows, headless
		// fallback elsewhere. The server runs in-process; see internal/tray.
		return runTrayLoop(ctx, stdout)
```

Add:

```go
// runTrayLoop is bare-`venom` tray mode. It boots the in-process server with an
// append-only file logger, starts the single ctx-cancel teardown watcher, then
// hands the main goroutine to the native tray UI (Windows) or a headless block
// (elsewhere). Guaranteed bounded exit is owned by tray.Controller.
func runTrayLoop(parent context.Context, stdout io.Writer) error {
	cfg, err := config.Load(nil)
	if err != nil {
		return fmt.Errorf("cli: load config: %w", err)
	}

	logger, logPath, closeLog, err := openTrayLog()
	if err != nil {
		return fmt.Errorf("cli: open tray log: %w", err)
	}
	defer closeLog()

	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	lc := tray.NewServerLifecycle(cfg.Bind, logger)
	ctrl := tray.NewController(lc, tray.NewOpener(), tray.Options{
		ShutdownTimeout: app.ShutdownTimeout,
		Logger:          logger,
		LogPath:         logPath,
	})

	if err := lc.Boot(ctx); err != nil {
		return fmt.Errorf("cli: boot: %w", err)
	}
	ctrl.MarkRunning()
	_, _ = fmt.Fprintf(stdout, "venom: tray mode serving on %s\n", cfg.Bind)

	// Single teardown path: any ctx cancel (signal or Quit menu) runs the
	// watchdog-first ShutdownAndExit.
	go func() {
		<-ctx.Done()
		ctrl.ShutdownAndExit()
	}()

	return tray.RunNativeUI(ctx, cancel, ctrl)
}

// openTrayLog opens <dataDir>/logs/venom.log APPEND-ONLY (never truncated or
// rotated — owner condition 3 / spec 4.3) and returns a JSON logger over it.
func openTrayLog() (*observability.Logger, string, func(), error) {
	dataDir, err := platform.EnsureDataDir()
	if err != nil {
		return nil, "", func() {}, err
	}
	logsDir := filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(logsDir, 0o700); err != nil {
		return nil, "", func() {}, err
	}
	logPath := filepath.Join(logsDir, "venom.log")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, "", func() {}, err
	}
	logger := observability.New(slog.NewJSONHandler(f, nil))
	return logger, logPath, func() { _ = f.Close() }, nil
}
```

Add imports to `internal/cli/cli.go`: `io` (already), `os`, `path/filepath`, `log/slog`, `github.com/VENOMDRMSUPPORT/venom-router/internal/observability`, `github.com/VENOMDRMSUPPORT/venom-router/internal/platform`, `github.com/VENOMDRMSUPPORT/venom-router/internal/tray`. Note `runServeLoop` stays for `case "serve":`.

- [ ] **Step 4: Run the bare-mode test to verify it passes**

Run: `go test ./internal/cli/ -run TestDispatch_BareMode -v`
Expected: PASS — boots and returns within bound after cancel.

- [ ] **Step 5: Run the full cli suite (serve unchanged)**

Run: `go test ./internal/cli/ -v`
Expected: PASS — including the existing `serve` and end-to-end serve tests.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/cli.go internal/cli/cli_test.go
git commit -m "feat(cli): P6-FND-001 wire bare venom to tray mode (append-only log)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: One-command bundle (Taskfile convenience)

Directly answers "commands are complicated": one task produces a double-clickable `venom.exe` with the dashboard embedded. Touches only `Taskfile.yml`.

**Files:**
- Modify: `Taskfile.yml`

- [ ] **Step 1: Add the `bundle` task**

Add under `tasks:` in `Taskfile.yml`:

```yaml
  bundle:
    desc: >
      Build the single double-clickable venom.exe with the dashboard embedded:
      dashboard:build-embed, then `go build`. Run it and double-click the exe —
      bare `venom` starts the server + system tray (P6-FND-001).
    cmds:
      - task: dashboard:build-embed
      - go build -o dist/venom.exe ./cmd/venom
      - 'echo "built dist/venom.exe — double-click it to start Venom in the tray"'
```

- [ ] **Step 2: Verify it builds**

Run: `task bundle` (on Windows, with Node deps installed)
Expected: `dist/venom.exe` produced.

- [ ] **Step 3: Commit**

```bash
git add Taskfile.yml
git commit -m "build: P6-FND-001 add `bundle` task for a double-clickable venom.exe

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 7: Completion gate (owner condition + DoD)

No new code. Prove the whole thing per spec §11 before claiming done.

- [ ] **Step 1: Run the static-invariants gate on both OSes**

Run: `task gate` (Windows) and `task gate` (Linux/WSL or CI).
Expected: green — gofmt, goimports, vet, golangci-lint (forbidigo clean: no new `fmt.Print*`/`panic`/`os.Getenv`), and `go test ./...` (incl. the 5 bounded-exit tests + Restart tests + layering test).

- [ ] **Step 2: Confirm Linux CGO isolation**

Run: `GOOS=linux CGO_ENABLED=0 go build ./...`
Expected: builds with no systray in the dependency graph.

- [ ] **Step 3: Windows tray manual-evidence recording**

On Windows, `task bundle` then double-click `dist/venom.exe`. Record (screen capture) proving: tray icon appears; Open Dashboard opens `http://127.0.0.1:8081/`; Status shows running; Restart works; View Logs opens the append-only log; Quit exits within a few seconds. Save the recording as the `P6-FND-001` evidence.

- [ ] **Step 4: Confirm headless unaffected**

Run: `venom serve` in a terminal; confirm it still logs to stderr and shuts down on Ctrl+C within the bound.

- [ ] **Step 5: Hand back to the governor**

Do NOT update the roadmap tracker. Report the gate output, the Linux build result, and the manual-evidence recording to the governor for verified approval; the tracker STATUS for `P6-FND-001` moves only after that review.

---

## Self-Review

**1. Spec coverage:**
- §2 single-process / rejected supervisor → Tasks 1–5 (in-process lifecycle). ✓
- §3 menu Open Dashboard/Status/Restart/View Logs/Quit → Task 4 onReady. ✓
- §4.1 dispatch change → Task 5. ✓
- §4.2 Controller (Quit vs Restart; Restart skips Boot on dirty shutdown) → Task 1 (`Restart`, tests). ✓
- §4.3 append-only log → Task 5 `openTrayLog`. ✓
- §4.4 icon embed → Task 4. ✓
- §4.5 watchdog-first bounded exit → Task 1 `ShutdownAndExit` + exit tests. ✓
- §5 console-hide (GetConsoleProcessList), LockOSThread+tid, WM_QUIT, threading → Task 4. ✓
- §6 behaviors → Task 4 onReady wiring. ✓
- §7 fallback structural + bounded exit → Tasks 1, 3, 4. ✓
- §8 gate/forbidigo/layering/tests → Task 7. ✓
- §9 bundle task → Task 6. ✓
- §11 owner conditions (tests in repo / restart-no-boot / append-only) + completion gate → Tasks 1, 5, 7. ✓

**2. Placeholder scan:** No TBD/TODO. The only non-code deferral is the `.ico` asset (Task 4 Step 6), which is an explicit asset-provisioning step, flagged in spec §10.3.

**3. Type consistency:** `ServerLifecycle`, `Opener`, `Controller`, `Options{ShutdownTimeout,WatchdogMargin,Logger,LogPath,Exit,UIStop}`, `RunNativeUI(ctx, cancel, *Controller)`, `NewOpener()`, `NewServerLifecycle(bind, logger)`, `NewController(lc, op, opts)`, `ShutdownAndExit()`, `MarkRunning()`, `Refresh(ctx)`, `Restart(ctx)`, `SetUIStop(func())`, `ExitClean=0`/`ExitShutdownHang=2` — used identically across Tasks 1–5. ✓
