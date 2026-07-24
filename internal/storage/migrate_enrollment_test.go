package storage

import (
	"context"
	"testing"
)

// enrollmentVersion is the goose version of the M2 enrollment-core
// migration (00003_enrollment_core.sql).
const enrollmentVersion = 3

func TestMigrateEnrollment_UpDownUp(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	if _, err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() (up) error = %v", err)
	}
	assertTableExists(t, db, "providers", true)
	assertTableExists(t, db, "accounts", true)
	assertTableExists(t, db, "account_credentials", true)
	assertTableExists(t, db, "account_funding_evidence", true)
	assertTableExists(t, db, "oauth_transactions", true)

	// Roll back every migration applied on top of M2, then M2 itself, so
	// this proves M2's own down path no matter how many later migrations
	// exist (enrollmentVersion is M2's goose version). The M1/M3 up/down
	// tests use the same robustness.
	for currentVersion(t, db) >= enrollmentVersion {
		if _, err := DownOne(ctx, db); err != nil {
			t.Fatalf("DownOne() error = %v", err)
		}
	}
	assertTableExists(t, db, "providers", false)
	assertTableExists(t, db, "accounts", false)
	assertTableExists(t, db, "account_credentials", false)
	assertTableExists(t, db, "account_funding_evidence", false)
	assertTableExists(t, db, "oauth_transactions", false)
	// M1 and baseline must survive rolling back only M2.
	assertTableExists(t, db, "owner_auth", true)
	assertTableExists(t, db, "owner_sessions", true)
	assertTableExists(t, db, "auth_events", true)
	assertBaselineTableExists(t, db, true)

	if _, err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() (re-up) error = %v", err)
	}
	assertTableExists(t, db, "providers", true)
	assertTableExists(t, db, "accounts", true)
	assertTableExists(t, db, "account_credentials", true)
	assertTableExists(t, db, "account_funding_evidence", true)
	assertTableExists(t, db, "oauth_transactions", true)
}

func TestCredActivePerKind_Unique(t *testing.T) {
	db := migratedEnrollmentDB(t)
	insertProvider(t, db, "prov1")
	insertAccount(t, db, "acct1", "prov1")

	if err := insertCredential(db, "cred1", "acct1", "prov1", "api_key", "active", "fp1"); err != nil {
		t.Fatalf("insert first active api_key credential: %v", err)
	}

	if err := insertCredential(db, "cred2", "acct1", "prov1", "api_key", "active", "fp2"); err == nil {
		t.Fatalf("insert second active api_key credential for same account succeeded, want rejection")
	}

	if err := insertCredential(db, "cred3", "acct1", "prov1", "oauth2", "active", "fp3"); err != nil {
		t.Fatalf("insert active credential of a different kind on the same account: %v, want success (multi-kind coexistence)", err)
	}

	if err := retireCredential(db, "cred1"); err != nil {
		t.Fatalf("retire cred1: %v", err)
	}
	if err := insertCredential(db, "cred4", "acct1", "prov1", "api_key", "active", "fp4"); err != nil {
		t.Fatalf("insert new active api_key credential after retiring the prior one: %v, want success", err)
	}
}

func TestCredStagedPerKind_Unique(t *testing.T) {
	db := migratedEnrollmentDB(t)
	insertProvider(t, db, "prov1")
	insertAccount(t, db, "acct1", "prov1")

	if err := insertCredential(db, "cred1", "acct1", "prov1", "api_key", "active", "fp1"); err != nil {
		t.Fatalf("insert active credential: %v", err)
	}
	if err := insertCredential(db, "cred2", "acct1", "prov1", "api_key", "staged", "fp2"); err != nil {
		t.Fatalf("insert staged credential alongside active of same kind: %v, want success (active+staged coexist)", err)
	}

	if err := insertCredential(db, "cred3", "acct1", "prov1", "api_key", "staged", "fp3"); err == nil {
		t.Fatalf("insert second staged credential for same (account, kind) succeeded, want rejection")
	}
}

func TestCredFingerprint_Unique(t *testing.T) {
	db := migratedEnrollmentDB(t)
	insertProvider(t, db, "prov1")
	insertAccount(t, db, "acct1", "prov1")
	insertAccount(t, db, "acct2", "prov1")

	if err := insertCredential(db, "cred1", "acct1", "prov1", "api_key", "active", "dupfp"); err != nil {
		t.Fatalf("insert first credential with fingerprint: %v", err)
	}

	if err := insertCredential(db, "cred2", "acct2", "prov1", "api_key", "active", "dupfp"); err == nil {
		t.Fatalf("insert credential with duplicate (provider_id, fingerprint) among non-retired rows succeeded, want rejection")
	}

	if err := retireCredential(db, "cred1"); err != nil {
		t.Fatalf("retire cred1: %v", err)
	}
	if err := insertCredential(db, "cred3", "acct2", "prov1", "api_key", "active", "dupfp"); err != nil {
		t.Fatalf("insert credential re-using fingerprint after prior row retired: %v, want success", err)
	}
}

