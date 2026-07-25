// Package cli dispatches process arguments to the venom binary's run
// modes: serve, version, help, and the bare (tray) entry point. serve and
// bare load config and boot the real composition root
// (internal/app.Boot), then block until a shutdown request arrives and
// shut down within app.ShutdownTimeout; version/help are simple,
// immediate outputs.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/app"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/config"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/observability"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/platform"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/tray"
)

// version is a placeholder build identifier. A real build-time version
// scheme lands in P8-REL-005.
const version = "dev"

const usage = `venom - Venom Router

Usage:
  venom              Tray mode: system tray (status, open dashboard, restart, logs, quit)
  venom serve        Headless server mode with graceful shutdown
  venom reset-owner  Clear the owner login (first-run setup state) without
                      touching encrypted provider credentials. Requires
                      venom to NOT be currently running.
  venom version      Print version
  venom help         Print this help text
`

// ErrUnrecognizedMode is returned by Dispatch when args names a mode this
// binary does not recognize.
var ErrUnrecognizedMode = errors.New("cli: unrecognized mode")

// bootFunc is the composition-root boot entry, indirected only so the
// dispatch tests can substitute a boot that injects a fake dashboard SPA
// (app.BootConfig.SPAHandler). Production always uses app.Boot unchanged,
// which builds the real embedded dashboard via httpui.New(). This keeps
// the cli serve/bare tests independent of a frontend build.
var bootFunc = app.Boot

// Dispatch routes process arguments (typically os.Args[1:]) to the
// correct run mode, writing mode output to stdout and error output to
// stderr. ctx carries the shutdown request for serve/bare's run loop —
// in production it comes from app.NotifyContext (real SIGINT/SIGTERM);
// tests may cancel it directly.
func Dispatch(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	mode := ""
	if len(args) > 0 {
		mode = args[0]
	}

	switch mode {
	case "":
		// Bare mode runs the real tray (P6-FND-001): the native system-tray
		// UI on Windows, a headless fallback elsewhere, both backed by the
		// in-process server. Indirected via runTrayLoopFn so dispatch routing
		// is testable without the desktop UI / os.Exit path.
		return runTrayLoopFn(ctx, stdout)
	case "serve":
		// args[1:] (everything after "serve" itself) is what config.Load
		// parses for flags, so `venom serve -bind host:port` works.
		return runServeLoop(ctx, args[1:], stdout)
	case "reset-owner":
		return runResetOwner(ctx, stdout, stderr)
	case "version":
		_, _ = fmt.Fprintln(stdout, version)
		return nil
	case "help":
		_, _ = fmt.Fprint(stdout, usage)
		return nil
	default:
		_, _ = fmt.Fprintf(stderr, "venom: unrecognized mode %q\n\n%s", mode, usage)
		return fmt.Errorf("%w: %q", ErrUnrecognizedMode, mode)
	}
}

// runResetOwner implements `venom reset-owner` (P2b-SEC-007, 09 §5.7's
// "local owner reset"): the owner physically controls the machine, so
// running this subcommand on the host IS the authorization — there is
// no password, no email, no recoverable hint. It clears the owner_auth
// row and revokes every session, returning the app to the first-run
// setup state. It NEVER touches the keyring or any encrypted provider
// credential — those remain protected by the device keyring exactly as
// before; this only resets the login gate.
//
// The single-instance lock is acquired FIRST and is the "server is
// stopped" proof: if venom is currently running, AcquireLock fails with
// app.ErrAlreadyRunning and this refuses outright, rather than mutating
// owner_auth out from under a live session.
func runResetOwner(ctx context.Context, stdout, stderr io.Writer) error {
	lock, err := app.AcquireLock()
	if err != nil {
		if errors.Is(err, app.ErrAlreadyRunning) {
			_, _ = fmt.Fprintln(stderr, "venom: cannot reset while venom is running — stop it first")
			return err
		}
		return fmt.Errorf("cli: acquire lock: %w", err)
	}
	defer func() { _ = lock.Release() }()

	dataDir, err := platform.EnsureDataDir()
	if err != nil {
		return fmt.Errorf("cli: resolve data dir: %w", err)
	}

	db, err := storage.Open(dataDir)
	if err != nil {
		return fmt.Errorf("cli: open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := storage.Migrate(ctx, db); err != nil {
		return fmt.Errorf("cli: migrate database: %w", err)
	}

	if err := storage.NewOwnerAuthRepo(db).Clear(ctx); err != nil {
		return fmt.Errorf("cli: clear owner auth: %w", err)
	}
	if err := storage.NewOwnerSessionRepo(db).RevokeAll(ctx, time.Now()); err != nil {
		return fmt.Errorf("cli: revoke sessions: %w", err)
	}

	_, _ = fmt.Fprintln(stdout, "venom: owner login reset — the app is back to first-run setup state.")
	_, _ = fmt.Fprintln(stdout, "venom: provider credentials remain encrypted and were not touched.")
	return nil
}

// runServeLoop loads config, boots the real composition root
// (internal/app.Boot), blocks until ctx is done (a shutdown request),
// then shuts down within app.ShutdownTimeout via app.Run — passing
// Server.Shutdown itself as the Drainer, since its signature already
// matches (func(context.Context) error).
func runServeLoop(ctx context.Context, configArgs []string, stdout io.Writer) error {
	cfg, err := config.Load(configArgs)
	if err != nil {
		return fmt.Errorf("cli: load config: %w", err)
	}

	srv, err := bootFunc(ctx, app.BootConfig{Bind: cfg.Bind})
	if err != nil {
		return fmt.Errorf("cli: boot: %w", err)
	}

	_, _ = fmt.Fprintf(stdout, "venom: serving on %s (waiting for shutdown signal)\n", srv.Addr)

	err = app.Run(ctx, srv.Shutdown, app.ShutdownTimeout)

	_, _ = fmt.Fprintln(stdout, "venom: shutdown complete")
	return err
}

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

	if err := tray.RunNativeUI(ctx, cancel, ctrl); err != nil {
		return err
	}
	// Termination is owned by the ctx.Done() watcher above (watchdog-first
	// ShutdownAndExit -> os.Exit). Block so returning to main can never race —
	// and skip — that graceful shutdown on the non-Windows fallback path. The
	// watchdog guarantees os.Exit within ShutdownTimeout+margin.
	select {}
}

// runTrayLoopFn indirects runTrayLoop so tests can assert bare-mode routing
// without entering the real tray UI / bounded-exit path (mirrors bootFunc).
var runTrayLoopFn = runTrayLoop

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
	f, err := openAppendLog(logPath)
	if err != nil {
		return nil, "", func() {}, err
	}
	logger := observability.New(slog.NewJSONHandler(f, nil))
	return logger, logPath, func() { _ = f.Close() }, nil
}

// openAppendLog opens path append-only (O_APPEND|O_CREATE|O_WRONLY), never
// truncating — owner condition 3 / spec 4.3. Factored out for a hermetic
// append-only test against a temp path.
func openAppendLog(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
}
