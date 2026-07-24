package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// migratedDB opens a fresh, fully migrated database for a test — the
// same fixture shape storage's own tests use, reimplemented here since
// this is a separate (application_test) package: application_test MAY
// import storage to wire the real implementation (only non-test code in
// internal/accounts/application may never import it).
func migratedDB(t *testing.T) *storage.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(dir)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := storage.Migrate(context.Background(), db); err != nil {
		t.Fatalf("storage.Migrate: %v", err)
	}
	return db
}

func seedProvider(t *testing.T, db *storage.DB, providerID string) {
	t.Helper()
	if _, err := db.Conn().Exec(
		`INSERT INTO providers (id, display_name, auth_mode, funding_mode, created_at, updated_at)
		 VALUES (?, ?, 'api_key', 'owner_policy', 0, 0)`,
		providerID, providerID,
	); err != nil {
		t.Fatalf("seed provider %s: %v", providerID, err)
	}
}

func seedAccount(t *testing.T, db *storage.DB, providerID, accountID string) {
	t.Helper()
	if _, err := db.Conn().Exec(
		`INSERT INTO accounts (id, provider_id, external_id, auth_type, created_at, updated_at)
		 VALUES (?, ?, ?, 'api_key', 0, 0)`,
		accountID, providerID, accountID,
	); err != nil {
		t.Fatalf("seed account %s: %v", accountID, err)
	}
}

func seedProviderAndAccount(t *testing.T, db *storage.DB, providerID, accountID string) {
	t.Helper()
	seedProvider(t, db, providerID)
	seedAccount(t, db, providerID, accountID)
}

func newTestKeyring(t *testing.T) *secrets.Keyring {
	t.Helper()
	kr, err := secrets.Load(t.TempDir(), "", false)
	if err != nil {
		t.Fatalf("secrets.Load: %v", err)
	}
	return kr
}

func TestCredentialService_StoreThenUse_RoundTripsPlaintext(t *testing.T) {
	db := migratedDB(t)
	seedProviderAndAccount(t, db, "prov1", "acct1")

	repo := storage.NewAccountCredentialRepo(db)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc := application.NewCredentialService(repo, newTestKeyring(t), func() time.Time { return now })

	cred, err := svc.Store(context.Background(), application.StoreCredentialParams{
		ID: "cred1", AccountID: "acct1", ProviderID: "prov1",
		Kind: domain.CredentialKindAPIKey, Active: true, PlaintextKey: "sk-test-0123456789",
	})
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	if cred.State != domain.CredentialActive {
		t.Fatalf("State = %q, want active", cred.State)
	}

	var got []byte
	err = svc.Use(context.Background(), "cred1", func(plaintext []byte) error {
		got = append([]byte(nil), plaintext...)
		return nil
	})
	if err != nil {
		t.Fatalf("Use: %v", err)
	}
	if string(got) != "sk-test-0123456789" {
		t.Fatalf("round-tripped plaintext = %q, want %q", got, "sk-test-0123456789")
	}
}

