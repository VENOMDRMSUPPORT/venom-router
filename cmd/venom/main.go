// Command venom is the Venom Router binary. This file is a thin
// entrypoint only: it wires real OS signal handling into internal/cli's
// dispatch and translates a returned error into a non-zero exit code.
// All actual behavior — config loading, the composition-root boot,
// graceful shutdown — lives in internal/cli and internal/app.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/app"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/cli"
)

func main() {
	ctx, stop := app.NotifyContext(context.Background())
	defer stop()

	if err := cli.Dispatch(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
