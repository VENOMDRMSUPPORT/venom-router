package storage

import (
	"context"
	"testing"
)

func TestFundingEvidenceRepo_CurrentForAccount_NoneNotOK(t *testing.T) {
	db := migratedEnrollmentDB(t)
	insertProvider(t, db, "prov1")
	insertAccount(t, db, "acct1", "prov1")

	_, ok, err := NewFundingEvidenceRepo(db).CurrentForAccount(context.Background(), "acct1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("CurrentForAccount ok = true with no evidence rows, want false")
	}
}

func TestFundingEvidenceRepo_CurrentForAccount_FindsNonSupersededRow(t *testing.T) {
	db := migratedEnrollmentDB(t)
	insertProvider(t, db, "prov1")
	insertAccount(t, db, "acct1", "prov1")
	if err := insertFunding(db, "fund1", "acct1", nil); err != nil {
		t.Fatalf("insertFunding: %v", err)
	}

	e, ok, err := NewFundingEvidenceRepo(db).CurrentForAccount(context.Background(), "acct1")
	if err != nil || !ok {
		t.Fatalf("CurrentForAccount: ok=%v err=%v", ok, err)
	}
	if e.ID != "fund1" || e.AccountID != "acct1" {
		t.Fatalf("evidence = %+v, want ID=fund1 AccountID=acct1", e)
	}
	if !e.IsCurrent() {
		t.Fatalf("IsCurrent() = false for a row with superseded_at IS NULL, want true")
	}
}

func TestFundingEvidenceRepo_CurrentForAccount_IgnoresSupersededRows(t *testing.T) {
	db := migratedEnrollmentDB(t)
	insertProvider(t, db, "prov1")
	insertAccount(t, db, "acct1", "prov1")
	supersededAt := int64(1000)
	if err := insertFunding(db, "fund1", "acct1", &supersededAt); err != nil {
		t.Fatalf("insertFunding: %v", err)
	}

	_, ok, err := NewFundingEvidenceRepo(db).CurrentForAccount(context.Background(), "acct1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("CurrentForAccount found a superseded row, want none current")
	}
}
