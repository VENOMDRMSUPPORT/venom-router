package secrets

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// validEnvKey is a base64-standard-encoded 32-byte value, matching the
// documented VENOM_ENCRYPTION_KEY encoding.
func validEnvKey(t *testing.T) string {
	t.Helper()
	material := make([]byte, 32)
	for i := range material {
		material[i] = byte(i)
	}
	return base64.StdEncoding.EncodeToString(material)
}

func TestLoad_CreateThenReload_RoundTrip(t *testing.T) {
	dir := t.TempDir()

	first, err := Load(dir, "", false)
	if err != nil {
		t.Fatalf("first Load() error = %v", err)
	}
	if first.ActiveKeyID == "" {
		t.Fatalf("first.ActiveKeyID is empty")
	}
	if len(first.Keys[first.ActiveKeyID]) != 32 {
		t.Fatalf("first active key material length = %d, want 32", len(first.Keys[first.ActiveKeyID]))
	}

	second, err := Load(dir, "", false)
	if err != nil {
		t.Fatalf("second Load() error = %v", err)
	}
	if second.ActiveKeyID != first.ActiveKeyID {
		t.Fatalf("second.ActiveKeyID = %q, want %q (reload must not regenerate)", second.ActiveKeyID, first.ActiveKeyID)
	}
	if string(second.Keys[second.ActiveKeyID]) != string(first.Keys[first.ActiveKeyID]) {
		t.Fatalf("reload produced different key material than first create")
	}
}

func TestLoad_AbsentFile_CreatesFreshKey(t *testing.T) {
	dir := t.TempDir()
	keyringPath := filepath.Join(dir, "secrets", "keyring.json")

	if _, err := os.Stat(keyringPath); !os.IsNotExist(err) {
		t.Fatalf("keyring file already exists before Load()")
	}

	kr, err := Load(dir, "", false)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if _, err := os.Stat(keyringPath); err != nil {
		t.Fatalf("keyring file was not created: %v", err)
	}
	if len(kr.Keys) != 1 {
		t.Fatalf("len(kr.Keys) = %d, want 1", len(kr.Keys))
	}
}

func TestLoad_EnvOverride_Valid_FileNotCreatedOrRead(t *testing.T) {
	dir := t.TempDir()
	keyringPath := filepath.Join(dir, "secrets", "keyring.json")
	envKey := validEnvKey(t)

	kr, err := Load(dir, envKey, true)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if kr.ActiveKeyID != EnvKeyID {
		t.Fatalf("ActiveKeyID = %q, want %q", kr.ActiveKeyID, EnvKeyID)
	}
	want, _ := base64.StdEncoding.DecodeString(envKey)
	if string(kr.Keys[EnvKeyID]) != string(want) {
		t.Fatalf("env key material mismatch")
	}

	if _, err := os.Stat(keyringPath); !os.IsNotExist(err) {
		t.Fatalf("keyring file must not be created when env override is used, stat err = %v", err)
	}
}

func TestLoad_EnvOverride_WrongLength_FailsClosed(t *testing.T) {
	dir := t.TempDir()
	shortKey := base64.StdEncoding.EncodeToString([]byte("too-short"))

	_, err := Load(dir, shortKey, true)
	if !errors.Is(err, ErrInvalidEnvKey) {
		t.Fatalf("Load() error = %v, want ErrInvalidEnvKey", err)
	}
}

func TestLoad_EnvOverride_Malformed_FailsClosed(t *testing.T) {
	dir := t.TempDir()

	_, err := Load(dir, "not-valid-base64!!!", true)
	if !errors.Is(err, ErrInvalidEnvKey) {
		t.Fatalf("Load() error = %v, want ErrInvalidEnvKey", err)
	}
}

func TestLoad_EnvOverride_PresentButEmpty_FallsBackToFile(t *testing.T) {
	dir := t.TempDir()

	kr, err := Load(dir, "", true)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if kr.ActiveKeyID == EnvKeyID {
		t.Fatalf("empty env override must not be treated as present")
	}
}

