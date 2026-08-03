package storage

import (
	"context"
	"testing"
)

// TestAccountRepo_Delete_CascadesOperationalDataAndCleansOrphans proves that
// removing an account returns its provider to a pristine state: the account row
// is deleted, every FK-cascaded operational table is emptied for it (offerings,
// offering_operations, certifications, quota_windows), and the FK-less orphan
// state that would otherwise linger (an account-scoped circuit breaker) is
// explicitly cleaned. Append-only history (usage_records, audit_events) has no
// account FK and is retained by construction — not asserted here.
func TestAccountRepo_Delete_CascadesOperationalDataAndCleansOrphans(t *testing.T) {
	db := migratedCatalogRepoDB(t)
	const acct = "acct-remove"
	insertProvider(t, db, "prov-1")
	insertAccount(t, db, acct, "prov-1")
	insertModelFull(t, db, "model-1", "ck-1", "M1", nil, nil, nil)
	insertOfferingFull(t, db, acct, "prov-1", "big-pickle", "model-1", nil, nil, nil, nil, nil, 0, 0)
	insertOfferingOperationFull(t, db, "op-1", acct, "prov-1", "big-pickle", "chat", "certified", "supported", 1, nil, "")

	mustExec(t, db, `INSERT INTO quota_windows (id, account_id, source, unit, window_type, window_key, confidence, freshness_state, observed_at, created_at, updated_at)
		VALUES ('qw-1', ?, 'provider_evidence', 'requests', 'rolling', 'k1', 1.0, 'fresh', 0, 0, 0)`, acct)
	mustExec(t, db, `INSERT INTO circuit_breakers (scope, scope_id, state) VALUES ('account', ?, 'closed')`, acct)

	deleted, err := NewAccountRepo(db).Delete(context.Background(), acct)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if !deleted {
		t.Fatal("Delete() reported not-deleted for an existing account")
	}

	for _, c := range []struct {
		table string
		where string
	}{
		{"accounts", "id = '" + acct + "'"},
		{"account_model_offerings", "account_id = '" + acct + "'"},
		{"offering_operations", "account_id = '" + acct + "'"},
		{"certifications", "offering_operation_id = 'op-1'"},
		{"quota_windows", "account_id = '" + acct + "'"},
		{"circuit_breakers", "scope = 'account' AND scope_id = '" + acct + "'"},
	} {
		if n := countWhere(t, db, c.table, c.where); n != 0 {
			t.Errorf("%s still has %d row(s) for the removed account (want 0)", c.table, n)
		}
	}
}

// TestAccountRepo_Delete_UnknownAccountReportsNotDeleted proves Delete is a
// safe no-op for a missing id.
func TestAccountRepo_Delete_UnknownAccountReportsNotDeleted(t *testing.T) {
	db := migratedCatalogRepoDB(t)
	deleted, err := NewAccountRepo(db).Delete(context.Background(), "nope")
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deleted {
		t.Fatal("Delete() reported deleted for a missing account")
	}
}

func mustExec(t *testing.T, db *DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Conn().Exec(query, args...); err != nil {
		t.Fatalf("seed exec failed: %v\nquery: %s", err, query)
	}
}

func countWhere(t *testing.T, db *DB, table, where string) int {
	t.Helper()
	var n int
	if err := db.Conn().QueryRow("SELECT COUNT(*) FROM " + table + " WHERE " + where).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}
