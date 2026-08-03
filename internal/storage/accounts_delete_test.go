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

// TestAccountRepo_PurgeDisconnected_RemovesLegacySoftDisconnectedAccounts proves
// the startup purge enforces the "no disconnected account ever lingers"
// invariant: every disconnected account (left behind by the old soft-disconnect)
// is hard-deleted with its data, while connected/stopped accounts are untouched.
func TestAccountRepo_PurgeDisconnected_RemovesLegacySoftDisconnectedAccounts(t *testing.T) {
	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-1")
	insertAccount(t, db, "gone-1", "prov-1")
	insertAccount(t, db, "gone-2", "prov-1")
	insertAccount(t, db, "live-1", "prov-1")
	mustExec(t, db, `UPDATE accounts SET connection_state = 'disconnected' WHERE id IN ('gone-1','gone-2')`)
	mustExec(t, db, `UPDATE accounts SET connection_state = 'connected' WHERE id = 'live-1'`)
	// A disconnected account's lingering offering must go with it.
	insertModelFull(t, db, "model-1", "ck-1", "M1", nil, nil, nil)
	insertOfferingFull(t, db, "gone-1", "prov-1", "big-pickle", "model-1", nil, nil, nil, nil, nil, 0, 0)

	n, err := NewAccountRepo(db).PurgeDisconnected(context.Background())
	if err != nil {
		t.Fatalf("PurgeDisconnected() error = %v", err)
	}
	if n != 2 {
		t.Fatalf("PurgeDisconnected() = %d, want 2", n)
	}
	if c := countWhere(t, db, "accounts", "connection_state = 'disconnected'"); c != 0 {
		t.Fatalf("%d disconnected accounts remain, want 0", c)
	}
	if c := countWhere(t, db, "accounts", "id = 'live-1'"); c != 1 {
		t.Fatal("the connected account was wrongly purged")
	}
	if c := countWhere(t, db, "account_model_offerings", "account_id = 'gone-1'"); c != 0 {
		t.Fatalf("a purged account's offerings remain: %d", c)
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
