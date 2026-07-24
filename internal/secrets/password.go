package secrets

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Argon2Time, Argon2MemKiB, Argon2Threads, and Argon2KeyLen are the
// documented owner-password KDF parameters (09 §5.1). They are stored
// alongside every derived hash (see OwnerPasswordHash) so a later
// parameter bump can re-hash on next login without invalidating
// already-stored rows.
const (
	Argon2Time    uint32 = 3
	Argon2MemKiB  uint32 = 65536 // 64 MiB
	Argon2Threads uint8  = 4
	Argon2KeyLen  uint32 = 32
)

// saltLen is the per-install random salt length in bytes.
const saltLen = 16

// MinPasswordLength is the minimum owner-password length this unit
// enforces (09 §5.1's "min length/entropy policy"). This is a length
// floor, not a full entropy estimator; a fuller strength check is out of
// this unit's scope.
const MinPasswordLength = 12

// ErrPasswordTooShort is returned by ValidateOwnerPassword /
// DeriveOwnerPasswordHash when the password is shorter than
// MinPasswordLength. Its message never echoes the password itself.
var ErrPasswordTooShort = fmt.Errorf("secrets: password must be at least %d characters", MinPasswordLength)

// OwnerPasswordHash is the persisted Argon2id output: the derived key,
// the per-install salt, and the exact KDF parameters used to produce it —
// never the password itself.
type OwnerPasswordHash struct {
	Hash    []byte
	Salt    []byte
	Time    uint32
	MemKiB  uint32
	Threads uint8
	KeyLen  uint32
}

// ValidateOwnerPassword enforces the minimum length policy. Its returned
// error never includes password.
func ValidateOwnerPassword(password string) error {
	if utf8.RuneCountInString(password) < MinPasswordLength {
		return ErrPasswordTooShort
	}
	return nil
}

// DeriveOwnerPasswordHash validates password, generates a fresh
// per-install random salt (crypto/rand), and derives the Argon2id hash
// using the documented parameters. password is read only for the
// duration of this call; this function does not store, log, or return it
// in any form — only the derived OwnerPasswordHash leaves this function.
func DeriveOwnerPasswordHash(password string) (OwnerPasswordHash, error) {
	if err := ValidateOwnerPassword(password); err != nil {
		return OwnerPasswordHash{}, err
	}

	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return OwnerPasswordHash{}, errors.New("secrets: generate salt failed")
	}

	hash := argon2.IDKey([]byte(password), salt, Argon2Time, Argon2MemKiB, Argon2Threads, Argon2KeyLen)

	return OwnerPasswordHash{
		Hash:    hash,
		Salt:    salt,
		Time:    Argon2Time,
		MemKiB:  Argon2MemKiB,
		Threads: Argon2Threads,
		KeyLen:  Argon2KeyLen,
	}, nil
}

// VerifyOwnerPassword recomputes the Argon2id hash for password using
// stored's own salt and KDF parameters (so a future parameter bump still
// verifies rows derived under the old parameters) and compares it against
// stored.Hash in constant time via crypto/subtle. It returns a bare bool
// — never a distinguishable error for "wrong password" versus anything
// else — so there is no error-branching surface that could leak which
// case occurred.
func VerifyOwnerPassword(password string, stored OwnerPasswordHash) bool {
	candidate := argon2.IDKey([]byte(password), stored.Salt, stored.Time, stored.MemKiB, stored.Threads, stored.KeyLen)
	return subtle.ConstantTimeCompare(candidate, stored.Hash) == 1
}
