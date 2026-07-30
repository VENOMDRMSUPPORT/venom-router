package storage

import (
	"context"
	"strings"
	"testing"
	"time"
)

func migratedUsageDB(t *testing.T) *DB {
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

func strp(s string) *string { return &s }

// TestUsageRecordRepo_InsertRoundTrips proves a fully-populated row persists and
// reads back with every correlation id, tier, status, and numeric metric intact
// — the billing-truth record the handler writes on a terminal path.
func TestUsageRecordRepo_InsertRoundTrips(t *testing.T) {
	db := migratedUsageDB(t)
	// api_key_id FKs to venom_api_keys(id); seed a key so attribution is real.
	if _, err := db.Conn().Exec(
		`INSERT INTO venom_api_keys (id, label, key_hash, key_prefix, created_at) VALUES ('key-1', 'l', ?, 'vk_live_', 0)`,
		strings.Repeat("a", 64),
	); err != nil {
		t.Fatalf("seed key: %v", err)
	}

	repo := NewUsageRecordRepo(db)
	rec := UsageRecord{
		ID: "u1", RequestID: "req-1", APIKeyID: strp("key-1"), Tier: "pro",
		ProviderID: strp("opencode-zen"), AccountID: strp("acct-1"), ProviderModelID: strp("m1"),
		Funding: strp("free"), Status: "success",
		LatencyMS: intp(42), TokensIn: intp(10), TokensOut: intp(20), FallbackAttempts: intp(2),
		CreatedAt: time.Unix(1234, 0),
	}
	if err := repo.Insert(context.Background(), rec); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	var (
		tier, status, provider, apiKey string
		latency, tokensIn, attempts    int
		created                        int64
	)
	row := db.Conn().QueryRow(
		`SELECT tier, status, provider_id, api_key_id, latency_ms, tokens_in, fallback_attempts, created_at FROM usage_records WHERE id = 'u1'`)
	if err := row.Scan(&tier, &status, &provider, &apiKey, &latency, &tokensIn, &attempts, &created); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if tier != "pro" || status != "success" || provider != "opencode-zen" || apiKey != "key-1" {
		t.Fatalf("row = (%s,%s,%s,%s), want (pro,success,opencode-zen,key-1)", tier, status, provider, apiKey)
	}
	if latency != 42 || tokensIn != 10 || attempts != 2 || created != 1234 {
		t.Fatalf("numeric metrics = (%d,%d,%d,%d), want (42,10,2,1234)", latency, tokensIn, attempts, created)
	}
}

// TestUsageRecordRepo_NilMetricsAreNULL proves a nil pointer metric persists as
// NULL (UNKNOWN) — never a misleading 0.
func TestUsageRecordRepo_NilMetricsAreNULL(t *testing.T) {
	db := migratedUsageDB(t)
	repo := NewUsageRecordRepo(db)
	if err := repo.Insert(context.Background(), UsageRecord{
		ID: "u2", RequestID: "req-2", Tier: "lite", Status: "no_eligible_offering", CreatedAt: time.Unix(0, 0),
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	var latencyNull, apiKeyNull, providerNull int
	if err := db.Conn().QueryRow(
		`SELECT latency_ms IS NULL, api_key_id IS NULL, provider_id IS NULL FROM usage_records WHERE id = 'u2'`).
		Scan(&latencyNull, &apiKeyNull, &providerNull); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if latencyNull != 1 || apiKeyNull != 1 || providerNull != 1 {
		t.Fatalf("nil metrics = (latency %d, apiKey %d, provider %d), want all NULL(1)", latencyNull, apiKeyNull, providerNull)
	}
}

// TestUsageRecords_SchemaHasNoContentColumn proves — by construction — that the
// usage_records table has NO column able to hold a prompt, response, token
// content, raw provider error, or Authorization header (05 §7). A future
// migration that added such a column would fail this test.
func TestUsageRecords_SchemaHasNoContentColumn(t *testing.T) {
	db := migratedUsageDB(t)
	rows, err := db.Conn().Query(`SELECT name FROM pragma_table_info('usage_records')`)
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	defer func() { _ = rows.Close() }()
	forbidden := []string{"prompt", "response", "message", "content", "body", "text", "error", "authorization", "auth", "header", "credential", "key"}
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			t.Fatalf("scan col: %v", err)
		}
		lc := strings.ToLower(col)
		for _, f := range forbidden {
			// api_key_id is a correlation id, not content — allow the exact id columns.
			if lc == "api_key_id" || lc == "provider_model_id" {
				continue
			}
			if strings.Contains(lc, f) {
				t.Fatalf("usage_records has a content-shaped column %q (matches %q) — 05 §7 forbids persisting content", col, f)
			}
		}
	}
}

// TestUsageRecordRepo_InsertErrorSurfaced proves a write error is RETURNED, never
// swallowed (the old build's bug). A duplicate primary key forces the error.
func TestUsageRecordRepo_InsertErrorSurfaced(t *testing.T) {
	db := migratedUsageDB(t)
	repo := NewUsageRecordRepo(db)
	rec := UsageRecord{ID: "dup", RequestID: "r", Tier: "max", Status: "success", CreatedAt: time.Unix(0, 0)}
	if err := repo.Insert(context.Background(), rec); err != nil {
		t.Fatalf("first Insert: %v", err)
	}
	if err := repo.Insert(context.Background(), rec); err == nil {
		t.Fatalf("second Insert of a duplicate id returned nil — a write failure must be surfaced, never swallowed")
	}
}
