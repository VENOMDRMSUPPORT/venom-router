//go:build windows

package tray

import "testing"

// TestAutostart_EnableDisable_RoundTrip points autostartValueName at a
// throwaway Run-key value name (never the real "VenomRouter") so this test
// can never clobber the owner's actual autostart entry, and always cleans up
// via t.Cleanup regardless of pass/fail.
func TestAutostart_EnableDisable_RoundTrip(t *testing.T) {
	orig := autostartValueName
	autostartValueName = "VenomRouterTest"
	t.Cleanup(func() {
		_ = disableAutostart()
		autostartValueName = orig
	})

	if autostartEnabled() {
		t.Fatal("autostartEnabled()=true before enabling; throwaway value name must start absent")
	}

	if err := enableAutostart(); err != nil {
		t.Fatalf("enableAutostart() error: %v", err)
	}
	if !autostartEnabled() {
		t.Fatal("autostartEnabled()=false after enableAutostart()")
	}

	if err := disableAutostart(); err != nil {
		t.Fatalf("disableAutostart() error: %v", err)
	}
	if autostartEnabled() {
		t.Fatal("autostartEnabled()=true after disableAutostart()")
	}
}

// TestAutostart_DisableWhenAbsent_NotAnError proves disableAutostart is safe
// to call even when nothing was ever enabled (e.g. a menu click before the
// user ever toggled autostart on).
func TestAutostart_DisableWhenAbsent_NotAnError(t *testing.T) {
	orig := autostartValueName
	autostartValueName = "VenomRouterTestAbsent"
	t.Cleanup(func() { autostartValueName = orig })

	if err := disableAutostart(); err != nil {
		t.Fatalf("disableAutostart() on an absent value: %v", err)
	}
}
