// Package platform resolves the OS-specific application data directory and
// ensures it exists with owner-only permissions, and provides the
// OS-specific primitives internal/app's single-instance lock is built on.
//
// Windows: %LOCALAPPDATA%\venom-router (NOT the old VenomRouter name —
//
//	that directory belongs to the owner's separate live install at
//	G:\Venom-Router and must never be read or written by this project;
//	see DataDir in platform_windows.go).
//
// Linux: $XDG_DATA_HOME/venom-router, falling back to the XDG Base
//
//	Directory Specification default $HOME/.local/share/venom-router
//	when XDG_DATA_HOME is unset or empty. That fallback is the
//	documented standard behavior of $XDG_DATA_HOME itself, not an
//	invented convention.
//
// TryLockFile/UnlockFile and IsProcessRunning are implemented per-OS
// (platform_windows.go / platform_linux.go), the same way DataDir is.
package platform

import (
	"errors"
	"fmt"
	"os"
)

// ErrLocked is returned by TryLockFile when another open file handle (in
// this or another process) already holds the exclusive lock.
var ErrLocked = errors.New("platform: file is already locked")

// dirPerm is the permission mode used when creating the data directory. On
// Linux this yields owner rwx and nothing for group/other. On Windows the
// bits beyond the write flag are not meaningful to the filesystem; per-user
// isolation there comes from %LOCALAPPDATA% already being a per-user path
// by OS convention — no custom ACL manipulation is applied here.
const dirPerm = 0o700

// EnsureDataDir resolves the application data directory and creates it, and
// any missing parents, if it does not already exist. Creation is idempotent.
//
// Resolution order: the VENOM_DATA_DIR environment variable when set and
// non-empty, then the OS-specific DataDir default. Normal dev AND prod both run
// on the default — the ONE canonical database. VENOM_DATA_DIR is a deliberate,
// opt-in escape hatch for a fully isolated scratch instance (throwaway lock/DB/
// keyring), used only when explicitly set — e.g. safe testing that must never
// touch the real DB. It is not set by any normal launch path, so it can never
// silently open a second database.
func EnsureDataDir() (string, error) {
	dir, ok := os.LookupEnv("VENOM_DATA_DIR")
	if !ok || dir == "" {
		var err error
		dir, err = DataDir()
		if err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return "", fmt.Errorf("platform: create data dir %q: %w", dir, err)
	}
	return dir, nil
}

// DevRoot returns the VENOM_DEV_ROOT override for the tray's Development
// section, or "" when unset or empty. The env read lives here because
// forbidigo confines os.Getenv/os.LookupEnv to internal/config and
// internal/platform.
func DevRoot() string {
	v, _ := os.LookupEnv("VENOM_DEV_ROOT")
	return v
}
