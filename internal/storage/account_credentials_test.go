package storage

import (
	"context"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
)

func TestAccountCredentialRepo_CreateAndGetCredential_RoundTripsEnvelope(t *testing.T) {
	db := migratedEnrollmentDB(t)
	insertProvider(t, db, "prov1")
	insertAccount(t, db, "acct1", "prov1")

	repo := NewAccountCredentialRepo(db)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cred := domain.Credential{ID: "cred1", AccountID: "acct1", Kind: domain.CredentialKindAPIKey, State: domain.CredentialActive, Fingerprint: "fp-abc"}
	env := secrets.Envelope{KeyID: "k1", Nonce: []byte("nonce-bytes-12"), Ciphertext: []byte("ciphertext-bytes")}

	if err := repo.Create(context.Background(), "prov1", cred, env, now); err != nil {
		t.Fatalf("Create: %v", err)
	}

	gotCred, providerID, gotEnv, ok, err := repo.GetCredential(context.Background(), "cred1")
	if err != nil || !ok {
		t.Fatalf("GetCredential: ok=%v err=%v", ok, err)
	}
	if providerID != "prov1" {
		t.Fatalf("providerID = %q, want prov1", providerID)
	}
	if gotCred.Kind != domain.CredentialKindAPIKey || gotCred.State != domain.CredentialActive || gotCred.Fingerprint != "fp-abc" {
		t.Fatalf("cred = %+v, want kind=api_key state=active fingerprint=fp-abc", gotCred)
	}
	if gotEnv.KeyID != "k1" || string(gotEnv.Nonce) != "nonce-bytes-12" || string(gotEnv.Ciphertext) != "ciphertext-bytes" {
		t.Fatalf("envelope = %+v, want the exact stored bytes back", gotEnv)
	}
}

func TestAccountCredentialRepo_GetCredential_UnknownNotOK(t *testing.T) {
	db := migratedEnrollmentDB(t)
	_, _, _, ok, err := NewAccountCredentialRepo(db).GetCredential(context.Background(), "does-not-exist")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("GetCredential(unknown) ok = true, want false")
	}
}

