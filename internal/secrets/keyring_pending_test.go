package secrets

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeRawKeyring(t *testing.T, dir string, raw map[string]any) string {
	t.Helper()
	secretsDir := filepath.Join(dir, "secrets")
	if err := os.MkdirAll(secretsDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	keyringPath := filepath.Join(secretsDir, "keyring.json")
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(keyringPath, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return keyringPath
}

func materialB64(t *testing.T, fill byte) string {
	t.Helper()
	m := make([]byte, 32)
	for i := range m {
		m[i] = fill
	}
	return base64.StdEncoding.EncodeToString(m)
}

func TestLoad_PendingRotation_ValidMarker_Loads(t *testing.T) {
	dir := t.TempDir()
	writeRawKeyring(t, dir, map[string]any{
		"active_key_id": "k_new",
		"keys": map[string]any{
			"k_old": map[string]any{"material": materialB64(t, 1)},
			"k_new": map[string]any{"material": materialB64(t, 2)},
		},
		"pending_rotation": map[string]any{
			"from_key_id": "k_old",
			"to_key_id":   "k_new",
		},
	})

	kr, err := Load(dir, "", false)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if kr.PendingRotation == nil {
		t.Fatalf("PendingRotation is nil, want set")
	}
	if kr.PendingRotation.FromKeyID != "k_old" || kr.PendingRotation.ToKeyID != "k_new" {
		t.Fatalf("PendingRotation = %+v, want {k_old k_new}", kr.PendingRotation)
	}
	if kr.ActiveKeyID != "k_new" {
		t.Fatalf("ActiveKeyID = %q, want k_new", kr.ActiveKeyID)
	}
	if len(kr.Keys) != 2 {
		t.Fatalf("len(Keys) = %d, want 2", len(kr.Keys))
	}
}

func TestLoad_PendingRotation_FromKeyMissing_FailsClosed(t *testing.T) {
	dir := t.TempDir()
	writeRawKeyring(t, dir, map[string]any{
		"active_key_id": "k_new",
		"keys": map[string]any{
			"k_new": map[string]any{"material": materialB64(t, 2)},
		},
		"pending_rotation": map[string]any{
			"from_key_id": "k_old",
			"to_key_id":   "k_new",
		},
	})

	_, err := Load(dir, "", false)
	if !errors.Is(err, ErrKeyringCorrupt) {
		t.Fatalf("Load() error = %v, want ErrKeyringCorrupt", err)
	}
}

func TestLoad_PendingRotation_ToKeyMissing_FailsClosed(t *testing.T) {
	dir := t.TempDir()
	writeRawKeyring(t, dir, map[string]any{
		"active_key_id": "k_old",
		"keys": map[string]any{
			"k_old": map[string]any{"material": materialB64(t, 1)},
		},
		"pending_rotation": map[string]any{
			"from_key_id": "k_old",
			"to_key_id":   "k_new",
		},
	})

	_, err := Load(dir, "", false)
	if !errors.Is(err, ErrKeyringCorrupt) {
		t.Fatalf("Load() error = %v, want ErrKeyringCorrupt", err)
	}
}

func TestLoad_NoPendingRotationField_LeavesPendingRotationNil(t *testing.T) {
	dir := t.TempDir()

	kr, err := Load(dir, "", false)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if kr.PendingRotation != nil {
		t.Fatalf("PendingRotation = %+v, want nil for a freshly created keyring", kr.PendingRotation)
	}
}
