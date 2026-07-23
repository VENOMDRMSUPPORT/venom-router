package platform

import (
	"os"
	"testing"
)

func TestEncryptionKeyOverride_Present(t *testing.T) {
	t.Setenv("VENOM_ENCRYPTION_KEY", "some-value")

	value, present := EncryptionKeyOverride()
	if !present {
		t.Fatalf("present = false, want true")
	}
	if value != "some-value" {
		t.Fatalf("value = %q, want %q", value, "some-value")
	}
}

func TestEncryptionKeyOverride_PresentButEmpty(t *testing.T) {
	t.Setenv("VENOM_ENCRYPTION_KEY", "")

	value, present := EncryptionKeyOverride()
	if !present {
		t.Fatalf("present = false, want true (set but empty is still 'present')")
	}
	if value != "" {
		t.Fatalf("value = %q, want empty", value)
	}
}

func TestEncryptionKeyOverride_Absent(t *testing.T) {
	if orig, ok := os.LookupEnv("VENOM_ENCRYPTION_KEY"); ok {
		t.Cleanup(func() { _ = os.Setenv("VENOM_ENCRYPTION_KEY", orig) })
	}
	if err := os.Unsetenv("VENOM_ENCRYPTION_KEY"); err != nil {
		t.Fatalf("unsetenv: %v", err)
	}

	_, present := EncryptionKeyOverride()
	if present {
		t.Fatalf("present = true, want false")
	}
}
