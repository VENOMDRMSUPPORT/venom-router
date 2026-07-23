package secrets

import (
	"context"
	"errors"
	"fmt"
)

// ErrMissingKey is returned by Reconcile when a stored ciphertext
// references a key_id the keyring does not hold — the card-mandated
// fail-closed condition: a DB reference to a key the keyring lacks
// aborts startup. Its message carries only the key_id, never key or
// ciphertext bytes.
var ErrMissingKey = errors.New("secrets: stored ciphertext references a key_id the keyring does not have")

// ErrUnreadableCiphertext is returned by Reconcile when a stored
// ciphertext's key_id IS present in the keyring but its body still
// fails authenticated decryption (e.g. tampered/corrupted storage).
// Trial-decryption only runs for refs that carry a full Envelope (see
// CiphertextRef); it never appears with key or ciphertext bytes.
var ErrUnreadableCiphertext = errors.New("secrets: stored ciphertext is unreadable")

// CiphertextRef is one stored ciphertext's key_id reference, as
// enumerated by a CiphertextRefStore for reconciliation.
//
// Envelope.KeyID is the mandatory field: Reconcile always checks it
// against the keyring. Identity and the rest of Envelope are optional —
// when Envelope.Ciphertext is non-empty, Reconcile additionally
// trial-decrypts under Identity, catching a readable-key/corrupted-body
// mismatch the key_id check alone would miss; a ref with only KeyID set
// (Ciphertext empty) validates the key_id reference only, which is the
// card's mandatory minimum.
type CiphertextRef struct {
	Identity RecordIdentity
	Envelope Envelope
}

// CiphertextRefStore is the minimal seam Reconcile needs to enumerate
// every stored ciphertext's key_id reference. The real, credential-
// table-backed implementation lands in P2b once M2's columns exist;
// until then, callers (internal/app's Boot) wire an empty store, so
// Reconcile trivially passes with zero rows to check.
type CiphertextRefStore interface {
	ListKeyRefs(ctx context.Context) ([]CiphertextRef, error)
}

// Reconcile validates every stored ciphertext reference against kr
// before a listener is ever allowed to open (01 §2/§8). It fails closed
// on the FIRST ref whose Envelope.KeyID is not present in kr.Keys
// (ErrMissingKey), and — for any ref that also carries a non-empty
// Envelope.Ciphertext — on the first trial-decrypt failure
// (ErrUnreadableCiphertext). No key or ciphertext bytes ever appear in
// the returned error.
func Reconcile(ctx context.Context, kr *Keyring, store CiphertextRefStore) error {
	refs, err := store.ListKeyRefs(ctx)
	if err != nil {
		return fmt.Errorf("secrets: list stored ciphertext references: %w", err)
	}

	for _, ref := range refs {
		if _, ok := kr.Keys[ref.Envelope.KeyID]; !ok {
			return fmt.Errorf("%w: key_id %q", ErrMissingKey, ref.Envelope.KeyID)
		}
		if len(ref.Envelope.Ciphertext) == 0 {
			continue
		}
		if _, err := Decrypt(kr, ref.Identity, ref.Envelope); err != nil {
			return fmt.Errorf("%w: key_id %q", ErrUnreadableCiphertext, ref.Envelope.KeyID)
		}
	}
	return nil
}
