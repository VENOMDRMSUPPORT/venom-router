//go:build windows

package tray

import (
	"sync"
	"syscall"
	"time"
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
	procPostMessageW             = user32.NewProc("PostMessageW")
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procGetWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")
	procAttachThreadInput        = user32.NewProc("AttachThreadInput")
	procLoadImageW               = user32.NewProc("LoadImageW")
	procSendMessageW             = user32.NewProc("SendMessageW")
	procGetSystemMetrics         = user32.NewProc("GetSystemMetrics")
)

const (
	swHide    = 0
	swRestore = 9
	wmClose   = 0x0010
	wmQuit    = 0x0012
	wmSetIcon = 0x0080

	iconSmall = 0
	iconBig   = 1
	imageIcon = 1

	smCxIcon      = 11
	smCyIcon      = 12
	smCxSmallIcon = 49
	smCySmallIcon = 50
	smCxScreen    = 0
	smCyScreen    = 1

	lrDefaultSize = 0x00000040
	lrShared      = 0x00008000

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
// hiding would hide the user's terminal, so return false.
func shouldHideConsole() bool {
	hwnd, _, _ := procGetConsoleWindow.Call()
	if hwnd == 0 {
		return false
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

var (
	enumMu   sync.Mutex
	enumOnce sync.Once
	enumCB   uintptr
	enumPred func(class, title string, visible bool) bool
	enumHit  uintptr
)

// findWindowMatching returns the handle of the first top-level window pred accepts.
func findWindowMatching(pred func(class, title string, visible bool) bool) uintptr {
	enumMu.Lock()
	defer enumMu.Unlock()

	enumOnce.Do(func() {
		enumCB = windows.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
			if enumPred != nil && enumPred(windowClass(hwnd), windowTitle(hwnd), windowVisible(hwnd)) {
				enumHit = hwnd
				return 0
			}
			return 1
		})
	})

	enumPred, enumHit = pred, 0
	_, _, _ = procEnumWindows.Call(enumCB, 0)
	enumPred = nil
	return enumHit
}

// focusControlWindow raises the tray's own control window and reports whether
// one was found. Icon assignment is repeated here so a slow browser launch is
// corrected on the first subsequent tray click as well.
func focusControlWindow() bool {
	hwnd := findWindowMatching(isControlWindow)
	if hwnd == 0 {
		return false
	}
	applyVenomWindowIdentity(hwnd)
	if iconic, _, _ := procIsIconic.Call(hwnd); iconic != 0 {
		_, _, _ = procShowWindow.Call(hwnd, uintptr(swRestore))
	}
	if ok, _, _ := procSetForegroundWindow.Call(hwnd); ok == 0 {
		forceForeground(hwnd)
	}
	_, _, _ = procBringWindowToTop.Call(hwnd)
	return true
}

// applyVenomWindowIcon replaces the browser-provided icon on the control
// window. Resource ID 1 is supplied by cmd/venom/rsrc_windows_amd64.syso.
func applyVenomWindowIcon(hwnd uintptr) {
	var module windows.Handle
	if err := windows.GetModuleHandleEx(0, nil, &module); err != nil {
		return
	}
	bigW, _, _ := procGetSystemMetrics.Call(smCxIcon)
	bigH, _, _ := procGetSystemMetrics.Call(smCyIcon)
	smallW, _, _ := procGetSystemMetrics.Call(smCxSmallIcon)
	smallH, _, _ := procGetSystemMetrics.Call(smCySmallIcon)
	flags := uintptr(lrDefaultSize | lrShared)
	big, _, _ := procLoadImageW.Call(uintptr(module), 1, imageIcon, bigW, bigH, flags)
	small, _, _ := procLoadImageW.Call(uintptr(module), 1, imageIcon, smallW, smallH, flags)
	if big != 0 {
		_, _, _ = procSendMessageW.Call(hwnd, wmSetIcon, iconBig, big)
	}
	if small != 0 {
		_, _, _ = procSendMessageW.Call(hwnd, wmSetIcon, iconSmall, small)
	}
}

// retireControlWindow closes the one app-window Chromium may have kept alive
// after the previous venom.exe exited.
func retireControlWindow() {
	hwnd := findWindowMatching(isControlWindow)
	if hwnd == 0 {
		return
	}
	_, _, _ = procPostMessageW.Call(hwnd, uintptr(wmClose), 0, 0)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if findWindowMatching(isControlWindow) == 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
}

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
	defer func() { _, _, _ = procAttachThreadInput.Call(ours, fgTID, 0) }()
	_, _, _ = procSetForegroundWindow.Call(hwnd)
}

func windowClass(hwnd uintptr) string {
	var buf [classNameMax]uint16
	n, _, _ := procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return windows.UTF16ToString(buf[:n])
}

func windowTitle(hwnd uintptr) string {
	var buf [windowTextMax]uint16
	n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return windows.UTF16ToString(buf[:n])
}

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
	r, _, callErr := procShellExecuteW.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)), 0, 0, 1)
	if r <= 32 {
		return callErr
	}
	return nil
}
