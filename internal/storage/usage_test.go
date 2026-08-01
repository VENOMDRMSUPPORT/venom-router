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

// --- Aggregate (P6-CAPI-EXTRA-2, enables P6-UI-005) ---

// seedUsage inserts one usage row. Nil metrics stay NULL.
func seedUsage(t *testing.T, db *DB, id, tier string, account, model *string, tokensIn, tokensOut, latency *int, at int64) {
	t.Helper()
	if _, err := db.Conn().Exec(
		`INSERT INTO usage_records (id, request_id, tier, account_id, provider_model_id, status, latency_ms, tokens_in, tokens_out, created_at)
		 VALUES (?, ?, ?, ?, ?, 'success', ?, ?, ?, ?)`,
		id, id+"-req", tier, nullString(account), nullString(model), nullInt(latency), nullInt(tokensIn), nullInt(tokensOut), at,
	); err != nil {
		t.Fatalf("seed usage %s: %v", id, err)
	}
}

func findGroup(t *testing.T, groups []UsageGroup, key string) UsageGroup {
	t.Helper()
	for _, g := range groups {
		if g.Key != nil && *g.Key == key {
			return g
		}
	}
	t.Fatalf("group %q not found in %+v", key, groups)
	return UsageGroup{}
}

// TestUsageRecordRepo_AggregateExcludesUnknownsFromSums is THE test of this
// reader. A NULL token count is UNKNOWN, not zero:
//
//   - it must not be added to the sum (which would understate nothing but
//     silently claim the row contributed zero tokens),
//   - it must not enter the average's denominator (KnownCount), because dividing
//     by rows that reported nothing manufactures a lower average,
//   - and the UNKNOWN COUNT must be reported, so a dashboard can say "1 of 3
//     requests reports no token count" instead of quietly presenting a floor as
//     a total.
func TestUsageRecordRepo_AggregateExcludesUnknownsFromSums(t *testing.T) {
	db := migratedUsageDB(t)
	acct := "acct-1"
	model := "m1"
	// Two known token counts (10, 30) and one UNKNOWN.
	seedUsage(t, db, "u1", "pro", &acct, &model, intp(10), intp(1), intp(100), 1000)
	seedUsage(t, db, "u2", "pro", &acct, &model, intp(30), intp(3), nil, 1001)
	seedUsage(t, db, "u3", "pro", &acct, &model, nil, nil, intp(300), 1002)

	got, err := NewUsageRecordRepo(db).Aggregate(context.Background(), UsageQuery{Limit: 100})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}

	if got.Scanned != 3 {
		t.Fatalf("Scanned = %d, want 3", got.Scanned)
	}
	if got.Truncated {
		t.Fatalf("Truncated = true for 3 rows under a limit of 100, want false")
	}

	tokens := got.Totals.TokensIn
	if tokens.Sum == nil {
		t.Fatalf("TokensIn.Sum = nil, want 40 — two rows DID report a token count")
	}
	if *tokens.Sum != 40 {
		t.Errorf("TokensIn.Sum = %d, want 40 (10+30; the NULL row contributes nothing, not 0)", *tokens.Sum)
	}
	if tokens.KnownCount != 2 {
		t.Errorf("TokensIn.KnownCount = %d, want 2 — an unknown row must not enter the average's denominator", tokens.KnownCount)
	}
	if tokens.UnknownCount != 1 {
		t.Errorf("TokensIn.UnknownCount = %d, want 1 — the dashboard cannot say \"1 of 3 unknown\" unless this is reported", tokens.UnknownCount)
	}

	// Latency has its own independent unknown tally (u2 reported none).
	latency := got.Totals.LatencyMS
	if latency.Sum == nil || *latency.Sum != 400 {
		t.Errorf("LatencyMS.Sum = %v, want 400 (100+300)", latency.Sum)
	}
	if latency.KnownCount != 2 || latency.UnknownCount != 1 {
		t.Errorf("LatencyMS known/unknown = %d/%d, want 2/1 — each dimension tallies unknowns separately",
			latency.KnownCount, latency.UnknownCount)
	}

	// Requests is a ROW count, always known — a row exists whatever its metrics.
	if got.Totals.Requests != 3 {
		t.Errorf("Totals.Requests = %d, want 3", got.Totals.Requests)
	}
}

