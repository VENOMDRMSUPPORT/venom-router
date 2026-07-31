package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureDataDir_HonorsVenomDataDirOverride(t *testing.T) {
	override := filepath.Join(t.TempDir(), "override-data")
	t.Setenv("VENOM_DATA_DIR", override)

	got, err := EnsureDataDir()
	if err != nil {
		t.Fatalf("EnsureDataDir() error = %v", err)
	}
	if got != override {
		t.Fatalf("EnsureDataDir() = %q, want the VENOM_DATA_DIR override %q", got, override)
	}
	info, err := os.Stat(got)
	if err != nil || !info.IsDir() {
		t.Fatalf("override dir was not created: info=%v err=%v", info, err)
	}
}

func TestEnsureDataDir_EmptyOverrideFallsBackToDefault(t *testing.T) {
	t.Setenv("VENOM_DATA_DIR", "")

	def, err := DataDir()
	if err != nil {
		t.Skipf("no OS default data dir in this environment: %v", err)
	}
	got, err := EnsureDataDir()
	if err != nil {
		t.Fatalf("EnsureDataDir() error = %v", err)
	}
	if got != def {
		t.Fatalf("EnsureDataDir() with empty override = %q, want OS default %q", got, def)
	}
}

func TestDevRoot(t *testing.T) {
	t.Setenv("VENOM_DEV_ROOT", "")
	if got := DevRoot(); got != "" {
		t.Fatalf("DevRoot() with unset/empty env = %q, want empty", got)
	}
	want := filepath.Join("C:", "repo")
	t.Setenv("VENOM_DEV_ROOT", want)
	if got := DevRoot(); got != want {
		t.Fatalf("DevRoot() = %q, want %q", got, want)
	}
}
