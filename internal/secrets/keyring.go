package secrets

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// keyLength is the AES-256 master key size in bytes.
const keyLength = 32

// secretsDirName and keyringFileName make up the on-disk location of the
// keyring, relative to the caller-supplied dataDir:
// <dataDir>/secrets/keyring.json.
const (
	secretsDirName  = "secrets"
	keyringFileName = "keyring.json"
	tmpFileName     = ".keyring.json.tmp"
)

// secretsDirPerm and keyringFilePerm mirror the owner-only rationale
// already established in internal/platform.EnsureDataDir (dirPerm
// 0o700): on Linux these are enforced by the filesystem; on Windows a
// Go file-mode is not meaningful, and per-user isolation instead comes
// from %LOCALAPPDATA% already being a per-user path (the same stance
// internal/platform's DataDir documents).
const (
	secretsDirPerm  = 0o700
	keyringFilePerm = 0o600
)

// EnvKeyID is the fixed key_id used for the master key when it is
// sourced from the VENOM_ENCRYPTION_KEY environment override rather than
// the on-disk keyring. It never appears in keyring.json.
const EnvKeyID = "env"

// ErrInvalidEnvKey is returned when VENOM_ENCRYPTION_KEY is present and
// non-empty but does not decode (base64 standard encoding) to exactly
// keyLength bytes. Load never falls back to the file in this case.
var ErrInvalidEnvKey = errors.New("secrets: VENOM_ENCRYPTION_KEY must be the base64 encoding of a 32-byte key")

// ErrKeyringCorrupt is returned when the on-disk keyring file exists but
// is empty, unparseable, or fails validation (missing active_key_id,
// active_key_id absent from the key set, or wrong-length key material).
// Load never overwrites a file that fails for this reason.
var ErrKeyringCorrupt = errors.New("secrets: keyring file is corrupt")

// ErrKeyringUnavailable is returned when the on-disk keyring file exists
// but could not be read for a reason other than not existing (e.g. a
// permissions error). Load fails closed in this case too.
var ErrKeyringUnavailable = errors.New("secrets: keyring file could not be read")

// Keyring is the in-memory master keyring for a run: the active key_id
// and every key currently known, keyed by key_id. Only Load builds a
// Keyring; the zero value is not meaningful.
type Keyring struct {
	ActiveKeyID string
	Keys        map[string][]byte
}

// ActiveKey returns the master key material for the keyring's active
// key_id.
func (k *Keyring) ActiveKey() []byte {
	return k.Keys[k.ActiveKeyID]
}

// fileFormat is the on-disk JSON shape of keyring.json. It holds a set
// of key_id -> key entries plus which one is active, so that key
// rotation (SEC-003) can add entries later without changing the format:
// this unit always writes and expects exactly one entry.
type fileFormat struct {
	ActiveKeyID string              `json:"active_key_id"`
	Keys        map[string]keyEntry `json:"keys"`
}

// keyEntry holds one key's material, base64-standard-encoded (RFC 4648
// with padding) — the same encoding VENOM_ENCRYPTION_KEY uses.
type keyEntry struct {
	Material string `json:"material"`
}

// Load resolves the master keyring for one run.
//
// envValue and envPresent are the (value, present) pair from
// platform.EncryptionKeyOverride, injected by the caller — this package
// reads no environment variables itself.
//
// Resolution order:
//
//  1. envPresent && envValue != "": the env override is the sole source
//     of the master key. It must decode to exactly 32 bytes or Load
//     fails closed with ErrInvalidEnvKey; the keyring file is neither
//     read nor written in this branch.
//  2. Otherwise, <dataDir>/secrets/keyring.json is consulted:
//     - absent: a fresh key is generated and the file is created.
//     - present and valid: it is loaded into memory.
//     - present but corrupt/invalid: Load fails closed with
//     ErrKeyringCorrupt (or ErrKeyringUnavailable for a read error)
//     and never overwrites the existing file.
func Load(dataDir, envValue string, envPresent bool) (*Keyring, error) {
	if envPresent && envValue != "" {
		material, err := decodeMaterial(envValue)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidEnvKey, err)
		}
		return &Keyring{
			ActiveKeyID: EnvKeyID,
			Keys:        map[string][]byte{EnvKeyID: material},
		}, nil
	}

	secretsDir := filepath.Join(dataDir, secretsDirName)
	keyringPath := filepath.Join(secretsDir, keyringFileName)

	data, err := os.ReadFile(keyringPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return createKeyring(secretsDir, keyringPath)
	case err != nil:
		return nil, fmt.Errorf("%w: %w", ErrKeyringUnavailable, err)
	}

	return parseKeyring(data)
}