// TestUsageRecordRepo_AggregateAllUnknownReportsUnknownNotZero proves a group
// whose every row is unknown reports the sum as UNKNOWN (nil) rather than 0.
// Zero is a measurement; this is the absence of one, and rendering it as 0 would
// claim the tier consumed no tokens at all.
func TestUsageRecordRepo_AggregateAllUnknownReportsUnknownNotZero(t *testing.T) {
	db := migratedUsageDB(t)
	acct := "acct-dark"
	seedUsage(t, db, "u1", "lite", &acct, nil, nil, nil, nil, 1000)
	seedUsage(t, db, "u2", "lite", &acct, nil, nil, nil, nil, 1001)

	got, err := NewUsageRecordRepo(db).Aggregate(context.Background(), UsageQuery{Limit: 100})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}

	for name, metric := range map[string]UsageMetric{
		"TokensIn":  got.Totals.TokensIn,
		"TokensOut": got.Totals.TokensOut,
		"LatencyMS": got.Totals.LatencyMS,
	} {
		if metric.Sum != nil {
			t.Errorf("%s.Sum = %d, want nil — every contributing row was unknown, and 0 is a different claim", name, *metric.Sum)
		}
		if metric.KnownCount != 0 {
			t.Errorf("%s.KnownCount = %d, want 0", name, metric.KnownCount)
		}
		if metric.UnknownCount != 2 {
			t.Errorf("%s.UnknownCount = %d, want 2", name, metric.UnknownCount)
		}
	}
	// The requests still happened.
	if got.Totals.Requests != 2 {
		t.Errorf("Totals.Requests = %d, want 2", got.Totals.Requests)
	}
}

// TestUsageRecordRepo_AggregateGroupsByAccountModelAndTier proves the three
// groupings are independent and each carries its own unknown tallies.
func TestUsageRecordRepo_AggregateGroupsByAccountModelAndTier(t *testing.T) {
	db := migratedUsageDB(t)
	a1, a2 := "acct-1", "acct-2"
	m1, m2 := "model-1", "model-2"
	seedUsage(t, db, "u1", "pro", &a1, &m1, intp(10), intp(1), intp(10), 1000)
	seedUsage(t, db, "u2", "pro", &a1, &m2, intp(20), intp(2), intp(20), 1001)
	seedUsage(t, db, "u3", "lite", &a2, &m1, nil, intp(3), intp(30), 1002)

	got, err := NewUsageRecordRepo(db).Aggregate(context.Background(), UsageQuery{Limit: 100})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}

	acct1 := findGroup(t, got.ByAccount, a1)
	if acct1.Requests != 2 || acct1.TokensIn.Sum == nil || *acct1.TokensIn.Sum != 30 {
		t.Errorf("acct-1 = (%d requests, tokens_in %v), want (2, 30)", acct1.Requests, acct1.TokensIn.Sum)
	}
	acct2 := findGroup(t, got.ByAccount, a2)
	if acct2.TokensIn.Sum != nil || acct2.TokensIn.UnknownCount != 1 {
		t.Errorf("acct-2 tokens_in = (%v, unknown %d), want (nil, 1)", acct2.TokensIn.Sum, acct2.TokensIn.UnknownCount)
	}

	model1 := findGroup(t, got.ByModel, m1)
	if model1.Requests != 2 {
		t.Errorf("model-1 requests = %d, want 2 (one from each account)", model1.Requests)
	}
	if model1.TokensIn.KnownCount != 1 || model1.TokensIn.UnknownCount != 1 {
		t.Errorf("model-1 tokens_in known/unknown = %d/%d, want 1/1", model1.TokensIn.KnownCount, model1.TokensIn.UnknownCount)
	}

	pro := findGroup(t, got.ByTier, "pro")
	if pro.Requests != 2 {
		t.Errorf("tier pro requests = %d, want 2", pro.Requests)
	}
	lite := findGroup(t, got.ByTier, "lite")
	if lite.Requests != 1 {
		t.Errorf("tier lite requests = %d, want 1", lite.Requests)
	}
}

// TestUsageRecordRepo_AggregateUnattributedRowsGroupSeparately proves a row whose
// account_id or provider_model_id is NULL forms its OWN unattributed group rather
// than being folded into an arbitrary real one or dropped.
//
// Dropping it would understate total usage; folding it into a named account would
// attribute consumption to an account that did not incur it. Both are worse than
// an explicitly unattributed bucket.
func TestUsageRecordRepo_AggregateUnattributedRowsGroupSeparately(t *testing.T) {
	db := migratedUsageDB(t)
	acct := "acct-known"
	seedUsage(t, db, "u1", "pro", &acct, nil, intp(5), intp(1), intp(10), 1000)
	seedUsage(t, db, "u2", "pro", nil, nil, intp(7), intp(1), intp(10), 1001)

	got, err := NewUsageRecordRepo(db).Aggregate(context.Background(), UsageQuery{Limit: 100})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}

	var unattributed *UsageGroup
	for i := range got.ByAccount {
		if got.ByAccount[i].Key == nil {
			unattributed = &got.ByAccount[i]
		}
	}
	if unattributed == nil {
		t.Fatalf("no unattributed account group — the NULL-account row was dropped or misattributed: %+v", got.ByAccount)
	}
	if unattributed.Requests != 1 || unattributed.TokensIn.Sum == nil || *unattributed.TokensIn.Sum != 7 {
		t.Errorf("unattributed = (%d requests, tokens %v), want (1, 7)", unattributed.Requests, unattributed.TokensIn.Sum)
	}
	// Both rows are still counted overall.
	if got.Totals.Requests != 2 {
		t.Errorf("Totals.Requests = %d, want 2 — an unattributed row is still a request", got.Totals.Requests)
	}
	// Every model here is NULL, so ByModel has exactly one unattributed group.
	if len(got.ByModel) != 1 || got.ByModel[0].Key != nil {
		t.Errorf("ByModel = %+v, want exactly one unattributed group", got.ByModel)
	}
}

