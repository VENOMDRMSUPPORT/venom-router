//go:build linux

package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// ErrHomeUnset is returned when XDG_DATA_HOME is unset/empty and the
// $HOME fallback is also unset/empty.
var ErrHomeUnset = errors.New("platform: HOME is unset")

// DataDir returns $XDG_DATA_HOME/venom-router, falling back to
// $HOME/.local/share/venom-router when XDG_DATA_HOME is unset or empty,
// per the XDG Base Directory Specification.
func DataDir() (string, error) {
	if v, ok := os.LookupEnv("XDG_DATA_HOME"); ok && v != "" {
		return filepath.Join(v, "venom-router"), nil
	}

	home, ok := os.LookupEnv("HOME")
	if !ok || home == "" {
		return "", ErrHomeUnset
	}
	return filepath.Join(home, ".local", "share", "venom-router"), nil
}

// TryLockFile acquires an exclusive, non-blocking OS-level lock on f via
// flock(2). flock (not fcntl byte-range locks) is used deliberately:
// flock locks are scoped to the open file description, so opening the
// same path again — even from within this same process — yields an
// independent description that correctly conflicts with an
// already-locked one, which is exactly the real mutual exclusion
// single-instance enforcement needs.
func TryLockFile(f *os.File) error {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) {
			return ErrLocked
		}
		return fmt.Errorf("platform: lock file: %w", err)
	}
	return nil
}

// UnlockFile releases a lock previously acquired by TryLockFile on f.
func UnlockFile(f *os.File) error {
	if err := unix.Flock(int(f.Fd()), unix.LOCK_UN); err != nil {
		return fmt.Errorf("platform: unlock file: %w", err)
	}
	return nil
}

// IsProcessRunning reports whether pid identifies a currently running
// process. It is a diagnostic aid only (used to log what a recovered
// stale lock belonged to) — TryLockFile/UnlockFile, not this function,
// are what single-instance correctness actually depends on.
func IsProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := unix.Kill(pid, 0)
	if err == nil {
		return true
	}
	// EPERM means the process exists but we lack permission to signal
	// it — still running from our perspective. ESRCH (or anything else)
	// means no such process.
	return errors.Is(err, unix.EPERM)
}
