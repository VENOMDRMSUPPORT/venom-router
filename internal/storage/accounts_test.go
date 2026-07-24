package storage

import (
	"context"
	"testing"
)

func TestAccountRepo_GetByID_UnknownNotOK(t *testing.T) {
	db := migratedEnrollmentDB(t)
	_, ok, err := NewAccountRepo(db).GetByID(context.Background(), "does-not-exist")
	if err != nil {
		t.Fatalf("GetByID: unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("GetByID(unknown) ok = true, want false")
	}
}

func TestAccountRepo_GetByID_FindsInsertedRow(t *testing.T) {
	db := migratedEnrollmentDB(t)
	insertProvider(t, db, "prov1")
	insertAccount(t, db, "acct1", "prov1")

	acc, ok, err := NewAccountRepo(db).GetByID(context.Background(), "acct1")
	if err != nil || !ok {
		t.Fatalf("GetByID: ok=%v err=%v", ok, err)
	}
	if acc.ID != "acct1" || acc.ProviderID != "prov1" || acc.ExternalID != "acct1" {
		t.Fatalf("account = %+v, want {ID:acct1 ProviderID:prov1 ExternalID:acct1}", acc)
	}
	if acc.AuthType != "api_key" {
		t.Fatalf("AuthType = %q, want api_key", acc.AuthType)
	}
	if acc.ConnectionState != "connecting" || acc.HealthState != "unknown" {
		t.Fatalf("initial state = {%q %q}, want {connecting unknown} (schema defaults)", acc.ConnectionState, acc.HealthState)
	}
}

func TestAccountRepo_GetByProviderExternalID(t *testing.T) {
	db := migratedEnrollmentDB(t)
	insertProvider(t, db, "prov1")
	insertAccount(t, db, "acct1", "prov1")

	acc, ok, err := NewAccountRepo(db).GetByProviderExternalID(context.Background(), "prov1", "acct1")
	if err != nil || !ok {
		t.Fatalf("GetByProviderExternalID: ok=%v err=%v", ok, err)
	}
	if acc.ID != "acct1" {
		t.Fatalf("ID = %q, want acct1", acc.ID)
	}

	_, ok, err = NewAccountRepo(db).GetByProviderExternalID(context.Background(), "prov1", "no-such-external-id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("GetByProviderExternalID(unknown external_id) ok = true, want false")
	}
}
