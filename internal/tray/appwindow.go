package tray

// This file is the platform-neutral core of the tray's app-window launcher:
// choosing which installed browser to open the control page with, in
// chromeless "app" mode. The OS-specific executable resolution and the actual
// process spawn live in appwindow_windows.go.

// browserCandidate is a browser we may launch in app-window mode. Path is the
// resolved executable path, or "" when that browser is not installed.
type browserCandidate struct {
	Name string
	Path string
}

// appWindowSize is the initial chromeless window size (Chromium --window-size).
const appWindowSize = "420,600"

// resolveAppWindowCommand returns the executable and args to open url in a
// chromeless app-window using the first present candidate, or ok=false when
// none is installed (the caller then falls back to a normal browser tab).
func resolveAppWindowCommand(candidates []browserCandidate, url string) (name string, args []string, ok bool) {
	for _, c := range candidates {
		if c.Path == "" {
			continue
		}
		return c.Path, []string{"--app=" + url, "--window-size=" + appWindowSize}, true
	}
	return "", nil, false
}