func TestAccountCredentialRepo_ListForAccount_ReturnsAllStates(t *testing.T) {
	db := migratedEnrollmentDB(t)
	insertProvider(t, db, "prov1")
	insertAccount(t, db, "acct1", "prov1")
	repo := NewAccountCredentialRepo(db)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	active := domain.Credential{ID: "cred-active", AccountID: "acct1", Kind: domain.CredentialKindAPIKey, State: domain.CredentialActive, Fingerprint: "fp1"}
	if err := repo.Create(context.Background(), "prov1", active, secrets.Envelope{KeyID: "k", Nonce: []byte("n"), Ciphertext: []byte("c")}, now); err != nil {
		t.Fatalf("Create(active): %v", err)
	}
	staged := domain.Credential{ID: "cred-staged", AccountID: "acct1", Kind: domain.CredentialKindOAuth2, State: domain.CredentialStaged, Fingerprint: "fp2"}
	if err := repo.Create(context.Background(), "prov1", staged, secrets.Envelope{KeyID: "k", Nonce: []byte("n"), Ciphertext: []byte("c")}, now); err != nil {
		t.Fatalf("Create(staged): %v", err)
	}

	list, err := repo.ListForAccount(context.Background(), "acct1")
	if err != nil {
		t.Fatalf("ListForAccount: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("len(list) = %d, want 2", len(list))
	}
}

func TestAccountCredentialRepo_RotateCiphertext_ReplacesEnvelopeInPlace(t *testing.T) {
	db := migratedEnrollmentDB(t)
	insertProvider(t, db, "prov1")
	insertAccount(t, db, "acct1", "prov1")
	repo := NewAccountCredentialRepo(db)
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	cred := domain.Credential{ID: "cred1", AccountID: "acct1", Kind: domain.CredentialKindOAuth2, State: domain.CredentialActive, Fingerprint: "fp-old"}
	if err := repo.Create(context.Background(), "prov1", cred, secrets.Envelope{KeyID: "k1", Nonce: []byte("n1"), Ciphertext: []byte("c1")}, created); err != nil {
		t.Fatalf("Create: %v", err)
	}

	rotatedAt := created.Add(45 * time.Minute)
	newExpiry := rotatedAt.Add(time.Hour).UTC().Truncate(time.Second)
	ok, err := repo.RotateCiphertext(context.Background(), "cred1", "fp-new",
		secrets.Envelope{KeyID: "k2", Nonce: []byte("n2"), Ciphertext: []byte("c2")}, &newExpiry, rotatedAt)
	if err != nil || !ok {
		t.Fatalf("RotateCiphertext: ok=%v err=%v", ok, err)
	}

	got, providerID, env, ok, err := repo.GetCredential(context.Background(), "cred1")
	if err != nil || !ok {
		t.Fatalf("GetCredential after rotate: ok=%v err=%v", ok, err)
	}
	if providerID != "prov1" || got.AccountID != "acct1" || got.State != domain.CredentialActive || got.Kind != domain.CredentialKindOAuth2 {
		t.Fatalf("rotate must not change identity/state, got %+v provider=%q", got, providerID)
	}
	if got.Fingerprint != "fp-new" {
		t.Fatalf("Fingerprint = %q, want fp-new", got.Fingerprint)
	}
	if env.KeyID != "k2" || string(env.Nonce) != "n2" || string(env.Ciphertext) != "c2" {
		t.Fatalf("envelope after rotate = %+v, want the new sealed bytes", env)
	}
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(newExpiry) {
		t.Fatalf("ExpiresAt = %v, want %v", got.ExpiresAt, newExpiry)
	}
}

func TestAccountCredentialRepo_RotateCiphertext_RefusesNonActiveAndUnknown(t *testing.T) {
	db := migratedEnrollmentDB(t)
	insertProvider(t, db, "prov1")
	insertAccount(t, db, "acct1", "prov1")
	repo := NewAccountCredentialRepo(db)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	staged := domain.Credential{ID: "cred-staged", AccountID: "acct1", Kind: domain.CredentialKindOAuth2, State: domain.CredentialStaged, Fingerprint: "fp-s"}
	if err := repo.Create(context.Background(), "prov1", staged, secrets.Envelope{KeyID: "k", Nonce: []byte("n"), Ciphertext: []byte("c")}, now); err != nil {
		t.Fatalf("Create(staged): %v", err)
	}

	for name, id := range map[string]string{"staged row": "cred-staged", "unknown id": "nope"} {
		ok, err := repo.RotateCiphertext(context.Background(), id, "fp-x", secrets.Envelope{KeyID: "k", Nonce: []byte("n"), Ciphertext: []byte("c")}, nil, now)
		if err != nil {
			t.Fatalf("%s: RotateCiphertext error = %v", name, err)
		}
		if ok {
			t.Fatalf("%s: RotateCiphertext ok = true, want false", name)
		}
	}

	// The staged row's envelope must be untouched by the refused rotate.
	_, _, env, ok, err := repo.GetCredential(context.Background(), "cred-staged")
	if err != nil || !ok {
		t.Fatalf("GetCredential(staged): ok=%v err=%v", ok, err)
	}
	if env.KeyID != "k" || string(env.Ciphertext) != "c" {
		t.Fatalf("staged envelope changed by refused rotate: %+v", env)
	}
}

func TestAccountCredentialRepo_FingerprintExists(t *testing.T) {
	db := migratedEnrollmentDB(t)
	insertProvider(t, db, "prov1")
	insertAccount(t, db, "acct1", "prov1")
	repo := NewAccountCredentialRepo(db)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	exists, err := repo.FingerprintExists(context.Background(), "prov1", "fp-none-yet")
	if err != nil {
		t.Fatalf("FingerprintExists: %v", err)
	}
	if exists {
		t.Fatalf("FingerprintExists = true before any credential, want false")
	}

	cred := domain.Credential{ID: "cred1", AccountID: "acct1", Kind: domain.CredentialKindAPIKey, State: domain.CredentialActive, Fingerprint: "fp-dup"}
	if err := repo.Create(context.Background(), "prov1", cred, secrets.Envelope{KeyID: "k", Nonce: []byte("n"), Ciphertext: []byte("c")}, now); err != nil {
		t.Fatalf("Create: %v", err)
	}

	exists, err = repo.FingerprintExists(context.Background(), "prov1", "fp-dup")
	if err != nil {
		t.Fatalf("FingerprintExists: %v", err)
	}
	if !exists {
		t.Fatalf("FingerprintExists = false after Create, want true")
	}
}
