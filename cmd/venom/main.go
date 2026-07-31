// Command venom is the Venom Router binary. This file is a thin
// entrypoint only: it wires real OS signal handling into internal/cli's
// dispatch and translates a returned error into a non-zero exit code.
// All actual behavior — config loading, the composition-root boot,
// graceful shutdown — lives in internal/cli and internal/app.
//
// rsrc_windows_amd64.syso (committed alongside this file) embeds
// internal/tray/assets/venom.ico as the Windows resource icon, so
// Explorer shows the Venom icon on venom.exe itself. The Go linker picks
// it up automatically on windows/amd64 builds; the arch-suffixed name
// keeps it ignored everywhere else. Whenever the icon changes,
// regenerate both artifacts from the repo root:
//
//	go run ./tools/genicon
//	go run github.com/akavel/rsrc@v0.10.2 -ico internal/tray/assets/venom.ico -o cmd/venom/rsrc_windows_amd64.syso
//
// (go run with a full module@version never touches go.mod — no
// dependency is added.)
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/app"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/cli"
)

func main() {
	// FIRST: in the `-H windowsgui` bundle, re-attach to the parent
	// terminal's console (when launched from one) so CLI modes still
	// print. No-op for console builds and for double-click launches.
	attachParentConsole()

	ctx, stop := app.NotifyContext(context.Background())
	defer stop()

	if err := cli.Dispatch(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
