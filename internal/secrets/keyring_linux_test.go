//go:build linux

package secrets

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_Linux_FilePermissions(t *testing.T) {
	dir := t.TempDir()

	if _, err := Load(dir, "", false); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	secretsDir := filepath.Join(dir, "secrets")
	dirInfo, err := os.Stat(secretsDir)
	if err != nil {
		t.Fatalf("stat secrets dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("secrets dir perm = %o, want 0700", perm)
	}

	keyringPath := filepath.Join(secretsDir, "keyring.json")
	fileInfo, err := os.Stat(keyringPath)
	if err != nil {
		t.Fatalf("stat keyring file: %v", err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o600 {
		t.Fatalf("keyring file perm = %o, want 0600", perm)
	}
}
