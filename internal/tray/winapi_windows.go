//go:build windows

package tray

import (
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32                     = windows.NewLazySystemDLL("kernel32.dll")
	user32                       = windows.NewLazySystemDLL("user32.dll")
	shell32                      = windows.NewLazySystemDLL("shell32.dll")
	procGetConsoleWindow         = kernel32.NewProc("GetConsoleWindow")
	procGetConsoleProcList       = kernel32.NewProc("GetConsoleProcessList")
	procShowWindow               = user32.NewProc("ShowWindow")
	procPostThreadMessageW       = user32.NewProc("PostThreadMessageW")
	procShellExecuteW            = shell32.NewProc("ShellExecuteW")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetClassNameW            = user32.NewProc("GetClassNameW")
	procGetWindowTextW           = user32.NewProc("GetWindowTextW")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procIsIconic                 = user32.NewProc("IsIconic")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procBringWindowToTop         = user32.NewProc("BringWindowToTop")
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procGetWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")
	procAttachThreadInput        = user32.NewProc("AttachThreadInput")
)

const (
	swHide    = 0
	swRestore = 9
	wmQuit    = 0x0012

	// classNameMax / windowTextMax are the buffer sizes for the two window
	// strings we read. A window class is capped at 256 chars by Windows; titles
	// are not, but the ones we match are short and a truncated title simply
	// fails the exact-match predicate.
	classNameMax  = 256
	windowTextMax = 512
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

// Window enumeration state. windows.NewCallback registers a callback for the
// life of the process and the registry is small, so the EnumWindows callback is
// created exactly once and the per-search predicate/result are passed through
// these package variables under enumMu. enumMu also makes concurrent searches
// safe; taps are already serialised by appWindowGate, this is defence in depth.
var (
	enumMu   sync.Mutex
	enumOnce sync.Once
	enumCB   uintptr
	enumPred func(class, title string, visible bool) bool
	enumHit  uintptr
)

// findWindowMatching returns the handle of the first top-level window pred
// accepts, or 0 when none matches.
func findWindowMatching(pred func(class, title string, visible bool) bool) uintptr {
	enumMu.Lock()
	defer enumMu.Unlock()

	enumOnce.Do(func() {
		enumCB = windows.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
			if enumPred != nil && enumPred(windowClass(hwnd), windowTitle(hwnd), windowVisible(hwnd)) {
				enumHit = hwnd
				return 0 // a zero return stops the enumeration
			}
			return 1 // keep enumerating
		})
	})

	enumPred, enumHit = pred, 0
	_, _, _ = procEnumWindows.Call(enumCB, 0)
	enumPred = nil
	return enumHit
}

// focusControlWindow raises the tray's own control window and reports whether
// one was FOUND — not whether raising it fully succeeded. That distinction is
// deliberate: the return value decides whether the caller spawns another
// window, and a found-but-stubborn window must never become a second window.
// Raising is therefore best-effort, with the AttachThreadInput dance as the
// fallback for Windows' foreground lock (a tray click leaves the shell, not us,
// as the foreground process, so a bare SetForegroundWindow can be refused).
func focusControlWindow() bool {
	hwnd := findWindowMatching(isControlWindow)
	if hwnd == 0 {
		return false
	}
	if iconic, _, _ := procIsIconic.Call(hwnd); iconic != 0 {
		_, _, _ = procShowWindow.Call(hwnd, uintptr(swRestore))
	}
	if ok, _, _ := procSetForegroundWindow.Call(hwnd); ok == 0 {
		forceForeground(hwnd)
	}
	_, _, _ = procBringWindowToTop.Call(hwnd)
	return true
}

// forceForeground retries SetForegroundWindow while sharing an input queue with
// the current foreground window's thread, which lifts the foreground lock.
func forceForeground(hwnd uintptr) {
	fg, _, _ := procGetForegroundWindow.Call()
	if fg == 0 {
		return
	}
	fgTID, _, _ := procGetWindowThreadProcessID.Call(fg, 0)
	if fgTID == 0 {
		return
	}
	ours := uintptr(windows.GetCurrentThreadId())
	if fgTID == ours {
		return
	}
	if attached, _, _ := procAttachThreadInput.Call(ours, fgTID, 1); attached == 0 {
		return
	}
	defer procAttachThreadInput.Call(ours, fgTID, 0) //nolint:errcheck // best-effort detach
	_, _, _ = procSetForegroundWindow.Call(hwnd)
}

// windowClass returns a window's class name ("" on failure).
func windowClass(hwnd uintptr) string {
	var buf [classNameMax]uint16
	n, _, _ := procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return windows.UTF16ToString(buf[:n])
}

// windowTitle returns a window's caption ("" on failure or for untitled windows).
func windowTitle(hwnd uintptr) string {
	var buf [windowTextMax]uint16
	n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return windows.UTF16ToString(buf[:n])
}

// windowVisible reports IsWindowVisible, which filters out the many hidden
// helper windows Chromium keeps around.
func windowVisible(hwnd uintptr) bool {
	v, _, _ := procIsWindowVisible.Call(hwnd)
	return v != 0
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
