//go:build windows

package tray

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

// runKeyPath is the per-user autostart location: no admin rights required,
// unlike the machine-wide HKLM equivalent.
const runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`

// autostartValueName is the Run-key value name. It is an unexported package
// var so tests can point it at a throwaway value and leave the real entry alone.
var autostartValueName = "venom-router"

const legacyStartupScriptName = "venom-router.vbs"

// cleanupLegacyAutostart removes the old Startup-folder script used by earlier
// builds. The current checkbox is backed only by the per-user Run value.
func cleanupLegacyAutostart() error {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	path := filepath.Join(configDir, "Microsoft", "Windows", "Start Menu", "Programs", "Startup", legacyStartupScriptName)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// autostartEnabled reports whether the Run-key value is currently set.
func autostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer func() { _ = k.Close() }()
	_, _, err = k.GetStringValue(autostartValueName)
	return err == nil
}

// autostartCommand makes the intended startup behavior explicit: boot in the
// background and expose the control window only after the tray icon is clicked.
func autostartCommand(exe string) string {
	return `"` + exe + `" --minimized`
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
	return k.SetStringValue(autostartValueName, autostartCommand(exe))
}

// disableAutostart removes the Run-key value and the obsolete startup script.
func disableAutostart() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		if err == registry.ErrNotExist {
			if autostartValueName != "venom-router" {
				return nil
			}
			return cleanupLegacyAutostart()
		}
		return err
	}
	defer func() { _ = k.Close() }()
	if err := k.DeleteValue(autostartValueName); err != nil && err != registry.ErrNotExist {
		return err
	}
	if autostartValueName != "venom-router" {
		return nil
	}
	return cleanupLegacyAutostart()
}
