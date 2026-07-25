//go:build windows

package tray

import "testing"

// The test binary is normally launched by `go test` sharing the runner's
// console, so process count is > 1 => we must NOT hide.
func TestShouldHideConsole_SharedConsole_ReturnsFalse(t *testing.T) {
	if shouldHideConsole() {
		t.Fatal("shouldHideConsole()=true while sharing the test runner console; must be false")
	}
}
