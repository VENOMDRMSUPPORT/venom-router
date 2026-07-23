package secrets

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// newTestKeyring builds an in-memory keyring with a single fresh 32-byte
// active key. It mirrors the shape Load produces without touching disk,
// keeping these crypto tests focused on Encrypt/Decrypt.
func newTestKeyring(t *testing.T) *Keyring {
	t.Helper()
	key := make([]byte, keyLength)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	return &Keyring{
		ActiveKeyID: "k_test",
		Keys:        map[string][]byte{"k_test": key},
	}
}

// identityA is a fully populated record identity used across tests.
func identityA() RecordIdentity {
	return RecordIdentity{
		Purpose:  "credential",
		Provider: "openai",
		Account:  "acct-1",
		Record:   "rec-1",
		Kind:     "api_key",
	}
}

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	kr := newTestKeyring(t)
	id := identityA()

	cases := []struct {
		name      string
		plaintext []byte
	}{
		{name: "empty", plaintext: []byte{}},
		{name: "small", plaintext: []byte("sk-live-secret-value")},
		{name: "binary", plaintext: []byte{0x00, 0xff, 0x10, 0x00, 0x7f}},
		{name: "large", plaintext: make([]byte, 4096)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, err := Encrypt(kr, id, tc.plaintext)
			if err != nil {
				t.Fatalf("Encrypt() error = %v", err)
			}
			if env.KeyID != kr.ActiveKeyID {
				t.Fatalf("env.KeyID = %q, want %q", env.KeyID, kr.ActiveKeyID)
			}
			if len(env.Nonce) == 0 {
				t.Fatalf("env.Nonce is empty")
			}
			if len(env.Ciphertext) == 0 {
				t.Fatalf("env.Ciphertext is empty (must include GCM tag even for empty plaintext)")
			}

			got, err := Decrypt(kr, id, env)
			if err != nil {
				t.Fatalf("Decrypt() error = %v", err)
			}
			if string(got) != string(tc.plaintext) {
				t.Fatalf("round-trip mismatch: got %d bytes, want %d bytes", len(got), len(tc.plaintext))
			}
		})
	}
}

func TestDecrypt_AADMismatch_FailsWithErrDecrypt(t *testing.T) {
	kr := newTestKeyring(t)
	base := identityA()
	plaintext := []byte("bound-to-identity")

	env, err := Encrypt(kr, base, plaintext)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	// Each case differs from base in exactly one field, proving every
	// field of the identity is bound into the AAD.
	cases := []struct {
		name string
		id   RecordIdentity
	}{
		{name: "purpose", id: RecordIdentity{Purpose: "other", Provider: base.Provider, Account: base.Account, Record: base.Record, Kind: base.Kind}},
		{name: "provider", id: RecordIdentity{Purpose: base.Purpose, Provider: "anthropic", Account: base.Account, Record: base.Record, Kind: base.Kind}},
		{name: "account", id: RecordIdentity{Purpose: base.Purpose, Provider: base.Provider, Account: "acct-2", Record: base.Record, Kind: base.Kind}},
		{name: "record", id: RecordIdentity{Purpose: base.Purpose, Provider: base.Provider, Account: base.Account, Record: "rec-2", Kind: base.Kind}},
		{name: "kind", id: RecordIdentity{Purpose: base.Purpose, Provider: base.Provider, Account: base.Account, Record: base.Record, Kind: "oauth_token"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Decrypt(kr, tc.id, env)
			if !errors.Is(err, ErrDecrypt) {
				t.Fatalf("Decrypt() with mismatched %s error = %v, want ErrDecrypt", tc.name, err)
			}
		})
	}
}

