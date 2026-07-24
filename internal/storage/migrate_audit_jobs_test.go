package storage

import (
	"context"
	"testing"
)

// auditJobsVersion is the goose version of the M3 audit+jobs migration
// (00004_audit_jobs.sql).
const auditJobsVersion = 4

func TestMigrateAuditJobs_UpDownUp(t *testing.T) {
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
	assertTableExists(t, db, "audit_events", true)
	assertTableExists(t, db, "jobs", true)

	// Roll back every migration applied on top of M3, then M3 itself, so
	// this proves M3's own down path no matter how many later migrations
	// land (auditJobsVersion is M3's goose version). This matches the
	// robustness the M1/M2 up/down tests use, so a future M4 will not
	// silently break this test the way M3 broke M2's.
	for currentVersion(t, db) >= auditJobsVersion {
		if _, err := DownOne(ctx, db); err != nil {
			t.Fatalf("DownOne() error = %v", err)
		}
	}
	assertTableExists(t, db, "audit_events", false)
	assertTableExists(t, db, "jobs", false)
	// M2/M1/baseline must survive rolling back only M3.
	assertTableExists(t, db, "providers", true)
	assertTableExists(t, db, "accounts", true)
	assertTableExists(t, db, "account_credentials", true)
	assertTableExists(t, db, "account_funding_evidence", true)
	assertTableExists(t, db, "oauth_transactions", true)
	assertTableExists(t, db, "owner_auth", true)
	assertTableExists(t, db, "owner_sessions", true)
	assertTableExists(t, db, "auth_events", true)
	assertBaselineTableExists(t, db, true)

	if _, err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() (re-up) error = %v", err)
	}
	assertTableExists(t, db, "audit_events", true)
	assertTableExists(t, db, "jobs", true)
}

func TestAuditEvents_AppendOnly(t *testing.T) {
	db := migratedAuditJobsDB(t)

	res, err := db.Conn().Exec(
		`INSERT INTO audit_events (action, entity_type, entity_id, result, reason_code, at)
		 VALUES ('account.enroll', 'account', 'acct1', 'ok', NULL, 0)`,
	)
	if err != nil {
		t.Fatalf("insert audit_events row: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}

	if _, err := db.Conn().Exec(
		`UPDATE audit_events SET result = 'rejected' WHERE id = ?`, id,
	); err == nil {
		t.Fatalf("UPDATE audit_events succeeded, want RAISE(ABORT) rejection")
	}

	if _, err := db.Conn().Exec(
		`DELETE FROM audit_events WHERE id = ?`, id,
	); err == nil {
		t.Fatalf("DELETE audit_events succeeded, want RAISE(ABORT) rejection")
	}

	var result string
	if err := db.Conn().QueryRow(
		`SELECT result FROM audit_events WHERE id = ?`, id,
	).Scan(&result); err != nil {
		t.Fatalf("query surviving audit_events row: %v", err)
	}
	if result != "ok" {
		t.Fatalf("audit_events row result = %q, want %q (rejected UPDATE must not have applied)", result, "ok")
	}
}

func TestJobs_StatusCheck(t *testing.T) {
	db := migratedAuditJobsDB(t)

	if err := insertJob(db, "job-bad", "bogus"); err == nil {
		t.Fatalf("insert job with disallowed status 'bogus' succeeded, want CHECK rejection")
	}

	for _, status := range []string{"pending", "running", "completed", "failed", "expired"} {
		id := "job-" + status
		if err := insertJob(db, id, status); err != nil {
			t.Fatalf("insert job with allowed status %q: %v, want success", status, err)
		}
	}
}

func migratedAuditJobsDB(t *testing.T) *DB {
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

func insertJob(db *DB, id, status string) error {
	_, err := db.Conn().Exec(
		`INSERT INTO jobs (id, kind, status, created_at) VALUES (?, 'discovery', ?, 0)`,
		id, status,
	)
	return err
}
