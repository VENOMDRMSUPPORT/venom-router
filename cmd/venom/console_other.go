//go:build !windows

package main

// attachParentConsole is Windows-only plumbing (parent-console attach for
// the `-H windowsgui` bundle — see console_windows.go); elsewhere the
// process always has usable std streams already.
func attachParentConsole() {}