func TestFundingCurrent_Unique(t *testing.T) {
	db := migratedEnrollmentDB(t)
	insertProvider(t, db, "prov1")
	insertAccount(t, db, "acct1", "prov1")

	if err := insertFunding(db, "fund1", "acct1", nil); err != nil {
		t.Fatalf("insert first current funding row: %v", err)
	}

	if err := insertFunding(db, "fund2", "acct1", nil); err == nil {
		t.Fatalf("insert second current (superseded_at IS NULL) funding row for same account succeeded, want rejection")
	}

	if _, err := db.Conn().Exec(
		`UPDATE account_funding_evidence SET superseded_at = 1000 WHERE id = 'fund1'`,
	); err != nil {
		t.Fatalf("supersede fund1: %v", err)
	}
	if err := insertFunding(db, "fund2", "acct1", nil); err != nil {
		t.Fatalf("insert new current funding row after superseding the prior one: %v, want success", err)
	}
}

func TestAccountCredentials_FKEnforced(t *testing.T) {
	db := migratedEnrollmentDB(t)
	insertProvider(t, db, "prov1")
	// Deliberately no accounts row for "missing-acct".

	if err := insertCredential(db, "cred1", "missing-acct", "prov1", "api_key", "active", "fp1"); err == nil {
		t.Fatalf("insert account_credentials row with no matching accounts row succeeded, want FK rejection")
	}
}

func TestProviderDelete_CascadesToAccounts(t *testing.T) {
	db := migratedEnrollmentDB(t)
	insertProvider(t, db, "prov1")
	insertAccount(t, db, "acct1", "prov1")

	if _, err := db.Conn().Exec(`DELETE FROM providers WHERE id = 'prov1'`); err != nil {
		t.Fatalf("delete provider: %v", err)
	}

	assertRowCount(t, db, "accounts", 0)
}

func migratedEnrollmentDB(t *testing.T) *DB {
	t.Helper()

	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := Migrate(context.Background(), db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return db
}

func insertProvider(t *testing.T, db *DB, id string) {
	t.Helper()
	_, err := db.Conn().Exec(
		`INSERT INTO providers (id, display_name, auth_mode, funding_mode, created_at, updated_at)
		 VALUES (?, ?, 'api_key', 'fixed', 0, 0)`,
		id, id,
	)
	if err != nil {
		t.Fatalf("insert provider %s: %v", id, err)
	}
}

func insertAccount(t *testing.T, db *DB, id, providerID string) {
	t.Helper()
	_, err := db.Conn().Exec(
		`INSERT INTO accounts (id, provider_id, external_id, auth_type, created_at, updated_at)
		 VALUES (?, ?, ?, 'api_key', 0, 0)`,
		id, providerID, id,
	)
	if err != nil {
		t.Fatalf("insert account %s: %v", id, err)
	}
}

func insertCredential(db *DB, id, accountID, providerID, kind, state, fingerprint string) error {
	_, err := db.Conn().Exec(
		`INSERT INTO account_credentials
		    (id, account_id, provider_id, kind, state, fingerprint_sha256, key_id, nonce, ciphertext, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, 'key1', X'00', X'00', 0, 0)`,
		id, accountID, providerID, kind, state, fingerprint,
	)
	return err
}

func retireCredential(db *DB, id string) error {
	_, err := db.Conn().Exec(
		`UPDATE account_credentials SET state = 'retired', retired_at = 1 WHERE id = ?`, id,
	)
	return err
}

func insertFunding(db *DB, id, accountID string, supersededAt *int64) error {
	_, err := db.Conn().Exec(
		`INSERT INTO account_funding_evidence
		    (id, account_id, funding, source, confidence, observed_at, superseded_at)
		 VALUES (?, ?, 'unknown', 'owner_policy', 0.5, 0, ?)`,
		id, accountID, supersededAt,
	)
	return err
}

func assertRowCount(t *testing.T, db *DB, table string, want int) {
	t.Helper()

	var got int
	if err := db.Conn().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&got); err != nil {
		t.Fatalf("count rows in %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("row count in %s = %d, want %d", table, got, want)
	}
}