// TestUsageRecordRepo_AggregateTimeWindow proves the window filters on created_at
// inclusively at `from` and exclusively at `to` — and that the reported window
// echoes what was applied.
func TestUsageRecordRepo_AggregateTimeWindow(t *testing.T) {
	db := migratedUsageDB(t)
	acct := "acct-1"
	seedUsage(t, db, "before", "pro", &acct, nil, intp(1), nil, nil, 900)
	seedUsage(t, db, "at-from", "pro", &acct, nil, intp(2), nil, nil, 1000)
	seedUsage(t, db, "inside", "pro", &acct, nil, intp(4), nil, nil, 1500)
	seedUsage(t, db, "at-to", "pro", &acct, nil, intp(8), nil, nil, 2000)

	from := time.Unix(1000, 0)
	to := time.Unix(2000, 0)
	got, err := NewUsageRecordRepo(db).Aggregate(context.Background(), UsageQuery{From: &from, To: &to, Limit: 100})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}

	// at-from (2) + inside (4) = 6. `before` and `at-to` are outside.
	if got.Totals.TokensIn.Sum == nil || *got.Totals.TokensIn.Sum != 6 {
		t.Fatalf("TokensIn.Sum = %v, want 6 (from is inclusive, to is exclusive)", got.Totals.TokensIn.Sum)
	}
	if got.Totals.Requests != 2 {
		t.Fatalf("Requests = %d, want 2", got.Totals.Requests)
	}
	if got.From == nil || !got.From.Equal(from) || got.To == nil || !got.To.Equal(to) {
		t.Fatalf("reported window = (%v,%v), want (%v,%v)", got.From, got.To, from, to)
	}
}

// TestUsageRecordRepo_AggregateIsBounded proves the scan is bounded and SAYS SO
// when it stops early. A silently-capped aggregate presents a partial total as a
// complete one.
func TestUsageRecordRepo_AggregateIsBounded(t *testing.T) {
	db := migratedUsageDB(t)
	acct := "acct-1"
	for i := 0; i < 5; i++ {
		seedUsage(t, db, "u"+string(rune('a'+i)), "pro", &acct, nil, intp(1), nil, nil, int64(1000+i))
	}

	tests := []struct {
		name          string
		limit         int
		wantScanned   int
		wantTruncated bool
	}{
		{name: "limit under the row count truncates and reports it", limit: 2, wantScanned: 2, wantTruncated: true},
		{name: "limit at the row count is not truncated", limit: 5, wantScanned: 5, wantTruncated: false},
		{name: "limit above the row count is not truncated", limit: 50, wantScanned: 5, wantTruncated: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NewUsageRecordRepo(db).Aggregate(context.Background(), UsageQuery{Limit: tc.limit})
			if err != nil {
				t.Fatalf("Aggregate: %v", err)
			}
			if got.Scanned != tc.wantScanned {
				t.Fatalf("Scanned = %d, want %d", got.Scanned, tc.wantScanned)
			}
			if got.Truncated != tc.wantTruncated {
				t.Fatalf("Truncated = %v, want %v", got.Truncated, tc.wantTruncated)
			}
		})
	}
}

// TestUsageRecordRepo_AggregateEmptyIsNotZero proves an empty window reports zero
// REQUESTS (a real count of rows) but UNKNOWN sums — there is nothing to have
// measured, so no measurement is claimed.
func TestUsageRecordRepo_AggregateEmptyIsNotZero(t *testing.T) {
	db := migratedUsageDB(t)

	got, err := NewUsageRecordRepo(db).Aggregate(context.Background(), UsageQuery{Limit: 100})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if got.Totals.Requests != 0 {
		t.Fatalf("Requests = %d, want 0", got.Totals.Requests)
	}
	if got.Totals.TokensIn.Sum != nil {
		t.Fatalf("TokensIn.Sum = %d, want nil for an empty window", *got.Totals.TokensIn.Sum)
	}
	if got.Totals.TokensIn.UnknownCount != 0 {
		t.Fatalf("TokensIn.UnknownCount = %d, want 0 — no row was unknown because no row exists", got.Totals.TokensIn.UnknownCount)
	}
	for _, groups := range [][]UsageGroup{got.ByAccount, got.ByModel, got.ByTier} {
		if len(groups) != 0 {
			t.Fatalf("groups = %+v, want empty", groups)
		}
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
