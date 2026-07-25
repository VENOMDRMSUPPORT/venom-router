//go:build !windows

package tray

// autostartEnabled, enableAutostart, and disableAutostart are stubs on
// non-Windows: the "Start with Windows" checkbox is only ever built by the
// Windows menu adapter (tray_windows.go) regardless, but these stay defined
// package-wide so no other file needs a build tag to reference them.
func autostartEnabled() bool  { return false }
func enableAutostart() error  { return nil }
func disableAutostart() error { return nil }