func parseKeyring(data []byte) (*Keyring, error) {
	var ff fileFormat
	if err := json.Unmarshal(data, &ff); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrKeyringCorrupt, err)
	}
	if ff.ActiveKeyID == "" {
		return nil, fmt.Errorf("%w: active_key_id is empty", ErrKeyringCorrupt)
	}

	keys := make(map[string][]byte, len(ff.Keys))
	for id, entry := range ff.Keys {
		material, err := decodeMaterial(entry.Material)
		if err != nil {
			return nil, fmt.Errorf("%w: key %q: %w", ErrKeyringCorrupt, id, err)
		}
		keys[id] = material
	}

	if _, ok := keys[ff.ActiveKeyID]; !ok {
		return nil, fmt.Errorf("%w: active_key_id %q not present in keys", ErrKeyringCorrupt, ff.ActiveKeyID)
	}

	return &Keyring{ActiveKeyID: ff.ActiveKeyID, Keys: keys}, nil
}

func decodeMaterial(s string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("invalid base64: %w", err)
	}
	if len(b) != keyLength {
		return nil, fmt.Errorf("expected %d bytes, got %d", keyLength, len(b))
	}
	return b, nil
}

func createKeyring(secretsDir, keyringPath string) (*Keyring, error) {
	material := make([]byte, keyLength)
	if _, err := rand.Read(material); err != nil {
		return nil, fmt.Errorf("secrets: generate key material: %w", err)
	}
	keyID, err := generateKeyID()
	if err != nil {
		return nil, fmt.Errorf("secrets: generate key id: %w", err)
	}

	ff := fileFormat{
		ActiveKeyID: keyID,
		Keys: map[string]keyEntry{
			keyID: {Material: base64.StdEncoding.EncodeToString(material)},
		},
	}
	data, err := json.MarshalIndent(&ff, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("secrets: encode keyring: %w", err)
	}

	if err := writeFileAtomic(secretsDir, keyringPath, data); err != nil {
		return nil, err
	}

	return &Keyring{
		ActiveKeyID: keyID,
		Keys:        map[string][]byte{keyID: material},
	}, nil
}

// generateKeyID returns a fresh, non-secret identifier for a newly
// generated key: "k_" followed by 16 hex characters from crypto/rand.
// It is stored alongside — never instead of — the key material, and
// never carries any information about the key itself.
func generateKeyID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "k_" + hex.EncodeToString(b), nil
}

// writeFileAtomic creates secretsDir (owner-only) if needed, writes data
// to a temp file in that same directory with owner-only perms from
// creation, and renames it into place — so a crash mid-write can never
// leave a half-written keyring, and the file is never briefly
// world-readable.
func writeFileAtomic(secretsDir, finalPath string, data []byte) error {
	if err := os.MkdirAll(secretsDir, secretsDirPerm); err != nil {
		return fmt.Errorf("secrets: create secrets dir: %w", err)
	}

	tmpPath := filepath.Join(secretsDir, tmpFileName)
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, keyringFilePerm)
	if err != nil {
		return fmt.Errorf("secrets: create temp keyring file: %w", err)
	}

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("secrets: write temp keyring file: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("secrets: sync temp keyring file: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("secrets: close temp keyring file: %w", err)
	}

	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("secrets: rename keyring file into place: %w", err)
	}
	return nil
}
