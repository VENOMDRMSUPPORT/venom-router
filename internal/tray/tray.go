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
	lifecycleMu   sync.Mutex

	// preShutdown, if set, runs once inside ShutdownAndExit AFTER the absolute
	// watchdog is armed and BEFORE the bounded prod shutdown. The tray wires it
	// to dev.Stop so an active dev session is torn down gracefully (backend
	// fully stopped, lock freed) rather than left to the OS's kill-on-exit of
	// the dev Job Object. Watchdog-armed-first means a hung hook still can't
	// block the bounded exit.
	preShutdown func()

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

// SetPreShutdown installs the graceful pre-shutdown hook (the tray wires it to
// dev.Stop). Called once inside ShutdownAndExit, after the watchdog is armed.
func (c *Controller) SetPreShutdown(fn func()) {
	c.mu.Lock()
	c.preShutdown = fn
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

// OpenURL opens an arbitrary URL with the OS opener (used by the menu's
// dashboard entries).
func (c *Controller) OpenURL(url string) {
	if err := c.op.Open(url); err != nil {
		c.log.Error("tray: open url failed", observability.String("err", err.Error()))
	}
}

// OpenDashboard opens the production dashboard URL in the default browser.
func (c *Controller) OpenDashboard() { c.OpenURL(c.lc.DashboardURL()) }

// OpenLogs opens the append-only log file in the default editor.
func (c *Controller) OpenLogs() {
	if c.logPath == "" {
		return
	}
	if err := c.op.Open(c.logPath); err != nil {
		c.log.Error("tray: open logs failed", observability.String("err", err.Error()))
	}
}

// Stop performs a re-runnable bounded ServerLifecycle.Shutdown. Unlike
// ShutdownAndExit (the sync.Once-guarded Quit path), it NEVER terminates the
// process — it only brings the in-process server down. Success -> StateStopped;
// a timeout/error -> StateError. No-op if the state is already StateStopped.
// Server.Shutdown releases the single-instance lock, so after Stop the lock is
// free; a following Start re-acquires it via Boot.
func (c *Controller) Stop() {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()

	if c.Status().State == StateStopped {
		return
	}
	c.stopLocked()
}

// Start boots the server via ServerLifecycle.Boot if not already Running.
// Success -> StateRunning; failure -> StateError. No-op if already Running.
func (c *Controller) Start(ctx context.Context) {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()

	c.startLocked(ctx)
}

// Restart performs a re-runnable Shutdown-then-Boot. If Shutdown times out or
// errors, the new Boot is SKIPPED and the controller enters Error (owner
// condition 2 / spec 4.2): a dirty shutdown must not be followed by a Boot that
// could race a still-held lock or open DB.
//
// This shares the stopLocked/startLocked cores with the public Stop/Start, but
// deliberately does NOT go through Stop's "no-op if already Stopped" guard: the
// coarse State defaults to StateStopped before the very first Boot ever runs,
// so a guard keyed on that value would wrongly skip the shutdown attempt (and
// the error/timeout it surfaces) on a controller that has never booted.
// Restart's contract has always been "always attempt the shutdown, then only
// boot if it came out clean" — unconditional on the prior coarse state.
func (c *Controller) Restart(ctx context.Context) {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()

	c.stopLocked()
	if c.Status().State != StateError {
		c.startLocked(ctx)
	}
}

// stopLocked runs the bounded shutdown unconditionally. Caller must hold
// lifecycleMu.
func (c *Controller) stopLocked() {
	if err := c.boundedShutdown(); err != nil {
		c.setState(StateError, "stop aborted: "+err.Error())
		c.log.Error("tray: shutdown did not complete cleanly, not re-booting",
			observability.String("err", err.Error()))
		return
	}
	c.setState(StateStopped, "")
}

// startLocked boots the server if not already Running. Caller must hold
// lifecycleMu.
func (c *Controller) startLocked(ctx context.Context) {
	if c.Status().State == StateRunning {
		return
	}
	if err := c.lc.Boot(ctx); err != nil {
		c.setState(StateError, "boot failed: "+err.Error())
		c.log.Error("tray: boot failed", observability.String("err", err.Error()))
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

	// Graceful pre-shutdown (dev.Stop): tear down any active dev session before
	// the prod shutdown. The watchdog is already armed, so even a hung hook
	// cannot defeat the bounded exit.
	c.mu.Lock()
	pre := c.preShutdown
	c.mu.Unlock()
	if pre != nil {
		pre()
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
	go stop() // best-effort loop release; must never block the exit path

	if err != nil {
		c.doExit(ExitShutdownHang)
		return
	}
	c.doExit(ExitClean)
}

func (c *Controller) doExit(code int) { c.exitOnce.Do(func() { c.exit(code) }) }