func TestCredentialService_Use_WrongIdentityFailsDecrypt(t *testing.T) {
	db := migratedDB(t)
	seedProviderAndAccount(t, db, "prov1", "acct1")

	repo := storage.NewAccountCredentialRepo(db)
	kr := newTestKeyring(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc := application.NewCredentialService(repo, kr, func() time.Time { return now })

	if _, err := svc.Store(context.Background(), application.StoreCredentialParams{
		ID: "cred1", AccountID: "acct1", ProviderID: "prov1",
		Kind: domain.CredentialKindAPIKey, Active: true, PlaintextKey: "a-secret-api-key-value",
	}); err != nil {
		t.Fatalf("Store: %v", err)
	}

	// Fetch the ACTUAL stored envelope directly, then attempt to decrypt
	// it under a WRONG RecordIdentity (a different account) — proving
	// the AAD binding independent of the service's own (correct) usage.
	_, providerID, env, ok, err := repo.GetCredential(context.Background(), "cred1")
	if err != nil || !ok {
		t.Fatalf("GetCredential: ok=%v err=%v", ok, err)
	}

	wrongIdentity := secrets.RecordIdentity{Purpose: "credential", Provider: providerID, Account: "some-other-account", Record: "cred1", Kind: "api_key"}
	if _, err := secrets.Decrypt(kr, wrongIdentity, env); !errors.Is(err, secrets.ErrDecrypt) {
		t.Fatalf("Decrypt with a wrong account identity = %v, want secrets.ErrDecrypt", err)
	}
}

func TestCredentialService_Store_RejectsDuplicateFingerprint(t *testing.T) {
	db := migratedDB(t)
	seedProviderAndAccount(t, db, "prov1", "acct1")
	seedAccount(t, db, "prov1", "acct2") // second account, same provider

	repo := storage.NewAccountCredentialRepo(db)
	svc := application.NewCredentialService(repo, newTestKeyring(t), nil)

	if _, err := svc.Store(context.Background(), application.StoreCredentialParams{
		ID: "cred1", AccountID: "acct1", ProviderID: "prov1",
		Kind: domain.CredentialKindAPIKey, Active: true, PlaintextKey: "the-same-key-value",
	}); err != nil {
		t.Fatalf("first Store: %v", err)
	}

	_, err := svc.Store(context.Background(), application.StoreCredentialParams{
		ID: "cred2", AccountID: "acct2", ProviderID: "prov1",
		Kind: domain.CredentialKindAPIKey, Active: true, PlaintextKey: "the-same-key-value",
	})
	if !errors.Is(err, application.ErrFingerprintExists) {
		t.Fatalf("second Store (duplicate fingerprint) error = %v, want ErrFingerprintExists", err)
	}
}

func TestCredentialService_Store_RejectsSecondActiveOfSameKind(t *testing.T) {
	db := migratedDB(t)
	seedProviderAndAccount(t, db, "prov1", "acct1")

	repo := storage.NewAccountCredentialRepo(db)
	svc := application.NewCredentialService(repo, newTestKeyring(t), nil)

	if _, err := svc.Store(context.Background(), application.StoreCredentialParams{
		ID: "cred1", AccountID: "acct1", ProviderID: "prov1",
		Kind: domain.CredentialKindAPIKey, Active: true, PlaintextKey: "key-value-one",
	}); err != nil {
		t.Fatalf("first Store: %v", err)
	}

	_, err := svc.Store(context.Background(), application.StoreCredentialParams{
		ID: "cred2", AccountID: "acct1", ProviderID: "prov1",
		Kind: domain.CredentialKindAPIKey, Active: true, PlaintextKey: "key-value-two",
	})
	if !errors.Is(err, domain.ErrCredentialActiveConflict) {
		t.Fatalf("second active Store of the same kind error = %v, want domain.ErrCredentialActiveConflict", err)
	}
}

// TestCredentialService_Canary_KeyNeverStoredInAnyColumn pushes a
// distinctive canary key through Store and asserts no fragment of it
// appears in ANY account_credentials column (only ciphertext/nonce/
// key_id/fingerprint should be present, and the fingerprint itself must
// not literally contain the key).
func TestCredentialService_Canary_KeyNeverStoredInAnyColumn(t *testing.T) {
	const canaryKey = "CANARY-9f3Kx2Qw8pLm0Zt7Vb4Nr1Hy6Dc5Ea-credential"

	db := migratedDB(t)
	seedProviderAndAccount(t, db, "prov1", "acct1")
	repo := storage.NewAccountCredentialRepo(db)
	svc := application.NewCredentialService(repo, newTestKeyring(t), nil)

	if _, err := svc.Store(context.Background(), application.StoreCredentialParams{
		ID: "cred1", AccountID: "acct1", ProviderID: "prov1",
		Kind: domain.CredentialKindAPIKey, Active: true, PlaintextKey: canaryKey,
	}); err != nil {
		t.Fatalf("Store: %v", err)
	}

	var fingerprint, keyID string
	var nonce, ciphertext []byte
	if err := db.Conn().QueryRow(
		`SELECT fingerprint_sha256, key_id, nonce, ciphertext FROM account_credentials WHERE id = 'cred1'`,
	).Scan(&fingerprint, &keyID, &nonce, &ciphertext); err != nil {
		t.Fatalf("query stored row: %v", err)
	}

	assertNoFragment(t, fingerprint, canaryKey, "stored fingerprint_sha256")
	assertNoFragment(t, keyID, canaryKey, "stored key_id")
	assertNoFragment(t, string(nonce), canaryKey, "stored nonce")
	// Ciphertext legitimately encrypts the key — it must not be a plain
	// substring match either, since GCM ciphertext bytes bear no
	// resemblance to the plaintext bytes at all.
	assertNoFragment(t, string(ciphertext), canaryKey, "stored ciphertext")
}

func assertNoFragment(t *testing.T, output, secret, where string) {
	t.Helper()
	const minWindow = 8
	for start := 0; start+minWindow <= len(secret); start++ {
		end := start + minWindow
		if strings.Contains(output, secret[start:end]) {
			t.Fatalf("%s leaked secret fragment %q", where, secret[start:end])
		}
	}
}
