// Package platform resolves the OS-specific application data directory and
// ensures it exists with owner-only permissions, and provides the
// OS-specific primitives internal/app's single-instance lock is built on.
//
// Windows: %LOCALAPPDATA%\VenomRouter.
// Linux:   $XDG_DATA_HOME/venom-router, falling back to the XDG Base
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

// EnsureDataDir resolves the OS-specific application data directory (via
// the platform-specific DataDir) and creates it, and any missing parents,
// if it does not already exist. Creation is idempotent.
func EnsureDataDir() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return "", fmt.Errorf("platform: create data dir %q: %w", dir, err)
	}
	return dir, nil
}