func TestLoad_CorruptFile_FailsClosed_AndDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	secretsDir := filepath.Join(dir, "secrets")
	if err := os.MkdirAll(secretsDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	keyringPath := filepath.Join(secretsDir, "keyring.json")
	corrupt := []byte("{ this is not valid json")
	if err := os.WriteFile(keyringPath, corrupt, 0o600); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}

	_, err := Load(dir, "", false)
	if !errors.Is(err, ErrKeyringCorrupt) {
		t.Fatalf("Load() error = %v, want ErrKeyringCorrupt", err)
	}

	after, readErr := os.ReadFile(keyringPath)
	if readErr != nil {
		t.Fatalf("re-read keyring file: %v", readErr)
	}
	if string(after) != string(corrupt) {
		t.Fatalf("corrupt keyring file was overwritten, want untouched")
	}
}

func TestLoad_ActiveKeyIDMissingFromKeySet_FailsClosed(t *testing.T) {
	dir := t.TempDir()
	secretsDir := filepath.Join(dir, "secrets")
	if err := os.MkdirAll(secretsDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	keyringPath := filepath.Join(secretsDir, "keyring.json")

	broken := map[string]any{
		"active_key_id": "does-not-exist",
		"keys":          map[string]any{},
	}
	data, err := json.Marshal(broken)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(keyringPath, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, loadErr := Load(dir, "", false)
	if !errors.Is(loadErr, ErrKeyringCorrupt) {
		t.Fatalf("Load() error = %v, want ErrKeyringCorrupt", loadErr)
	}
}

func TestLoad_WrongKeyLength_FailsClosed(t *testing.T) {
	dir := t.TempDir()
	secretsDir := filepath.Join(dir, "secrets")
	if err := os.MkdirAll(secretsDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	keyringPath := filepath.Join(secretsDir, "keyring.json")

	broken := map[string]any{
		"active_key_id": "k_bad",
		"keys": map[string]any{
			"k_bad": map[string]any{
				"material": base64.StdEncoding.EncodeToString([]byte("short")),
			},
		},
	}
	data, err := json.Marshal(broken)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(keyringPath, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, loadErr := Load(dir, "", false)
	if !errors.Is(loadErr, ErrKeyringCorrupt) {
		t.Fatalf("Load() error = %v, want ErrKeyringCorrupt", loadErr)
	}
}

func TestLoad_EmptyFile_FailsClosed(t *testing.T) {
	dir := t.TempDir()
	secretsDir := filepath.Join(dir, "secrets")
	if err := os.MkdirAll(secretsDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	keyringPath := filepath.Join(secretsDir, "keyring.json")
	if err := os.WriteFile(keyringPath, []byte(""), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, loadErr := Load(dir, "", false)
	if !errors.Is(loadErr, ErrKeyringCorrupt) {
		t.Fatalf("Load() error = %v, want ErrKeyringCorrupt", loadErr)
	}
}

func TestLoad_PresentValidFile_LoadsSameMaterial(t *testing.T) {
	dir := t.TempDir()
	secretsDir := filepath.Join(dir, "secrets")
	if err := os.MkdirAll(secretsDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	keyringPath := filepath.Join(secretsDir, "keyring.json")

	material := make([]byte, 32)
	for i := range material {
		material[i] = byte(255 - i)
	}
	valid := map[string]any{
		"active_key_id": "k_fixed",
		"keys": map[string]any{
			"k_fixed": map[string]any{
				"material": base64.StdEncoding.EncodeToString(material),
			},
		},
	}
	data, err := json.Marshal(valid)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(keyringPath, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	kr, loadErr := Load(dir, "", false)
	if loadErr != nil {
		t.Fatalf("Load() error = %v", loadErr)
	}
	if kr.ActiveKeyID != "k_fixed" {
		t.Fatalf("ActiveKeyID = %q, want %q", kr.ActiveKeyID, "k_fixed")
	}
	if string(kr.Keys["k_fixed"]) != string(material) {
		t.Fatalf("loaded material mismatch")
	}
}

func TestLoad_NeverGeneratesKeyMaterialFromMathRandLikeSource(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	kr1, err := Load(dir1, "", false)
	if err != nil {
		t.Fatalf("Load(dir1) error = %v", err)
	}
	kr2, err := Load(dir2, "", false)
	if err != nil {
		t.Fatalf("Load(dir2) error = %v", err)
	}
	if kr1.ActiveKeyID == kr2.ActiveKeyID {
		t.Fatalf("two independent creates produced the same key_id %q", kr1.ActiveKeyID)
	}
	if string(kr1.Keys[kr1.ActiveKeyID]) == string(kr2.Keys[kr2.ActiveKeyID]) {
		t.Fatalf("two independent creates produced identical key material")
	}
}
