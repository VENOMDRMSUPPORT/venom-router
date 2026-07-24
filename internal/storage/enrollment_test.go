package storage

import (
	"context"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
)

func TestEnrollmentRepo_CreateConnectedAccount_InsertsAllThreeRows(t *testing.T) {
	db := migratedEnrollmentDB(t)
	insertProvider(t, db, "prov1")
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	account := domain.Account{
		ID: "acct1", ProviderID: "prov1", ExternalID: "fingerprint-abc", AuthType: "api_key",
		ConnectionState: domain.ConnectionConnected, HealthState: domain.HealthUnknown,
		CreatedAt: now, UpdatedAt: now,
	}
	cred := domain.Credential{ID: "cred1", AccountID: "acct1", Kind: domain.CredentialKindAPIKey, State: domain.CredentialActive, Fingerprint: "fingerprint-abc"}
	env := secrets.Envelope{KeyID: "k1", Nonce: []byte("nonce-bytes"), Ciphertext: []byte("ciphertext-bytes")}
	funding := domain.FundingEvidence{ID: "fund1", AccountID: "acct1", Funding: domain.FundingFree, Source: domain.FundingSourceOwnerPolicy, Confidence: 1.0, ObservedAt: now}

	if err := NewEnrollmentRepo(db).CreateConnectedAccount(context.Background(), account, "prov1", cred, env, funding); err != nil {
		t.Fatalf("CreateConnectedAccount: %v", err)
	}

	assertRowCount(t, db, "accounts", 1)
	assertRowCount(t, db, "account_credentials", 1)
	assertRowCount(t, db, "account_funding_evidence", 1)

	acc, ok, err := NewAccountRepo(db).GetByID(context.Background(), "acct1")
	if err != nil || !ok {
		t.Fatalf("GetByID: ok=%v err=%v", ok, err)
	}
	if acc.ConnectionState != domain.ConnectionConnected {
		t.Fatalf("ConnectionState = %q, want connected", acc.ConnectionState)
	}

	gotCred, providerID, gotEnv, ok, err := NewAccountCredentialRepo(db).GetCredential(context.Background(), "cred1")
	if err != nil || !ok {
		t.Fatalf("GetCredential: ok=%v err=%v", ok, err)
	}
	if providerID != "prov1" || gotCred.State != domain.CredentialActive {
		t.Fatalf("credential = %+v (providerID=%q), want active under prov1", gotCred, providerID)
	}
	if gotEnv.KeyID != "k1" {
		t.Fatalf("envelope KeyID = %q, want k1", gotEnv.KeyID)
	}

	fund, ok, err := NewFundingEvidenceRepo(db).CurrentForAccount(context.Background(), "acct1")
	if err != nil || !ok {
		t.Fatalf("CurrentForAccount: ok=%v err=%v", ok, err)
	}
	if fund.Funding != domain.FundingFree || fund.Source != domain.FundingSourceOwnerPolicy {
		t.Fatalf("funding = %+v, want free/owner_policy", fund)
	}
}

// TestEnrollmentRepo_ForcedFailureLeavesZeroRows proves the atomicity
// 03 §2b requires: a CHECK-constraint violation on the THIRD insert
// (funding evidence, given an invalid source value) must roll back the
// account and credential inserts that already happened earlier in the
// same transaction — no partial enrollment is ever observable.
func TestEnrollmentRepo_ForcedFailureLeavesZeroRows(t *testing.T) {
	db := migratedEnrollmentDB(t)
	insertProvider(t, db, "prov1")
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	account := domain.Account{
		ID: "acct-fail", ProviderID: "prov1", ExternalID: "fingerprint-fail", AuthType: "api_key",
		ConnectionState: domain.ConnectionConnected, HealthState: domain.HealthUnknown,
		CreatedAt: now, UpdatedAt: now,
	}
	cred := domain.Credential{ID: "cred-fail", AccountID: "acct-fail", Kind: domain.CredentialKindAPIKey, State: domain.CredentialActive, Fingerprint: "fingerprint-fail"}
	env := secrets.Envelope{KeyID: "k1", Nonce: []byte("nonce-bytes"), Ciphertext: []byte("ciphertext-bytes")}
	// An invalid FundingSource value ("not_a_real_source") violates the
	// funding table's CHECK constraint, forcing the third insert to fail.
	badFunding := domain.FundingEvidence{ID: "fund-fail", AccountID: "acct-fail", Funding: domain.FundingFree, Source: domain.FundingSource("not_a_real_source"), Confidence: 1.0, ObservedAt: now}

	err := NewEnrollmentRepo(db).CreateConnectedAccount(context.Background(), account, "prov1", cred, env, badFunding)
	if err == nil {
		t.Fatalf("CreateConnectedAccount succeeded with an invalid funding source, want a CHECK-constraint failure")
	}

	assertRowCount(t, db, "accounts", 0)
	assertRowCount(t, db, "account_credentials", 0)
	assertRowCount(t, db, "account_funding_evidence", 0)
}
