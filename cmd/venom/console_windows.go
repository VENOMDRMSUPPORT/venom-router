//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// The shipped bundle is linked with `-H windowsgui` (see Taskfile.yml's
// bundle task) so a double-click / autostart launch is fully silent — no
// console, and no Windows Terminal window (which ShowWindow-based hiding
// cannot touch; see internal/tray/winapi_windows.go hideConsoleIfOwned).
// The trade-off is that CLI modes (`serve`, `version`, `help`,
// `reset-owner`) run from an existing terminal would print nothing,
// because a GUI-subsystem process gets no console. attachParentConsole
// restores that: attach to the parent's console when there is one, and
// repair only the std streams that were invalid at process start.
//
// AttachConsole is not exported by golang.org/x/sys/windows v0.47.0, so
// it is loaded via NewLazySystemDLL — the same pattern
// internal/tray/winapi_windows.go already uses.
var (
	kernel32Main      = windows.NewLazySystemDLL("kernel32.dll")
	procAttachConsole = kernel32Main.NewProc("AttachConsole")
)

// attachParentProcess is ATTACH_PARENT_PROCESS ((DWORD)-1, wincon.h).
const attachParentProcess = uintptr(0xFFFFFFFF)

// attachParentConsole is called FIRST in main(). Behavior by launch shape:
//
//   - Console build (plain `go build`, dev/CI/go test): the process
//     already has a console, AttachConsole fails, return — full no-op.
//   - GUI build double-clicked / autostarted: no parent console,
//     AttachConsole fails, return — stays silent.
//   - GUI build run from a terminal: AttachConsole succeeds; std streams
//     the shell explicitly provided (redirections like `> f.txt`) were
//     valid at process start and are left alone, while streams that were
//     invalid (0 or INVALID_HANDLE_VALUE — the normal GUI-subsystem
//     state) are re-pointed at the attached console via CONIN$/CONOUT$.
//
// Validity is snapshotted BEFORE AttachConsole on purpose: a successful
// attach can update the process std-handle table for handles that were
// never explicitly set, but os.Stdout/os.Stderr keep wrapping the stale
// pre-attach handles the Go runtime captured at init — checking after
// the attach could therefore see "valid" and skip a repair that is still
// needed.
//
// Every error here is swallowed by design: this is last-resort console
// plumbing on the way into main — there is nowhere to report a failure
// yet, and failing must never block the actual run modes.
func attachParentConsole() {
	stdinValid := stdHandleValid(windows.STD_INPUT_HANDLE)
	stdoutValid := stdHandleValid(windows.STD_OUTPUT_HANDLE)
	stderrValid := stdHandleValid(windows.STD_ERROR_HANDLE)

	r, _, _ := procAttachConsole.Call(attachParentProcess)
	if r == 0 {
		return // no parent console, or already attached — no-op
	}

	if !stdinValid {
		repairStd(windows.STD_INPUT_HANDLE, "CONIN$", os.O_RDONLY, &os.Stdin)
	}
	if !stdoutValid {
		repairStd(windows.STD_OUTPUT_HANDLE, "CONOUT$", os.O_WRONLY, &os.Stdout)
	}
	if !stderrValid {
		repairStd(windows.STD_ERROR_HANDLE, "CONOUT$", os.O_WRONLY, &os.Stderr)
	}
}

// stdHandleValid reports whether the std handle slot currently holds a
// usable handle (neither NULL nor INVALID_HANDLE_VALUE).
func stdHandleValid(std uint32) bool {
	h, err := windows.GetStdHandle(std)
	return err == nil && h != 0 && h != windows.InvalidHandle
}

// repairStd re-points one std stream at the attached console. The caller
// (attachParentConsole) is the gate: it calls this ONLY for streams whose
// handle was invalid BEFORE the attach — deliberately no re-check here,
// because after a successful AttachConsole the std slot may already hold
// a fresh console handle while the os.Std* variable still wraps the stale
// pre-attach one, so a post-attach validity check would wrongly skip.
// Errors are swallowed: see attachParentConsole.
func repairStd(std uint32, consoleName string, flag int, f **os.File) {
	c, err := os.OpenFile(consoleName, flag, 0)
	if err != nil {
		return
	}
	*f = c
	_ = windows.SetStdHandle(std, windows.Handle(c.Fd()))
}
