package storage

import (
	"context"
	"strings"
	"testing"
)

// apiKeysVersion is the goose version of the M7 migration
// (00012_api_keys_usage.sql).
const apiKeysVersion = 12

// apiKeysTables enumerates the two M7 tables in creation order.
var apiKeysTables = []string{"venom_api_keys", "usage_records"}

// hex64 returns a syntactically valid 64-char lowercase-hex key_hash whose
// leading bytes vary by seed, for tests that need distinct valid hashes.
func hex64(seed string) string {
	h := seed
	if len(h) > 64 {
		h = h[:64]
	}
	return h + strings.Repeat("0", 64-len(h))
}

// TestMigrateAPIKeys_UpDownUp proves 00012 creates the two M7 tables, rolls
// back to exactly the pre-M7 shape (spot-checked: route_decisions and
// venom_api_keys's predecessor tables survive), and re-applies. The rollback
// loop is count-agnostic (mirrors TestMigrateRouting_UpDownUp): it rolls back
// every migration at or above apiKeysVersion, so a later migration lands
// without silently breaking this test.
func TestMigrateAPIKeys_UpDownUp(t *testing.T) {
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
	for _, table := range apiKeysTables {
		assertTableExists(t, db, table, true)
	}

	for currentVersion(t, db) >= apiKeysVersion {
		if _, err := DownOne(ctx, db); err != nil {
			t.Fatalf("DownOne() error = %v", err)
		}
	}
	for _, table := range apiKeysTables {
		assertTableExists(t, db, table, false)
	}
	// A pre-M7 table must survive rolling back only 00012.
	assertTableExists(t, db, "route_decisions", true)

	if _, err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() (re-up) error = %v", err)
	}
	for _, table := range apiKeysTables {
		assertTableExists(t, db, table, true)
	}
}

// TestMigrateAPIKeys_NoRawKeyColumn enforces secret hygiene structurally: the
// full column set of each M7 table must equal the DESIGNED set exactly (so a
// later "convenience column" fails loudly here), AND neither table's DDL may
// contain a substring that names a raw key, plaintext, encrypted envelope,
// ciphertext, bearer token, prompt, response, or Authorization material.
//
// Resolved ambiguity: the batch prompt listed the bare substring "token" as
// forbidden, but usage_records legitimately needs integer token-COUNT columns
// (tokens_in / tokens_out — the X-Venom-Tokens-In/Out metrics, 01 §6c). A
// count is a metric, never content, so — exactly as the established
// migrate_routing_test.go precedent does with "token_text"/"raw_error" — the
// forbidden token check targets token TEXT/CONTENT, not the bare word. Every
// other substring from the prompt is kept verbatim.
func TestMigrateAPIKeys_NoRawKeyColumn(t *testing.T) {
	db := migratedQuotaDB(t)

	designed := map[string][]string{
		"venom_api_keys": {
			"created_at",
			"id",
			"key_hash",
			"key_prefix",
			"label",
			"last_used_at",
			"revoked_at",
			"rpm_limit",
		},
		"usage_records": {
			"account_id",
			"api_key_id",
			"created_at",
			"fallback_attempts",
			"funding",
			"id",
			"latency_ms",
			"provider_id",
			"provider_model_id",
			"request_id",
			"status",
			"tier",
			"tokens_in",
			"tokens_out",
		},
	}
	for table, want := range designed {
		got := tableColumnNames(t, db, table)
		if len(got) != len(want) {
			t.Fatalf("%s columns = %v (len %d), want exactly %v (len %d)", table, got, len(got), want, len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s columns = %v, want exactly %v (shape is frozen; a new column must be reviewed for secret hygiene)", table, got, want)
			}
		}
	}

	forbidden := []string{
		"raw", "secret", "plaintext", "token_text", "token_content",
		"envelope", "ciphertext", "prompt", "response", "authorization", "bearer",
	}
	for _, table := range apiKeysTables {
		ddl := tableDDL(t, db, table)
		lower := strings.ToLower(ddl)
		for _, bad := range forbidden {
			if strings.Contains(lower, bad) {
				t.Errorf("%s DDL contains forbidden substring %q (a Venom key is verified, never recovered):\n%s", table, bad, ddl)
			}
		}
	}
}

