//go:build windows

package tray

import (
	"os"
	"os/exec"
	"path/filepath"
)

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
// and falling back to PATH.
func resolveBrowserCandidates() []browserCandidate {
	return []browserCandidate{
		{Name: "edge", Path: firstExisting(edgePaths())},
		{Name: "chrome", Path: firstExisting(chromePaths())},
	}
}

func edgePaths() []string {
	var p []string
	for _, base := range []string{os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramFiles")} {
		if base != "" {
			p = append(p, filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe"))
		}
	}
	if lp, err := exec.LookPath("msedge.exe"); err == nil {
		p = append(p, lp)
	}
	return p
}

func chromePaths() []string {
	var p []string
	for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LOCALAPPDATA")} {
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
