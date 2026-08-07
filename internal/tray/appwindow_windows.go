//go:build windows

package tray

import (
	"os/exec"
	"path/filepath"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/platform"
)

// controlWindowGate is the process-wide gate that keeps the tray to a single
// control window across every left-click.
var controlWindowGate appWindowGate

// openOrFocusControlWindow is what a tray left-click does: raise the control
// window if one is already open, else open it — never a second one. Chromium
// does not de-duplicate --app= windows by URL, so without this every click
// would stack another identical window.
func openOrFocusControlWindow(url string) (tapOutcome, error) {
	return controlWindowGate.resolveTap(
		time.Now(),
		focusControlWindow,
		func() error { return openControlWindow(url) },
	)
}

// openControlWindow opens url as a chromeless app-window using an installed
// Edge or Chrome; if neither is found it falls back to the default browser (a
// normal tab), which still shows the fully functional control page. The
// browser is launched detached — it is a separate process, so closing its
// window never touches the tray.
func openControlWindow(url string) error {
	name, args, ok := resolveAppWindowCommand(resolveBrowserCandidates(), url)
	if !ok {
		return shellOpen(url)
	}
	return exec.Command(name, args...).Start()
}

// resolveBrowserCandidates returns Edge then Chrome with their resolved exe
// paths ("" when not installed), preferring the well-known install locations
// and falling back to PATH. The install roots are read once, in
// internal/platform, and passed in as a typed value — this package never
// reads the environment itself.
func resolveBrowserCandidates() []browserCandidate {
	roots := platform.WindowsInstallRoots()
	return []browserCandidate{
		{Name: "edge", Path: firstExisting(edgePaths(roots))},
		{Name: "chrome", Path: firstExisting(chromePaths(roots))},
	}
}

// edgePaths lists Edge's well-known locations under the given roots, most
// specific first, then whatever PATH resolves. A root reported as ""
// contributes no candidate: joining onto it would yield a relative path
// that could match an unrelated file in the process's working directory.
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

// chromePaths lists Chrome's well-known locations under the given roots
// (including the per-user LOCALAPPDATA install), with the same ""-root rule
// as edgePaths.
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

// firstExisting returns the first path that exists as a regular file, else "".
func firstExisting(paths []string) string {
	for _, p := range paths {
		if fileExists(p) {
			return p
		}
	}
	return ""
}