// tableDDL returns the CREATE statement SQLite recorded for table.
func tableDDL(t *testing.T, db *DB, table string) string {
	t.Helper()
	var ddl string
	if err := db.Conn().QueryRow(
		`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, table,
	).Scan(&ddl); err != nil {
		t.Fatalf("read DDL for %s: %v", table, err)
	}
	return ddl
}

// insertAPIKeyRaw inserts a venom_api_keys row via raw SQL so a test can drive
// the table's CHECK/UNIQUE constraints directly (rpm accepts `any` so a test
// can pass a NULL, 0, or negative value the typed repo would never construct).
func insertAPIKeyRaw(db *DB, id, label, keyHash, keyPrefix string, rpm any) error {
	_, err := db.Conn().Exec(
		`INSERT INTO venom_api_keys (id, label, key_hash, key_prefix, rpm_limit, created_at)
		 VALUES (?, ?, ?, ?, ?, 0)`,
		id, label, keyHash, keyPrefix, rpm,
	)
	return err
}

// insertUsageRecordRaw inserts a minimal usage_records row referencing
// apiKeyID (which may be NULL via `any`).
func insertUsageRecordRaw(db *DB, id, requestID string, apiKeyID any, tier string) error {
	_, err := db.Conn().Exec(
		`INSERT INTO usage_records (id, request_id, api_key_id, tier, status, created_at)
		 VALUES (?, ?, ?, ?, 'succeeded', 0)`,
		id, requestID, apiKeyID, tier,
	)
	return err
}

// TestMigrateAPIKeys_ChecksBite proves every venom_api_keys CHECK/UNIQUE
// constraint rejects the value it is meant to (empty label, rpm_limit 0 or -1,
// a non-64-char key_hash, a duplicate key_hash) while a well-formed row and a
// NULL rpm_limit are accepted.
func TestMigrateAPIKeys_ChecksBite(t *testing.T) {
	db := migratedQuotaDB(t)

	if err := insertAPIKeyRaw(db, "k-ok", "prod key", hex64("a"), "vk_live_a", 60); err != nil {
		t.Fatalf("well-formed key rejected: %v", err)
	}
	if err := insertAPIKeyRaw(db, "k-null-rpm", "no limit", hex64("b"), "vk_live_b", nil); err != nil {
		t.Fatalf("NULL rpm_limit (no explicit limit) must be accepted: %v", err)
	}

	if err := insertAPIKeyRaw(db, "k-empty", "   ", hex64("c"), "vk_live_c", 60); err == nil {
		t.Fatalf("empty/whitespace label accepted, want CHECK rejection")
	}
	if err := insertAPIKeyRaw(db, "k-rpm0", "zero", hex64("d"), "vk_live_d", 0); err == nil {
		t.Fatalf("rpm_limit=0 accepted, want CHECK rejection (0 must never mean unknown)")
	}
	if err := insertAPIKeyRaw(db, "k-rpmneg", "neg", hex64("e"), "vk_live_e", -1); err == nil {
		t.Fatalf("rpm_limit=-1 accepted, want CHECK rejection")
	}
	if err := insertAPIKeyRaw(db, "k-shorthash", "short", "abc123", "vk_live_f", 60); err == nil {
		t.Fatalf("non-64-char key_hash accepted, want CHECK rejection")
	}
	if err := insertAPIKeyRaw(db, "k-dup", "dup", hex64("a"), "vk_live_a2", 60); err == nil {
		t.Fatalf("duplicate key_hash accepted, want UNIQUE rejection")
	}
}

// TestMigrateAPIKeys_UsageSurvivesKeyDeletion proves usage_records history is
// never erased when its key is deleted: the FK is ON DELETE SET NULL, so the
// usage row survives with api_key_id NULL (billing history outlives the key).
func TestMigrateAPIKeys_UsageSurvivesKeyDeletion(t *testing.T) {
	db := migratedQuotaDB(t)

	if err := insertAPIKeyRaw(db, "k-1", "prod", hex64("dead"), "vk_live_de", 60); err != nil {
		t.Fatalf("insert key: %v", err)
	}
	if err := insertUsageRecordRaw(db, "u-1", "req-1", "k-1", "pro"); err != nil {
		t.Fatalf("insert usage record: %v", err)
	}

	if _, err := db.Conn().Exec(`DELETE FROM venom_api_keys WHERE id = ?`, "k-1"); err != nil {
		t.Fatalf("delete key: %v", err)
	}

	var count int
	var apiKeyID *string
	if err := db.Conn().QueryRow(
		`SELECT COUNT(*), MAX(api_key_id) FROM usage_records WHERE id = ?`, "u-1",
	).Scan(&count, &apiKeyID); err != nil {
		t.Fatalf("read usage record after key delete: %v", err)
	}
	if count != 1 {
		t.Fatalf("usage record count after key delete = %d, want 1 (history must survive)", count)
	}
	if apiKeyID != nil {
		t.Fatalf("usage record api_key_id after key delete = %q, want NULL (ON DELETE SET NULL)", *apiKeyID)
	}
}
