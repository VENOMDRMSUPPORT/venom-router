//go:build linux

package platform

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDataDir_Linux_XDGSet(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)

	got, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir() error = %v", err)
	}
	want := filepath.Join(dir, "venom-router")
	if got != want {
		t.Fatalf("DataDir() = %q, want %q", got, want)
	}
}

func TestDataDir_Linux_XDGUnsetFallsBackToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", home)

	got, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir() error = %v", err)
	}
	want := filepath.Join(home, ".local", "share", "venom-router")
	if got != want {
		t.Fatalf("DataDir() = %q, want %q", got, want)
	}
}

func TestDataDir_Linux_BothUnset(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", "")

	_, err := DataDir()
	if !errors.Is(err, ErrHomeUnset) {
		t.Fatalf("DataDir() error = %v, want ErrHomeUnset", err)
	}
}

func TestEnsureDataDir_Linux_Permissions(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)

	got, err := EnsureDataDir()
	if err != nil {
		t.Fatalf("EnsureDataDir() error = %v", err)
	}

	info, err := os.Stat(got)
	if err != nil {
		t.Fatalf("stat %q: %v", got, err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("dir perm = %o, want 0700", perm)
	}
}

func TestTryLockFile_ExclusiveAcrossHandles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.lock")

	f1, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatalf("open f1: %v", err)
	}
	defer func() { _ = f1.Close() }()

	if err := TryLockFile(f1); err != nil {
		t.Fatalf("first TryLockFile() error = %v", err)
	}

	f2, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatalf("open f2: %v", err)
	}
	defer func() { _ = f2.Close() }()

	if err := TryLockFile(f2); !errors.Is(err, ErrLocked) {
		t.Fatalf("second TryLockFile() (independent handle, same path) error = %v, want ErrLocked", err)
	}

	if err := UnlockFile(f1); err != nil {
		t.Fatalf("UnlockFile(f1) error = %v", err)
	}

	if err := TryLockFile(f2); err != nil {
		t.Fatalf("TryLockFile(f2) after f1 released, error = %v, want success", err)
	}
	if err := UnlockFile(f2); err != nil {
		t.Fatalf("UnlockFile(f2) error = %v", err)
	}
}

func TestIsProcessRunning(t *testing.T) {
	if !IsProcessRunning(os.Getpid()) {
		t.Fatalf("IsProcessRunning(own pid) = false, want true")
	}
	if IsProcessRunning(999999999) {
		t.Fatalf("IsProcessRunning(implausible pid) = true, want false")
	}
}
