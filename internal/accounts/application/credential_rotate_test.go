package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

func TestCredentialService_Rotate_RefusesStagedAndUnknown(t *testing.T) {
	db := migratedDB(t)
	seedProviderAndAccount(t, db, "prov1", "acct1")
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	svc := application.NewCredentialService(storage.NewAccountCredentialRepo(db), newTestKeyring(t), func() time.Time { return now })

	if _, err := svc.Store(context.Background(), application.StoreCredentialParams{
		ID: "cred-staged", AccountID: "acct1", ProviderID: "prov1",
		Kind: domain.CredentialKindOAuth2, Active: false, PlaintextKey: `{"access_token":"a"}`,
	}); err != nil {
		t.Fatalf("Store(staged): %v", err)
	}

	if err := svc.Rotate(context.Background(), "cred-staged", `{"access_token":"b"}`, nil); !errors.Is(err, application.ErrCredentialNotRotatable) {
		t.Fatalf("Rotate(staged) error = %v, want ErrCredentialNotRotatable", err)
	}
	if err := svc.Rotate(context.Background(), "no-such-cred", `{"access_token":"b"}`, nil); !errors.Is(err, application.ErrCredentialNotFound) {
		t.Fatalf("Rotate(unknown) error = %v, want ErrCredentialNotFound", err)
	}
}

// TestCredentialService_Rotate_StoresPlaintextVerbatim pins the no-
// normalization rule: OAuth token JSON may carry meaningful whitespace
// inside string values (e.g. a user's display name), so Rotate must seal
// the bytes exactly as the adapter marshaled them — unlike Store's
// API-key whitespace normalization.
func TestCredentialService_Rotate_StoresPlaintextVerbatim(t *testing.T) {
	db := migratedDB(t)
	seedProviderAndAccount(t, db, "prov1", "acct1")
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	svc := application.NewCredentialService(storage.NewAccountCredentialRepo(db), newTestKeyring(t), func() time.Time { return now })

	if _, err := svc.Store(context.Background(), application.StoreCredentialParams{
		ID: "cred1", AccountID: "acct1", ProviderID: "prov1",
		Kind: domain.CredentialKindOAuth2, Active: true, PlaintextKey: `{"access_token":"a"}`,
	}); err != nil {
		t.Fatalf("Store: %v", err)
	}

	withInnerSpaces := `{"access_token":"b","user_info":{"name":"Two  Spaces"}}`
	if err := svc.Rotate(context.Background(), "cred1", withInnerSpaces, nil); err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	var got string
	if err := svc.Use(context.Background(), "cred1", func(pt []byte) error {
		got = string(pt)
		return nil
	}); err != nil {
		t.Fatalf("Use: %v", err)
	}
	if got != withInnerSpaces {
		t.Fatalf("plaintext after rotate = %q, want verbatim %q", got, withInnerSpaces)
	}
}
