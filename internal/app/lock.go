package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/platform"
)

// ErrAlreadyRunning is returned by AcquireLock when another live instance
// already holds the single-instance lock.
var ErrAlreadyRunning = errors.New("app: another instance is already running")

// Lock represents a held single-instance lock. Release must be called to
// give it up. If the process exits without calling Release (including a
// crash), the OS releases the underlying advisory lock automatically when
// the file handle closes, so a later AcquireLock call recovers cleanly —
// see AcquireLock's stale-lock handling.
type Lock struct {
	file *os.File
	path string
}

// AcquireLock acquires the single-instance lock at <dataDir>/venom.lock,
// where dataDir comes from platform.EnsureDataDir/platform.DataDir —
// never a hardcoded path.
//
// Real mutual exclusion is enforced by an OS-level, non-blocking,
// exclusive file lock (platform.TryLockFile), not by merely checking
// whether the lock file exists: a second call while a live instance
// holds the lock is reliably rejected with ErrAlreadyRunning.
//
// If the lock file is left over from a previous instance that is no
// longer running, the OS lock on it is simply free (the OS released it
// when that instance's process/handle went away), so AcquireLock
// recovers automatically: it overwrites the stale content with the
// current process's own PID and succeeds, rather than permanently
// refusing to start.
func AcquireLock() (*Lock, error) {
	dataDir, err := platform.EnsureDataDir()
	if err != nil {
		return nil, fmt.Errorf("app: resolve data dir: %w", err)
	}

	lockPath := filepath.Join(dataDir, "venom.lock")

	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("app: open lock file %q: %w", lockPath, err)
	}

	if err := platform.TryLockFile(f); err != nil {
		_ = f.Close()
		if errors.Is(err, platform.ErrLocked) {
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("app: acquire lock %q: %w", lockPath, err)
	}

	// We now hold the OS lock exclusively. Any existing content here is
	// necessarily stale — a still-running owner would have kept us from
	// locking it — but log what it belonged to before overwriting it.
	logPriorOwner(f, lockPath)

	if err := writeOwnPID(f); err != nil {
		_ = platform.UnlockFile(f)
		_ = f.Close()
		return nil, fmt.Errorf("app: write lock file %q: %w", lockPath, err)
	}

	return &Lock{file: f, path: lockPath}, nil
}

// Release releases the single-instance lock: unlocks and closes the
// underlying file. It does not delete the lock file; the next
// AcquireLock call reuses and overwrites it.
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := platform.UnlockFile(l.file)
	closeErr := l.file.Close()
	if unlockErr != nil {
		return fmt.Errorf("app: release lock %q: %w", l.path, unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("app: release lock %q: %w", l.path, closeErr)
	}
	return nil
}

// FocusFirstInstance is the best-effort "focus the first instance" signal
// referenced by 01 §2. It is a documented no-op stub: there is no
// tray icon or dashboard window to focus yet (that UI belongs to
// P6-FND-001 / P2a). Once one exists, this is where IPC to bring it to
// the foreground would be implemented — no window-focusing mechanism is
// invented here.
func FocusFirstInstance() {
	// Intentionally a no-op; see doc comment above.
}

func logPriorOwner(f *os.File, lockPath string) {
	buf := make([]byte, 32)
	n, _ := f.ReadAt(buf, 0)
	if n == 0 {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(buf[:n])))
	if err != nil || pid <= 0 {
		return
	}
	// platform.IsProcessRunning is purely a diagnostic aid here — the OS
	// lock already proved this PID no longer holds it. It only changes
	// the wording of the log line, never the acquisition decision.
	if platform.IsProcessRunning(pid) {
		fmt.Fprintf(os.Stderr, "venom: recovered lock %q previously held by pid %d\n", lockPath, pid)
		return
	}
	fmt.Fprintf(os.Stderr, "venom: recovered stale lock %q left by pid %d (no longer running)\n", lockPath, pid)
}

func writeOwnPID(f *os.File) error {
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.WriteAt([]byte(strconv.Itoa(os.Getpid())), 0); err != nil {
		return err
	}
	return f.Sync()
}
