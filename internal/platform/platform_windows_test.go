//go:build windows

package platform

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDataDir_Windows(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)

	got, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir() error = %v", err)
	}
	// Lowercase-hyphen, matching the Linux dir name and the module name.
	// The owner's SEPARATE live install at G:\Venom-Router owns the old
	// %LOCALAPPDATA%\VenomRouter directory (its keyring is schema-
	// incompatible with our reader); this project must never read or
	// write that directory again.
	want := filepath.Join(dir, "venom-router")
	if got != want {
		t.Fatalf("DataDir() = %q, want %q", got, want)
	}
}

func TestDataDir_Windows_Unset(t *testing.T) {
	t.Setenv("LOCALAPPDATA", "")

	_, err := DataDir()
	if !errors.Is(err, ErrLocalAppDataUnset) {
		t.Fatalf("DataDir() error = %v, want ErrLocalAppDataUnset", err)
	}
}

func TestEnsureDataDir_Windows(t *testing.T) {
	base := t.TempDir()
	t.Setenv("LOCALAPPDATA", base)

	dir, err := EnsureDataDir()
	if err != nil {
		t.Fatalf("EnsureDataDir() error = %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat %q: %v", dir, err)
	}
	if !info.IsDir() {
		t.Fatalf("%q is not a directory", dir)
	}

	// Idempotent: calling again when the directory already exists must
	// succeed without error.
	dir2, err := EnsureDataDir()
	if err != nil {
		t.Fatalf("EnsureDataDir() second call error = %v", err)
	}
	if dir2 != dir {
		t.Fatalf("EnsureDataDir() second call = %q, want %q", dir2, dir)
	}
}

// TestWindowsInstallRoots pins that all three install roots are resolved in
// one read, and that a root which is unset or set-but-empty comes back as ""
// rather than as a value borrowed from another variable.
func TestWindowsInstallRoots(t *testing.T) {
	t.Run("all three roots resolved independently", func(t *testing.T) {
		t.Setenv("ProgramFiles", `C:\PF`)
		t.Setenv("ProgramFiles(x86)", `C:\PFx86`)
		t.Setenv("LOCALAPPDATA", `C:\LAD`)

		got := WindowsInstallRoots()
		want := InstallRoots{ProgramFiles: `C:\PF`, ProgramFilesX86: `C:\PFx86`, LocalAppData: `C:\LAD`}
		if got != want {
			t.Fatalf("WindowsInstallRoots() = %+v, want %+v", got, want)
		}
	})

	t.Run("empty roots stay empty", func(t *testing.T) {
		t.Setenv("ProgramFiles", `C:\PF`)
		t.Setenv("ProgramFiles(x86)", "")
		t.Setenv("LOCALAPPDATA", "")

		got := WindowsInstallRoots()
		want := InstallRoots{ProgramFiles: `C:\PF`}
		if got != want {
			t.Fatalf("WindowsInstallRoots() = %+v, want %+v", got, want)
		}
	})
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