func TestDecrypt_CiphertextRelocation_Rejected(t *testing.T) {
	kr := newTestKeyring(t)
	recordA := identityA()
	recordB := RecordIdentity{
		Purpose:  "credential",
		Provider: "openai",
		Account:  "acct-99",
		Record:   "rec-99",
		Kind:     "api_key",
	}

	env, err := Encrypt(kr, recordA, []byte("secret-for-record-A"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	// Attempt to open record A's envelope as though it belonged to record
	// B: AAD binding must reject the relocation.
	if _, err := Decrypt(kr, recordB, env); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("relocated ciphertext Decrypt() error = %v, want ErrDecrypt", err)
	}
	// Sanity: it still opens under its true identity.
	if _, err := Decrypt(kr, recordA, env); err != nil {
		t.Fatalf("Decrypt() under true identity error = %v", err)
	}
}

func TestAAD_LengthPrefixInjective(t *testing.T) {
	kr := newTestKeyring(t)
	// ("ab","c") and ("a","bc") must not collide: a naive concatenation
	// would derive identical AAD, a length-prefixed encoding must not.
	idAB := RecordIdentity{Purpose: "ab", Provider: "c", Account: "x", Record: "y", Kind: "z"}
	idA := RecordIdentity{Purpose: "a", Provider: "bc", Account: "x", Record: "y", Kind: "z"}

	env, err := Encrypt(kr, idAB, []byte("payload"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if _, err := Decrypt(kr, idA, env); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("Decrypt() under colliding-concat identity error = %v, want ErrDecrypt", err)
	}
}

func TestEncrypt_NonceUniqueness(t *testing.T) {
	kr := newTestKeyring(t)
	id := identityA()
	plaintext := []byte("same-plaintext-every-time")

	const iterations = 512
	seen := make(map[string]struct{}, iterations)
	for i := 0; i < iterations; i++ {
		env, err := Encrypt(kr, id, plaintext)
		if err != nil {
			t.Fatalf("Encrypt() iteration %d error = %v", i, err)
		}
		key := string(env.Nonce)
		if _, dup := seen[key]; dup {
			t.Fatalf("duplicate nonce produced at iteration %d", i)
		}
		seen[key] = struct{}{}
	}
	if len(seen) != iterations {
		t.Fatalf("distinct nonces = %d, want %d", len(seen), iterations)
	}
}

func TestDecrypt_UnknownKeyID_FailsClosed(t *testing.T) {
	kr := newTestKeyring(t)
	id := identityA()

	env, err := Encrypt(kr, id, []byte("payload"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	env.KeyID = "k_does_not_exist"

	if _, err := Decrypt(kr, id, env); !errors.Is(err, ErrUnknownKeyID) {
		t.Fatalf("Decrypt() with unknown key_id error = %v, want ErrUnknownKeyID", err)
	}
}

func TestDecrypt_Tampered_FailsWithErrDecrypt(t *testing.T) {
	kr := newTestKeyring(t)
	id := identityA()

	encrypt := func(t *testing.T) Envelope {
		t.Helper()
		env, err := Encrypt(kr, id, []byte("tamper-evident-payload"))
		if err != nil {
			t.Fatalf("Encrypt() error = %v", err)
		}
		return env
	}

	t.Run("ciphertext byte flipped", func(t *testing.T) {
		env := encrypt(t)
		env.Ciphertext[0] ^= 0x01
		if _, err := Decrypt(kr, id, env); !errors.Is(err, ErrDecrypt) {
			t.Fatalf("Decrypt() of tampered ciphertext error = %v, want ErrDecrypt", err)
		}
	})

	t.Run("nonce byte flipped", func(t *testing.T) {
		env := encrypt(t)
		env.Nonce[0] ^= 0x01
		if _, err := Decrypt(kr, id, env); !errors.Is(err, ErrDecrypt) {
			t.Fatalf("Decrypt() with tampered nonce error = %v, want ErrDecrypt", err)
		}
	})

	t.Run("nonce truncated", func(t *testing.T) {
		env := encrypt(t)
		env.Nonce = env.Nonce[:len(env.Nonce)-1]
		if _, err := Decrypt(kr, id, env); !errors.Is(err, ErrDecrypt) {
			t.Fatalf("Decrypt() with truncated nonce error = %v, want ErrDecrypt", err)
		}
	})

	t.Run("ciphertext emptied", func(t *testing.T) {
		env := encrypt(t)
		env.Ciphertext = nil
		if _, err := Decrypt(kr, id, env); !errors.Is(err, ErrDecrypt) {
			t.Fatalf("Decrypt() with empty ciphertext error = %v, want ErrDecrypt", err)
		}
	})
}

func TestEnvelope_JSONRoundTrip_ExcludesAADFields(t *testing.T) {
	kr := newTestKeyring(t)
	// Distinctive field values so we can assert none leak into the JSON.
	id := RecordIdentity{
		Purpose:  "PURPOSEMARKER",
		Provider: "PROVIDERMARKER",
		Account:  "ACCOUNTMARKER",
		Record:   "RECORDMARKER",
		Kind:     "KINDMARKER",
	}

	env, err := Encrypt(kr, id, []byte("payload"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	got := string(data)
	for _, marker := range []string{"PURPOSEMARKER", "PROVIDERMARKER", "ACCOUNTMARKER", "RECORDMARKER", "KINDMARKER"} {
		if strings.Contains(got, marker) {
			t.Fatalf("envelope JSON leaked AAD field %q: %s", marker, got)
		}
	}
	// The persisted shape is exactly key_id, nonce, ciphertext.
	for _, field := range []string{`"key_id"`, `"nonce"`, `"ciphertext"`} {
		if !strings.Contains(got, field) {
			t.Fatalf("envelope JSON missing field %s: %s", field, got)
		}
	}

	var decoded Envelope
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	plaintext, err := Decrypt(kr, id, decoded)
	if err != nil {
		t.Fatalf("Decrypt() after JSON round-trip error = %v", err)
	}
	if string(plaintext) != "payload" {
		t.Fatalf("post-JSON round-trip plaintext = %q, want %q", plaintext, "payload")
	}
}
