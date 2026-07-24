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
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/app"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/config"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/platform"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// version is a placeholder build identifier. A real build-time version
// scheme lands in P8-REL-005.
const version = "dev"

const usage = `venom - Venom Router

Usage:
  venom              Tray mode: starts the server (tray icon is a stub, see P6-FND-001)
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
		// Bare mode is the tray entry point. This unit owns only the
		// "starts the server" half of tray mode (the same run loop as
		// serve); the tray icon, console-hiding, and OS-native UI are a
		// stub deferred to P6-FND-001 — none of that is built here. Bare
		// mode takes no subcommand word, so it has no flags of its own;
		// config still resolves via defaults/env (e.g. VENOM_BIND).
		return runServeLoop(ctx, nil, stdout)
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
