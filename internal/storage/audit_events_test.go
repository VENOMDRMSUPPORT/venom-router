package storage

import (
	"context"
	"testing"
	"time"
)

// TestAuditEventRepo_Append_InsertsOneRow proves Append writes exactly
// one row with the fields it was given (P2b-OBS-001).
func TestAuditEventRepo_Append_InsertsOneRow(t *testing.T) {
	db := migratedAuditJobsDB(t)
	repo := NewAuditEventRepo(db)

	at := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	if err := repo.Append(context.Background(), AuditEventRow{
		Action: "account.connect", EntityType: "account", EntityID: "acct-1",
		Result: "success", ReasonCode: "", At: at,
	}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	var count int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM audit_events`).Scan(&count); err != nil {
		t.Fatalf("count audit_events: %v", err)
	}
	if count != 1 {
		t.Fatalf("audit_events row count = %d, want 1", count)
	}

	var action, entityType, entityID, result string
	var reasonCode *string
	var atEpoch int64
	if err := db.Conn().QueryRow(
		`SELECT action, entity_type, entity_id, result, reason_code, at FROM audit_events`,
	).Scan(&action, &entityType, &entityID, &result, &reasonCode, &atEpoch); err != nil {
		t.Fatalf("scan audit_events row: %v", err)
	}
	if action != "account.connect" || entityType != "account" || entityID != "acct-1" || result != "success" {
		t.Fatalf("row = (%q, %q, %q, %q), want (account.connect, account, acct-1, success)", action, entityType, entityID, result)
	}
	if reasonCode != nil {
		t.Fatalf("reason_code = %v, want NULL for an empty ReasonCode", *reasonCode)
	}
	if atEpoch != at.Unix() {
		t.Fatalf("at = %d, want %d", atEpoch, at.Unix())
	}
}

// TestAuditEventRepo_Append_AppendOnlyHolds proves that a row inserted
// via the repo (not just via raw SQL, as migrate_audit_jobs_test.go's
// TestAuditEvents_AppendOnly already covers) cannot be updated or
// deleted — the M3 BEFORE UPDATE/DELETE triggers fire regardless of how
// the row was written.
func TestAuditEventRepo_Append_AppendOnlyHolds(t *testing.T) {
	db := migratedAuditJobsDB(t)
	repo := NewAuditEventRepo(db)

	at := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	if err := repo.Append(context.Background(), AuditEventRow{
		Action: "account.connect", EntityType: "account", EntityID: "acct-1",
		Result: "success", At: at,
	}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	if _, err := db.Conn().Exec(`UPDATE audit_events SET result = 'rejected' WHERE action = 'account.connect'`); err == nil {
		t.Fatalf("UPDATE against a repo-inserted audit_events row succeeded, want RAISE(ABORT) rejection")
	}
	if _, err := db.Conn().Exec(`DELETE FROM audit_events WHERE action = 'account.connect'`); err == nil {
		t.Fatalf("DELETE against a repo-inserted audit_events row succeeded, want RAISE(ABORT) rejection")
	}

	var result string
	if err := db.Conn().QueryRow(`SELECT result FROM audit_events WHERE action = 'account.connect'`).Scan(&result); err != nil {
		t.Fatalf("query surviving audit_events row: %v", err)
	}
	if result != "success" {
		t.Fatalf("audit_events row result = %q, want %q (rejected UPDATE must not have applied)", result, "success")
	}
}
