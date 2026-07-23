package secrets

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
)

// RewrapRow is one stored ciphertext row a rotation re-wraps: the
// opaque row id a CiphertextStore understands, the RecordIdentity its
// Envelope is bound to (needed to Decrypt it), and its current
// Envelope (always under the rotation's FromKeyID).
type RewrapRow struct {
	ID       string
	Identity RecordIdentity
	Envelope Envelope
}

// CiphertextStore is the minimal seam RewrapAll needs over wherever
// ciphertext actually lives. The real, credential-table-backed
// implementation lands in P2b once M2's columns exist; this unit is
// storage-shape-agnostic on purpose so it is not rewritten then. Its
// only production consumer is rewrap, below.
//
// RewrapAll MUST perform the entire scan-and-update as one atomic
// operation — a real implementation wraps it in a single SQL
// transaction. It is handed every row currently stored under
// fromKeyID, and for each calls rewrap to obtain that row's new
// Envelope (already sealed under the keyring's new active key).
// If rewrap returns an error for any row, or persisting any row's new
// Envelope fails, RewrapAll must leave every row exactly as it was
// under fromKeyID — never a partial re-wrap, and never a state where
// some rows are unreadable.
type CiphertextStore interface {
	RewrapAll(ctx context.Context, fromKeyID string, rewrap func(RewrapRow) (Envelope, error)) error
}

// ErrRotationInProgress is returned by Rotate when the keyring already
// has a PendingRotation — a prior rotation was interrupted before its
// re-wrap completed. Call Resume to finish it instead of starting a
// second, overlapping rotation.
var ErrRotationInProgress = errors.New("secrets: a key rotation is already pending; call Resume to finish it")

// KeyringHolder guards a *Keyring so a Rotate/Resume can never run
// concurrently with, or be observed half-applied by, a concurrent
// Encrypt/Decrypt call elsewhere — the rotation barrier the card
// requires.
//
// Concurrency contract:
//   - Get takes the read lock just long enough to return the current
//     *Keyring pointer. Rotate and Resume NEVER mutate an existing
//     *Keyring's Keys map in place: every step that changes the
//     keyring (beginRotation, completeRotation) builds a brand new
//     *Keyring value and only then swaps the holder to point at it,
//     under the write lock. So a *Keyring returned by Get, whether
//     obtained before, during, or after a concurrent Rotate/Resume,
//     is always a complete, internally-consistent snapshot — either
//     the pre-rotation keyring (old key active, new key not added
//     yet) or the post-begin keyring (new key active, old key still
//     present, PendingRotation set) or the post-complete keyring (new
//     key active, PendingRotation cleared) — never a torn map, and
//     always safe to pass to Encrypt/Decrypt.
//   - Rotate and Resume take the write lock for their entire duration,
//     including the CiphertextStore round trip, so at most one
//     rotation-affecting operation runs at a time and Get can never
//     observe a keyring mid-swap.
type KeyringHolder struct {
	mu sync.RWMutex
	kr *Keyring
}

// NewKeyringHolder wraps kr (typically the result of Load) for
// rotation-safe access.
func NewKeyringHolder(kr *Keyring) *KeyringHolder {
	return &KeyringHolder{kr: kr}
}

// Get returns the current keyring snapshot. See KeyringHolder's doc
// comment for why the returned value is always safe to use.
func (h *KeyringHolder) Get() *Keyring {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.kr
}

// Rotate performs a full key rotation against store:
//
//  1. Generates a fresh 32-byte key + key_id (crypto/rand), adds it to
//     the keyring, and makes it active — retaining every prior key.
//  2. Persists that new keyring, plus a PendingRotation marker
//     (from the old active key_id to the new one), to
//     <secretsDir>/keyring.json in a single atomic write (step B in
//     the card): from this point on, a crash leaves both keys present
//     and the marker readable by a future Resume.
//  3. Re-wraps every row store holds under the old key_id to the new
//     one (Decrypt under the old key, Encrypt under the new one — see
//     rewrap), inside store's own atomic RewrapAll.
//  4. On full success, persists the keyring once more with
//     PendingRotation cleared.
//
// If a rotation is already pending, Rotate refuses with
// ErrRotationInProgress — call Resume instead. If the re-wrap (step 3)
// fails, Rotate returns that error with the keyring left exactly as
// step 2 persisted it (both keys present, marker still set): the prior
// key remains fully usable, and Resume can retry.
func (h *KeyringHolder) Rotate(ctx context.Context, secretsDir string, store CiphertextStore) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.kr.PendingRotation != nil {
		return ErrRotationInProgress
	}

	next, err := beginRotation(h.kr, secretsDir)
	if err != nil {
		return err
	}
	h.kr = next

	return h.finishPending(ctx, secretsDir, store)
}

