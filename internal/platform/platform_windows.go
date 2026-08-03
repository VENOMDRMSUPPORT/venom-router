//go:build windows

package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// ErrLocalAppDataUnset is returned when %LOCALAPPDATA% is unset or empty.
var ErrLocalAppDataUnset = errors.New("platform: LOCALAPPDATA is unset")

// DataDir returns %LOCALAPPDATA%\venom-router (lowercase-hyphen, matching
// the Linux dir name and the module name). Deliberately NOT the old
// %LOCALAPPDATA%\VenomRouter: that directory belongs to the owner's
// separate live install at G:\Venom-Router, whose keyring is
// schema-incompatible with this project's reader (it blocked bare tray
// boot). This project must never read or write that directory again, so
// no migration or fallback to the old name exists on purpose.
func DataDir() (string, error) {
	base, ok := os.LookupEnv("LOCALAPPDATA")
	if !ok || base == "" {
		return "", ErrLocalAppDataUnset
	}
	return filepath.Join(base, "venom-router"), nil
}

// envProgramFiles/envProgramFilesX86/envLocalAppData are the Windows
// install-root variables a caller needs to locate a third-party
// executable (the tray's app-window launcher looks for an installed Edge
// or Chrome under them). os.LookupEnv is called only within this package
// (and internal/config); forbidigo enforces that no other package reads
// environment variables directly — internal/tray receives the resolved
// roots as a typed value instead of reading the environment itself.
const (
	envProgramFiles    = "ProgramFiles"
	envProgramFilesX86 = "ProgramFiles(x86)"
	envLocalAppData    = "LOCALAPPDATA"
)

// InstallRoots carries the resolved Windows install-root directories.
// Each field is "" when its variable is unset OR set to an empty value:
// callers join a relative path onto these roots, and joining onto "" would
// produce a bogus relative path, so "unset" and "set but empty" are
// deliberately collapsed into the single "no such root" answer.
type InstallRoots struct {
	ProgramFiles    string
	ProgramFilesX86 string
	LocalAppData    string
}

// WindowsInstallRoots resolves the three install-root variables in one
// read. It never fails: a missing root is reported as "" and it is the
// caller's job to skip it (unlike DataDir, no single root is required for
// the router to run).
func WindowsInstallRoots() InstallRoots {
	return InstallRoots{
		ProgramFiles:    os.Getenv(envProgramFiles),
		ProgramFilesX86: os.Getenv(envProgramFilesX86),
		LocalAppData:    os.Getenv(envLocalAppData),
	}
}

// TryLockFile acquires an exclusive, non-blocking OS-level lock on f via
// LockFileEx. The lock is scoped to this specific open handle: opening
// the same path again (in this process or another) yields an independent
// handle that will conflict with an already-locked one, which is exactly
// the real mutual exclusion single-instance enforcement needs.
func TryLockFile(f *os.File) error {
	ol := new(windows.Overlapped)
	err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_FAIL_IMMEDIATELY|windows.LOCKFILE_EXCLUSIVE_LOCK,
		0,
		1, 0,
		ol,
	)
	if err != nil {
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return ErrLocked
		}
		return fmt.Errorf("platform: lock file: %w", err)
	}
	return nil
}

// UnlockFile releases a lock previously acquired by TryLockFile on f.
func UnlockFile(f *os.File) error {
	ol := new(windows.Overlapped)
	if err := windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, ol); err != nil {
		return fmt.Errorf("platform: unlock file: %w", err)
	}
	return nil
}

// stillActive is the Windows API STILL_ACTIVE constant (winbase.h),
// returned by GetExitCodeProcess for a process that has not yet exited.
// It is not exported by golang.org/x/sys/windows, so it is defined here.
const stillActive = 259

// IsProcessRunning reports whether pid identifies a currently running
// process. It is a diagnostic aid only (used to log what a recovered
// stale lock belonged to) — TryLockFile/UnlockFile, not this function,
// are what single-instance correctness actually depends on.
func IsProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(h) }()

	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}
