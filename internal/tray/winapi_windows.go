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
	swHide = 0
	wmQuit = 0x0012
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
//
// LIMITATION: this only helps console-subsystem builds running under the
// classic conhost. It cannot hide Windows Terminal (the Win11 default
// console host) — there GetConsoleWindow returns the pseudoconsole's
// hidden window, not the WT window, so the terminal stays visible for
// the tray's whole lifetime. That is why the shipped bundle is linked
// `-H windowsgui` instead (see Taskfile.yml's bundle task and
// cmd/venom/console_windows.go); this remains as a best-effort cleanup
// for plain console-subsystem `go build` runs under conhost.
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
