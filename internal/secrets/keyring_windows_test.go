//go:build windows

package secrets

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoad_Windows_FileCreatedUnderDataDir does not assert a POSIX mode
// (a Go file-mode chmod is not meaningful on Windows — see
// internal/platform.DataDir's per-OS rationale). It asserts only that
// the keyring is created at the expected per-user-data-dir-relative
// path, mirroring platform_windows_test.go's stance for EnsureDataDir.
func TestLoad_Windows_FileCreatedUnderDataDir(t *testing.T) {
	dir := t.TempDir()

	kr, err := Load(dir, "", false)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if kr.ActiveKeyID == "" {
		t.Fatalf("ActiveKeyID is empty")
	}

	keyringPath := filepath.Join(dir, "secrets", "keyring.json")
	if _, err := os.Stat(keyringPath); err != nil {
		t.Fatalf("keyring file does not exist at expected path: %v", err)
	}
}
