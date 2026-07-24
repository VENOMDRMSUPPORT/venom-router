package secrets

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"
)

// sessionHandleBytes is the raw entropy length (bytes) of a minted opaque
// session handle (09 §5.2: "high-entropy"). 32 bytes = 256 bits.
const sessionHandleBytes = 32

// DefaultIdleTTL and DefaultAbsoluteTTL are the documented session
// lifetime defaults (09 §5.3): idle timeout 30 minutes (sliding),
// absolute lifetime 12 hours (hard cap, never extended by activity).
// Enforcing these on subsequent requests is SEC-003's job; this unit only
// computes the initial expiry timestamps at session creation.
const (
	DefaultIdleTTL     = 30 * time.Minute
	DefaultAbsoluteTTL = 12 * time.Hour
)

// OwnerSession is a freshly minted session. Handle is the opaque value
// that becomes the cookie's value — returned to the caller exactly once,
// here, and never persisted by this package or any caller. TokenHash is
// the SHA-256 verifier of Handle, the only thing 09 §5.2 permits storing
// in owner_sessions ("store only its hash/verifier").
//
// SHA-256 (not Argon2id) is deliberate: Handle already carries 256 bits
// of crypto/rand entropy, so the hash only needs to be a one-way,
// collision-resistant map from handle to storable bytes — unlike a
// password, there is no low-entropy secret here for a slow, memory-hard
// KDF to protect against brute force.
type OwnerSession struct {
	Handle    string
	TokenHash []byte
}

// MintOwnerSession generates a fresh high-entropy opaque session handle
// (crypto/rand) and its verifier hash. The raw Handle must be handed to
// the caller (as the session cookie's value) and never itself written to
// storage.
func MintOwnerSession() (OwnerSession, error) {
	raw := make([]byte, sessionHandleBytes)
	if _, err := rand.Read(raw); err != nil {
		return OwnerSession{}, errors.New("secrets: generate session handle failed")
	}

	handle := base64.RawURLEncoding.EncodeToString(raw)
	return OwnerSession{Handle: handle, TokenHash: HashSessionHandle(handle)}, nil
}

// HashSessionHandle computes the verifier hash for a raw handle — e.g.
// one read back from an incoming cookie — for the caller to look up
// against owner_sessions.token_hash. Later session-consuming units
// (login, session-check) reuse this rather than duplicating the hash
// choice.
func HashSessionHandle(handle string) []byte {
	sum := sha256.Sum256([]byte(handle))
	return sum[:]
}
