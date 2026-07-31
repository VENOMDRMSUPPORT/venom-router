//go:build !windows

package tray

// NotifyStartupFailure is a no-op off Windows: the bare tray mode's
// headless fallback runs from a terminal, where the returned boot error
// is already printed to stderr by main.
func NotifyStartupFailure(string) {}
