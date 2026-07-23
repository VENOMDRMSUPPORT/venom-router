package app

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/platform"
)

// setDataDirEnv points platform's data-dir resolution at a fresh temp
// directory for the duration of the test, regardless of which OS build
// is running: LOCALAPPDATA is read on Windows, XDG_DATA_HOME on Linux.
func setDataDirEnv(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	t.Setenv("LOCALAPPDATA", base)
	t.Setenv("XDG_DATA_HOME", base)
	return base
}

func TestAcquireLock_SecondAcquisitionRejected(t *testing.T) {
	setDataDirEnv(t)

	lock1, err := AcquireLock()
	if err != nil {
		t.Fatalf("first AcquireLock() error = %v", err)
	}
	defer func() {
		if err := lock1.Release(); err != nil {
			t.Fatalf("Release() error = %v", err)
		}
	}()

	_, err = AcquireLock()
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second AcquireLock() error = %v, want ErrAlreadyRunning", err)
	}
}

func TestAcquireLock_ReacquirableAfterRelease(t *testing.T) {
	setDataDirEnv(t)

	lock1, err := AcquireLock()
	if err != nil {
		t.Fatalf("first AcquireLock() error = %v", err)
	}
	if err := lock1.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}

	lock2, err := AcquireLock()
	if err != nil {
		t.Fatalf("AcquireLock() after release, error = %v, want success", err)
	}
	if err := lock2.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
}

// TestAcquireLock_StaleLockRecovered simulates a lock file left behind by
// a previous instance that is no longer running. It seeds the lock file
// directly with an implausibly large PID (999999999, guaranteed not to
// correspond to a running process) rather than spawning and killing a
// real subprocess: this design's actual staleness recovery comes from
// the OS advisory lock being free (the OS releases it when the owning
// process/handle goes away), not from checking PID liveness, so any
// inert leftover file content exercises the recovery path equally well
// while keeping the test simple and non-flaky.
func TestAcquireLock_StaleLockRecovered(t *testing.T) {
	setDataDirEnv(t)

	dataDir, err := platform.EnsureDataDir()
	if err != nil {
		t.Fatalf("platform.EnsureDataDir() error = %v", err)
	}

	lockPath := filepath.Join(dataDir, "venom.lock")
	const stalePID = 999999999
	if err := os.WriteFile(lockPath, []byte(strconv.Itoa(stalePID)), 0o600); err != nil {
		t.Fatalf("seed stale lock file: %v", err)
	}

	lock, err := AcquireLock()
	if err != nil {
		t.Fatalf("AcquireLock() with stale lock file present, error = %v, want success (stale lock must be recovered, not permanently refused)", err)
	}
	defer func() {
		if err := lock.Release(); err != nil {
			t.Fatalf("Release() error = %v", err)
		}
	}()

	// Read back through the held handle itself (lock.file), not a fresh
	// os.ReadFile: on Windows, LockFileEx applies mandatory locking to the
	// locked byte range, so a second handle opened while the lock is
	// still held would itself fail to read that range.
	buf := make([]byte, 32)
	n, err := lock.file.ReadAt(buf, 0)
	if err != nil && n == 0 {
		t.Fatalf("read lock file via held handle: %v", err)
	}
	gotPID, err := strconv.Atoi(strings.TrimSpace(string(buf[:n])))
	if err != nil {
		t.Fatalf("parse lock file content %q: %v", buf[:n], err)
	}
	if gotPID != os.Getpid() {
		t.Fatalf("lock file pid = %d, want %d (own pid, overwriting the stale one)", gotPID, os.Getpid())
	}
}
