//go:build windows

package tray

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/platform"
)

// controlWindowGate is the process-wide gate that keeps the tray to a single
// control window across every left-click.
var controlWindowGate appWindowGate

func init() {
	appWindowLaunchArgs = centeredAppWindowArgs
}

// openOrFocusControlWindow is what a tray left-click does: raise the control
// window if one is already open, else open it — never a second one.
func openOrFocusControlWindow(url string) (tapOutcome, error) {
	return controlWindowGate.resolveTap(
		time.Now(),
		focusControlWindow,
		func() error {
			return replaceControlWindow(retireControlWindow, func() error {
				return openControlWindow(url)
			})
		},
	)
}

// openControlWindow uses the installed Edge or Chrome app-window for rendering,
// then replaces the browser-provided title-bar/taskbar icon with the Venom icon
// from the executable resource. This keeps the existing WebView-free fallback
// compatible with machines where the WebView2 runtime is unavailable.
func openControlWindow(url string) error {
	name, args, ok := resolveAppWindowCommand(resolveBrowserCandidates(), url)
	if !ok {
		return shellOpen(url)
	}
	if err := exec.Command(name, args...).Start(); err != nil {
		return err
	}

	// The browser creates its top-level window asynchronously. Wait briefly for
	// the exact control title, then set both large and small window icons.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if hwnd := findWindowMatching(isControlWindow); hwnd != 0 {
			applyVenomWindowIdentity(hwnd)
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil
}

func centeredAppWindowArgs(url string) []string {
	width := appWindowWidth
	height := appWindowHeight
	x, y := centeredWindowPosition(width, height)
	return []string{
		"--app=" + url,
		fmt.Sprintf("--window-size=%d,%d", width, height),
		fmt.Sprintf("--window-position=%d,%d", x, y),
	}
}

func centeredWindowPosition(width, height int) (x, y int) {
	sx, _, _ := procGetSystemMetrics.Call(smCxScreen)
	sy, _, _ := procGetSystemMetrics.Call(smCyScreen)
	if sx <= 0 || sy <= 0 {
		return 0, 0
	}
	x = int(sx)/2 - width/2
	y = int(sy)/2 - height/2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	return x, y
}

func resolveBrowserCandidates() []browserCandidate {
	roots := platform.WindowsInstallRoots()
	return []browserCandidate{
		{Name: "edge", Path: firstExisting(edgePaths(roots))},
		{Name: "chrome", Path: firstExisting(chromePaths(roots))},
	}
}

func edgePaths(roots platform.InstallRoots) []string {
	var p []string
	for _, base := range []string{roots.ProgramFilesX86, roots.ProgramFiles} {
		if base != "" {
			p = append(p, filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe"))
		}
	}
	if lp, err := exec.LookPath("msedge.exe"); err == nil {
		p = append(p, lp)
	}
	return p
}

func chromePaths(roots platform.InstallRoots) []string {
	var p []string
	for _, base := range []string{roots.ProgramFiles, roots.ProgramFilesX86, roots.LocalAppData} {
		if base != "" {
			p = append(p, filepath.Join(base, "Google", "Chrome", "Application", "chrome.exe"))
		}
	}
	if lp, err := exec.LookPath("chrome.exe"); err == nil {
		p = append(p, lp)
	}
	return p
}

func firstExisting(paths []string) string {
	for _, p := range paths {
		if fileExists(p) {
			return p
		}
	}
	return ""
}
