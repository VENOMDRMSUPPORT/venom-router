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

	"github.com/VENOMDRMSUPPORT/venom-router/internal/app"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/config"
)

// version is a placeholder build identifier. A real build-time version
// scheme lands in P8-REL-005.
const version = "dev"

const usage = `venom - Venom Router

Usage:
  venom            Tray mode: starts the server (tray icon is a stub, see P6-FND-001)
  venom serve      Headless server mode with graceful shutdown
  venom version    Print version
  venom help       Print this help text
`

// ErrUnrecognizedMode is returned by Dispatch when args names a mode this
// binary does not recognize.
var ErrUnrecognizedMode = errors.New("cli: unrecognized mode")

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

	srv, err := app.Boot(ctx, app.BootConfig{Bind: cfg.Bind})
	if err != nil {
		return fmt.Errorf("cli: boot: %w", err)
	}

	_, _ = fmt.Fprintf(stdout, "venom: serving on %s (waiting for shutdown signal)\n", srv.Addr)

	err = app.Run(ctx, srv.Shutdown, app.ShutdownTimeout)

	_, _ = fmt.Fprintln(stdout, "venom: shutdown complete")
	return err
}
