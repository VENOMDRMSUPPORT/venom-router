package secrets

import (
	"context"
	"errors"
	"testing"
)

type fakeRefStore struct {
	refs []CiphertextRef
	err  error
}

func (s fakeRefStore) ListKeyRefs(_ context.Context) ([]CiphertextRef, error) {
	return s.refs, s.err
}

func freshKeyring(t *testing.T) *Keyring {
	t.Helper()
	kr, err := Load(t.TempDir(), "", false)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return kr
}

func TestReconcile_EmptyStore_Passes(t *testing.T) {
	kr := freshKeyring(t)

	if err := Reconcile(context.Background(), kr, fakeRefStore{}); err != nil {
		t.Fatalf("Reconcile() error = %v, want nil for an empty store", err)
	}
}

func TestReconcile_AllKeyIDsPresent_Passes(t *testing.T) {
	kr := freshKeyring(t)
	store := fakeRefStore{refs: []CiphertextRef{
		{Envelope: Envelope{KeyID: kr.ActiveKeyID}},
		{Envelope: Envelope{KeyID: kr.ActiveKeyID}},
	}}

	if err := Reconcile(context.Background(), kr, store); err != nil {
		t.Fatalf("Reconcile() error = %v, want nil when every key_id is present", err)
	}
}

func TestReconcile_MissingKeyID_FailsClosed(t *testing.T) {
	kr := freshKeyring(t)
	store := fakeRefStore{refs: []CiphertextRef{
		{Envelope: Envelope{KeyID: kr.ActiveKeyID}},
		{Envelope: Envelope{KeyID: "k_does_not_exist"}},
	}}

	err := Reconcile(context.Background(), kr, store)
	if !errors.Is(err, ErrMissingKey) {
		t.Fatalf("Reconcile() error = %v, want ErrMissingKey", err)
	}
}

func TestReconcile_TrialDecryptFailure_FailsClosed(t *testing.T) {
	kr := freshKeyring(t)
	identity := RecordIdentity{Purpose: "p", Provider: "pr", Account: "a", Record: "r", Kind: "k"}

	env, err := Encrypt(kr, identity, []byte("plaintext"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	env.Ciphertext[0] ^= 0xFF // tamper: key_id is valid, but the body no longer decrypts

	store := fakeRefStore{refs: []CiphertextRef{{Identity: identity, Envelope: env}}}

	err = Reconcile(context.Background(), kr, store)
	if !errors.Is(err, ErrUnreadableCiphertext) {
		t.Fatalf("Reconcile() error = %v, want ErrUnreadableCiphertext", err)
	}
}

func TestReconcile_ValidEnvelope_TrialDecryptPasses(t *testing.T) {
	kr := freshKeyring(t)
	identity := RecordIdentity{Purpose: "p", Provider: "pr", Account: "a", Record: "r", Kind: "k"}

	env, err := Encrypt(kr, identity, []byte("plaintext"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	store := fakeRefStore{refs: []CiphertextRef{{Identity: identity, Envelope: env}}}

	if err := Reconcile(context.Background(), kr, store); err != nil {
		t.Fatalf("Reconcile() error = %v, want nil for a genuinely valid envelope", err)
	}
}

func TestReconcile_StoreListError_Propagates(t *testing.T) {
	kr := freshKeyring(t)
	wantErr := errors.New("boom")
	store := fakeRefStore{err: wantErr}

	err := Reconcile(context.Background(), kr, store)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Reconcile() error = %v, want it to wrap the store's list error", err)
	}
}
