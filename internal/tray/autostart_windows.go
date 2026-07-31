//go:build windows

package tray

import (
	"os"

	"golang.org/x/sys/windows/registry"
)

// runKeyPath is the per-user autostart location: no admin rights required,
// unlike the machine-wide HKLM equivalent.
const runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`

// autostartValueName is the Run-key value name. It is an unexported package
// var (rather than a const) so autostart_windows_test.go can point it at a
// throwaway name (e.g. "VenomRouterTest") and leave the real "venom-router"
// entry untouched.
//
// Deliberately NOT the old "VenomRouter" name: the owner's separate live
// install at G:\Venom-Router plausibly owns that Run-key value, and this
// project must never overwrite (or delete) it. An owner who enabled
// autostart from an old build of THIS app may keep a stale "VenomRouter"
// entry pointing at the old exe path — this code intentionally leaves any
// such entry alone and only manages its own "venom-router" value.
var autostartValueName = "venom-router"

// autostartEnabled reports whether the Run-key value is currently set. A
// missing key or missing value both mean "not enabled"; any other error is
// treated the same way (fail closed to "disabled" rather than erroring the
// caller, since this only backs a checkbox's initial state).
func autostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer func() { _ = k.Close() }()

	_, _, err = k.GetStringValue(autostartValueName)
	return err == nil
}

// enableAutostart writes the current executable's path into the Run key.
func enableAutostart() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer func() { _ = k.Close() }()

	return k.SetStringValue(autostartValueName, exe)
}

// disableAutostart removes the Run-key value. A value that is already absent
// is not an error.
func disableAutostart() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		if err == registry.ErrNotExist {
			return nil
		}
		return err
	}
	defer func() { _ = k.Close() }()

	if err := k.DeleteValue(autostartValueName); err != nil && err != registry.ErrNotExist {
		return err
	}
	return nil
}
