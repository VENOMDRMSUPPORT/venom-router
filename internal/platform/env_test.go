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

func TestEnvPresent(t *testing.T) {
	const name = "VENOM_TEST_ENV_PRESENT_CHECK"

	if orig, ok := os.LookupEnv(name); ok {
		t.Cleanup(func() { _ = os.Setenv(name, orig) })
	} else {
		t.Cleanup(func() { _ = os.Unsetenv(name) })
	}

	if err := os.Unsetenv(name); err != nil {
		t.Fatalf("unsetenv: %v", err)
	}
	if EnvPresent(name) {
		t.Fatalf("EnvPresent(unset) = true, want false")
	}

	t.Setenv(name, "irrelevant-value")
	if !EnvPresent(name) {
		t.Fatalf("EnvPresent(set) = false, want true")
	}
}

func TestAntigravityOAuthClientCredentials_BothSet(t *testing.T) {
	t.Setenv("VENOM_ANTIGRAVITY_CLIENT_ID", "test-client-id")
	t.Setenv("VENOM_ANTIGRAVITY_CLIENT_SECRET", "test-client-secret")

	id, secret, ok := AntigravityOAuthClientCredentials()
	if !ok {
		t.Fatalf("ok = false, want true when both vars are set and non-empty")
	}
	if id != "test-client-id" || secret != "test-client-secret" {
		t.Fatalf("id=%q secret=%q, want test-client-id/test-client-secret", id, secret)
	}
}

func TestAntigravityOAuthClientCredentials_MissingEither(t *testing.T) {
	t.Setenv("VENOM_ANTIGRAVITY_CLIENT_ID", "only-id-set")
	if err := os.Unsetenv("VENOM_ANTIGRAVITY_CLIENT_SECRET"); err != nil {
		t.Fatalf("unsetenv: %v", err)
	}

	if _, _, ok := AntigravityOAuthClientCredentials(); ok {
		t.Fatalf("ok = true with only the client id set, want false")
	}

	if err := os.Unsetenv("VENOM_ANTIGRAVITY_CLIENT_ID"); err != nil {
		t.Fatalf("unsetenv: %v", err)
	}
	t.Setenv("VENOM_ANTIGRAVITY_CLIENT_SECRET", "only-secret-set")
	if _, _, ok := AntigravityOAuthClientCredentials(); ok {
		t.Fatalf("ok = true with only the client secret set, want false")
	}
}

func TestAntigravityOAuthClientCredentials_SetButEmptyTreatedAsMissing(t *testing.T) {
	t.Setenv("VENOM_ANTIGRAVITY_CLIENT_ID", "")
	t.Setenv("VENOM_ANTIGRAVITY_CLIENT_SECRET", "a-secret")

	if _, _, ok := AntigravityOAuthClientCredentials(); ok {
		t.Fatalf("ok = true with an empty-string client id, want false")
	}
}

// TestOpenCodeZenE2ECredential_Present and _Absent are the P2b-TEST-003
// C.2 accessor's own unit tests, mirroring TestEncryptionKeyOverride_*
// above exactly (same present/absent shape).
func TestOpenCodeZenE2ECredential_Present(t *testing.T) {
	t.Setenv("VENOM_E2E_OPENCODE_ZEN_KEY", "sk-real-free-tier-key")

	value, present := OpenCodeZenE2ECredential()
	if !present {
		t.Fatalf("present = false, want true")
	}
	if value != "sk-real-free-tier-key" {
		t.Fatalf("value = %q, want %q", value, "sk-real-free-tier-key")
	}
}

func TestOpenCodeZenE2ECredential_Absent(t *testing.T) {
	if orig, ok := os.LookupEnv("VENOM_E2E_OPENCODE_ZEN_KEY"); ok {
		t.Cleanup(func() { _ = os.Setenv("VENOM_E2E_OPENCODE_ZEN_KEY", orig) })
	}
	if err := os.Unsetenv("VENOM_E2E_OPENCODE_ZEN_KEY"); err != nil {
		t.Fatalf("unsetenv: %v", err)
	}

	_, present := OpenCodeZenE2ECredential()
	if present {
		t.Fatalf("present = true, want false")
	}
}
