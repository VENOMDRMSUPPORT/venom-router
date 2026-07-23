package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
)

// ErrEncrypt is returned when authenticated encryption cannot be
// performed (e.g. the active key is unusable or the system CSPRNG fails).
// Its message never contains key, plaintext, or ciphertext bytes.
var ErrEncrypt = errors.New("secrets: encryption failed")

// ErrDecrypt is returned for every authentication failure on decrypt:
// a wrong AAD identity, a relocated ciphertext, or a tampered
// nonce/ciphertext. The failure is deliberately indistinguishable and
// carries no secret material, so callers cannot use it as an oracle.
var ErrDecrypt = errors.New("secrets: decryption failed")

// ErrUnknownKeyID is returned when an Envelope names a key_id that the
// keyring does not hold. Decrypt fails closed rather than guessing a key.
var ErrUnknownKeyID = errors.New("secrets: envelope key_id not present in keyring")

// ErrInvalidKey is returned when the key material selected for an
// operation is not exactly keyLength (32) bytes. Its message never
// contains the key bytes.
var ErrInvalidKey = errors.New("secrets: key material must be 32 bytes")

// Envelope is the persisted form of one authenticated-encryption result.
// It stores exactly the key_id that encrypted it, the per-encryption
// nonce, and the ciphertext (GCM ciphertext with its appended auth tag).
// It intentionally records nothing about the record identity (the AAD is
// derived, never stored) and nothing about the plaintext. This shape
// defines the columns M2 (P2b) will persist for credential rows.
type Envelope struct {
	KeyID      string `json:"key_id"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

// RecordIdentity is the tuple that authenticated encryption binds a
// ciphertext to. Its five fields identify the record a secret belongs to;
// they are folded into the GCM additional authenticated data (AAD) so a
// ciphertext can only be opened for the exact record it was sealed for.
// The identity is never stored in the Envelope: it is re-supplied by the
// caller at decrypt time and must match bit-for-bit.
type RecordIdentity struct {
	Purpose  string
	Provider string
	Account  string
	Record   string
	Kind     string
}

// aad derives the additional authenticated data for this identity with an
// injective, length-prefixed encoding: for each field, in fixed order, a
// 4-byte big-endian length followed by the field bytes. Length-prefixing
// makes the encoding unambiguous, so distinct tuples can never collide —
// e.g. ("ab","c") and ("a","bc") derive different AAD. The same bytes are
// derived identically at encrypt and decrypt; they are never persisted.
func (id RecordIdentity) aad() []byte {
	fields := [...]string{id.Purpose, id.Provider, id.Account, id.Record, id.Kind}

	size := 0
	for _, f := range fields {
		size += 4 + len(f)
	}

	out := make([]byte, 0, size)
	var lenPrefix [4]byte
	for _, f := range fields {
		binary.BigEndian.PutUint32(lenPrefix[:], uint32(len(f)))
		out = append(out, lenPrefix[:]...)
		out = append(out, f...)
	}
	return out
}

// Encrypt seals plaintext with AES-256-GCM under the keyring's active
// key, binding the ciphertext to id via the derived AAD. A fresh nonce is
// drawn from crypto/rand on every call. The returned Envelope records the
// active key_id, that nonce, and the ciphertext; the identity is not
// stored. Errors never contain key, plaintext, or ciphertext bytes.
func Encrypt(kr *Keyring, id RecordIdentity, plaintext []byte) (Envelope, error) {
	key := kr.ActiveKey()
	if len(key) != keyLength {
		return Envelope{}, fmt.Errorf("%w: active key", ErrInvalidKey)
	}

	gcm, err := newGCM(key)
	if err != nil {
		return Envelope{}, fmt.Errorf("%w: %w", ErrEncrypt, err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return Envelope{}, fmt.Errorf("%w: nonce generation failed", ErrEncrypt)
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, id.aad())
	return Envelope{
		KeyID:      kr.ActiveKeyID,
		Nonce:      nonce,
		Ciphertext: ciphertext,
	}, nil
}

// Decrypt opens env under the key named by env.KeyID, verifying the AAD
// derived from id. It fails closed: an unknown key_id yields
// ErrUnknownKeyID, and any authentication failure — wrong identity,
// relocated ciphertext, or tampered nonce/ciphertext — yields ErrDecrypt.
// Malformed input (wrong-length key, wrong-length nonce, empty
// ciphertext) is rejected before any cryptographic work. No key,
// plaintext, ciphertext, or AAD value ever appears in a returned error.
func Decrypt(kr *Keyring, id RecordIdentity, env Envelope) ([]byte, error) {
	key, ok := kr.Keys[env.KeyID]
	if !ok {
		return nil, ErrUnknownKeyID
	}
	if len(key) != keyLength {
		return nil, fmt.Errorf("%w: keyring key", ErrInvalidKey)
	}

	gcm, err := newGCM(key)
	if err != nil {
		return nil, ErrDecrypt
	}

	if len(env.Nonce) != gcm.NonceSize() {
		return nil, fmt.Errorf("%w: malformed nonce", ErrDecrypt)
	}
	if len(env.Ciphertext) == 0 {
		return nil, fmt.Errorf("%w: missing ciphertext", ErrDecrypt)
	}

	plaintext, err := gcm.Open(nil, env.Nonce, env.Ciphertext, id.aad())
	if err != nil {
		return nil, ErrDecrypt
	}
	return plaintext, nil
}

// newGCM constructs an AES-256-GCM AEAD from a 32-byte key. Callers guard
// the key length first, so in practice neither step fails; the errors are
// still surfaced (never with key bytes) so the caller can fail closed.
func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("secrets: cipher construction failed")
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("secrets: AEAD construction failed")
	}
	return gcm, nil
}