// Resume completes an interrupted rotation. It is meant to be called
// on a KeyringHolder freshly built from a reloaded keyring (Load),
// exactly as a restarted process would after a crash mid-rotation —
// but it works identically on a holder whose own Rotate call failed.
// If no rotation is pending, Resume is a no-op success (it never calls
// store, which is what makes calling it repeatedly idempotent).
func (h *KeyringHolder) Resume(ctx context.Context, secretsDir string, store CiphertextStore) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.kr.PendingRotation == nil {
		return nil
	}

	return h.finishPending(ctx, secretsDir, store)
}

// finishPending runs the re-wrap for h.kr's current PendingRotation and,
// on success, persists the keyring with the marker cleared. Callers
// must hold h.mu for writing.
func (h *KeyringHolder) finishPending(ctx context.Context, secretsDir string, store CiphertextStore) error {
	if err := rewrap(ctx, h.kr, store); err != nil {
		return fmt.Errorf("secrets: rotation re-wrap: %w", err)
	}

	completed, err := completeRotation(h.kr, secretsDir)
	if err != nil {
		return err
	}
	h.kr = completed
	return nil
}

// beginRotation generates a fresh key, adds it to kr (retaining every
// existing key), marks it active, sets PendingRotation to {kr's old
// active key_id -> the new one}, and persists the result atomically
// before returning. No ciphertext is touched here.
func beginRotation(kr *Keyring, secretsDir string) (*Keyring, error) {
	material := make([]byte, keyLength)
	if _, err := rand.Read(material); err != nil {
		return nil, fmt.Errorf("secrets: generate rotation key material: %w", err)
	}
	newKeyID, err := generateKeyID()
	if err != nil {
		return nil, fmt.Errorf("secrets: generate rotation key id: %w", err)
	}

	keys := make(map[string][]byte, len(kr.Keys)+1)
	for id, k := range kr.Keys {
		keys[id] = k
	}
	keys[newKeyID] = material

	next := &Keyring{
		ActiveKeyID: newKeyID,
		Keys:        keys,
		PendingRotation: &PendingRotation{
			FromKeyID: kr.ActiveKeyID,
			ToKeyID:   newKeyID,
		},
	}
	if err := persistKeyring(next, secretsDir); err != nil {
		return nil, err
	}
	return next, nil
}

// completeRotation clears kr's PendingRotation and persists the result
// atomically. It must only be called once every row has been confirmed
// moved off PendingRotation.FromKeyID.
func completeRotation(kr *Keyring, secretsDir string) (*Keyring, error) {
	next := &Keyring{
		ActiveKeyID:     kr.ActiveKeyID,
		Keys:            kr.Keys,
		PendingRotation: nil,
	}
	if err := persistKeyring(next, secretsDir); err != nil {
		return nil, err
	}
	return next, nil
}

// rewrap drives one CiphertextStore.RewrapAll pass for kr's current
// PendingRotation: every row still under FromKeyID is decrypted under
// the prior key and re-encrypted under kr's (already-active) new key.
// Plaintext exists only transiently in memory for the duration of one
// row's rewrap callback — it is never persisted or logged. If
// PendingRotation is nil, rewrap does nothing (used by Resume's
// idempotent no-op path, and defensively here too).
func rewrap(ctx context.Context, kr *Keyring, store CiphertextStore) error {
	pending := kr.PendingRotation
	if pending == nil {
		return nil
	}

	return store.RewrapAll(ctx, pending.FromKeyID, func(row RewrapRow) (Envelope, error) {
		plaintext, err := Decrypt(kr, row.Identity, row.Envelope)
		if err != nil {
			return Envelope{}, fmt.Errorf("decrypt row under prior key: %w", err)
		}
		env, err := Encrypt(kr, row.Identity, plaintext)
		if err != nil {
			return Envelope{}, fmt.Errorf("encrypt row under new key: %w", err)
		}
		return env, nil
	})
}
