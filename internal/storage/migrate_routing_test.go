package storage

import (
	"context"
	"sort"
	"strings"
	"testing"
)

// routingVersion is the goose version of the M6 routing migration
// (00011_routing.sql).
const routingVersion = 11

// routingTables enumerates the four M6 tables in creation order.
var routingTables = []string{
	"route_decisions",
	"route_attempts",
	"circuit_breakers",
	"deficit_cells",
}

// TestMigrateRouting_UpDownUp proves 00011 creates the four M6 routing
// tables, rolls back to exactly the pre-M6 shape (spot-checked: probe_runs
// and quota_reservations survive), and re-applies. The rollback loop is
// count-agnostic: it rolls back every migration at or above
// routingVersion, so a later migration lands without silently breaking
// this test (mirrors TestMigrateQuotaReconciliation_UpDownUp).
func TestMigrateRouting_UpDownUp(t *testing.T) {
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
	for _, table := range routingTables {
		assertTableExists(t, db, table, true)
	}
	assertColumnExists(t, db, "route_decisions", "workload_profile_bucket", true)
	assertColumnExists(t, db, "route_decisions", "exclusion_reasons", true)
	assertColumnExists(t, db, "route_attempts", "route_decision_id", true)
	assertColumnExists(t, db, "route_attempts", "reservation_id", true)
	assertColumnExists(t, db, "circuit_breakers", "next_probe_at", true)
	assertColumnExists(t, db, "deficit_cells", "deficit", true)

	for currentVersion(t, db) >= routingVersion {
		if _, err := DownOne(ctx, db); err != nil {
			t.Fatalf("DownOne() error = %v", err)
		}
	}
	for _, table := range routingTables {
		assertTableExists(t, db, table, false)
	}
	// Pre-M6 tables must survive rolling back only 00011.
	assertTableExists(t, db, "probe_runs", true)
	assertTableExists(t, db, "quota_reservations", true)

	if _, err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() (re-up) error = %v", err)
	}
	for _, table := range routingTables {
		assertTableExists(t, db, table, true)
	}
}

// tableColumnNames returns every column name of table, lowercased and
// sorted, via SQLite's pragma_table_info introspection.
func tableColumnNames(t *testing.T, db *DB, table string) []string {
	t.Helper()

	rows, err := db.Conn().Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatalf("query pragma_table_info(%s): %v", table, err)
	}
	defer func() { _ = rows.Close() }()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan pragma_table_info(%s) row: %v", table, err)
		}
		names = append(names, strings.ToLower(name))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate pragma_table_info(%s): %v", table, err)
	}
	sort.Strings(names)
	return names
}

// TestMigrateRouting_RecordsAreSecretFree enforces 05 §7 structurally:
// no column of route_decisions or route_attempts may carry (by name) a
// prompt, response, token content, raw provider error, or authorization
// material — AND the full column set of each table must equal the
// designed set exactly, so a later "convenience column" fails loudly
// here rather than silently widening the observability surface.
func TestMigrateRouting_RecordsAreSecretFree(t *testing.T) {
	db := migratedQuotaDB(t)

	forbidden := []string{
		"prompt", "response", "messages", "content",
		"authorization", "api_key", "token_text", "raw_error",
	}
	designed := map[string][]string{
		"route_decisions": {
			"applied_thinking",
			"candidate_summary",
			"certified_clamped",
			"chosen_funding",
			"chosen_provider_id",
			"chosen_provider_model_id",
			"created_at",
			"exclusion_reasons",
			"id",
			"request_id",
			"requested_thinking",
			"scores",
			"tier",
			"tier_clamped",
			"workload_profile_bucket",
		},
		"route_attempts": {
			"account_id",
			"attempt_number",
			"finished_at",
			"id",
			"latency_ms",
			"offering_operation_id",
			"provider_id",
			"reservation_id",
			"route_decision_id",
			"started_at",
			"status",
			"thinking_clamped",
		},
	}

	for table, want := range designed {
		got := tableColumnNames(t, db, table)
		for _, col := range got {
			for _, bad := range forbidden {
				if strings.Contains(col, bad) {
					t.Errorf("%s.%s matches forbidden substring %q (05 §7: route records must be secret-free)", table, col, bad)
				}
			}
		}
		if len(got) != len(want) {
			t.Fatalf("%s columns = %v (len %d), want exactly %v (len %d)", table, got, len(got), want, len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s columns = %v, want exactly %v (shape is frozen; a new column must be reviewed against 05 §7)", table, got, want)
			}
		}
	}
}

// insertRouteDecision seeds a minimal route_decisions row.
func insertRouteDecision(db *DB, id, tier string) error {
	_, err := db.Conn().Exec(
		`INSERT INTO route_decisions
		    (id, request_id, tier, workload_profile_bucket, candidate_summary, exclusion_reasons, created_at)
		 VALUES (?, ?, ?, 'standard', '{}', '[]', 0)`,
		id, "req-"+id, tier,
	)
	return err
}

// insertRouteAttempt seeds a minimal route_attempts row under decisionID.
func insertRouteAttempt(db *DB, id, decisionID string) error {
	_, err := db.Conn().Exec(
		`INSERT INTO route_attempts
		    (id, route_decision_id, attempt_number, provider_id, account_id, offering_operation_id, status, thinking_clamped, started_at)
		 VALUES (?, ?, 1, 'prov-x', 'acct-x', 'oo-x', 'succeeded', 0, 0)`,
		id, decisionID,
	)
	return err
}

// insertCircuitBreaker seeds a circuit_breakers row.
func insertCircuitBreaker(db *DB, scope, scopeID string) error {
	_, err := db.Conn().Exec(
		`INSERT INTO circuit_breakers
		    (scope, scope_id, state, consecutive_failures, backoff_multiplier)
		 VALUES (?, ?, 'closed', 0, 1)`,
		scope, scopeID,
	)
	return err
}

// insertDeficitCell seeds a deficit_cells row.
func insertDeficitCell(db *DB, tier, bucket, fundingClass string) error {
	_, err := db.Conn().Exec(
		`INSERT INTO deficit_cells (tier, workload_profile_bucket, funding_class, deficit, updated_at)
		 VALUES (?, ?, ?, 0.0, 0)`,
		tier, bucket, fundingClass,
	)
	return err
}

// TestMigrateRouting_VocabularyChecksAndCascade proves, in both
// directions, the M6 vocabulary CHECKs (route_decisions.tier and
// deficit_cells.tier/funding_class, circuit_breakers.scope), the
// composite primary keys, and that deleting a route decision cascades
// its attempts away (the decision owns them).
func TestMigrateRouting_VocabularyChecksAndCascade(t *testing.T) {
	db := migratedQuotaDB(t)

	// Happy path: every closed-vocabulary value is accepted.
	for _, tier := range []string{"lite", "pro", "max"} {
		if err := insertRouteDecision(db, "dec-"+tier, tier); err != nil {
			t.Fatalf("insert route_decisions with tier=%q: %v, want success", tier, err)
		}
	}
	if err := insertRouteDecision(db, "dec-bad", "ultra"); err == nil {
		t.Fatalf("insert route_decisions with tier=%q succeeded, want CHECK rejection", "ultra")
	}

	for _, scope := range []string{"account", "offering", "provider"} {
		if err := insertCircuitBreaker(db, scope, "scope-1"); err != nil {
			t.Fatalf("insert circuit_breakers with scope=%q: %v, want success", scope, err)
		}
	}
	if err := insertCircuitBreaker(db, "model", "scope-1"); err == nil {
		t.Fatalf("insert circuit_breakers with scope=%q succeeded, want CHECK rejection", "model")
	}
	if err := insertCircuitBreaker(db, "account", "scope-1"); err == nil {
		t.Fatalf("insert duplicate circuit_breakers (account, scope-1) succeeded, want composite-PK rejection")
	}
	if err := insertCircuitBreaker(db, "account", "scope-2"); err != nil {
		t.Fatalf("insert circuit_breakers (account, scope-2): %v, want success (distinct scope_id)", err)
	}

	for _, fundingClass := range []string{"free", "paid"} {
		if err := insertDeficitCell(db, "pro", "standard", fundingClass); err != nil {
			t.Fatalf("insert deficit_cells with funding_class=%q: %v, want success", fundingClass, err)
		}
	}
	if err := insertDeficitCell(db, "pro", "standard", "unknown"); err == nil {
		t.Fatalf("insert deficit_cells with funding_class=%q succeeded, want CHECK rejection (05 §8.1: cells exist only for free|paid)", "unknown")
	}
	if err := insertDeficitCell(db, "pro", "standard", "free"); err == nil {
		t.Fatalf("insert duplicate deficit_cells (pro, standard, free) succeeded, want composite-PK rejection")
	}
	if err := insertDeficitCell(db, "bad-tier", "standard", "free"); err == nil {
		t.Fatalf("insert deficit_cells with tier=%q succeeded, want CHECK rejection", "bad-tier")
	}

	// Cascade: the decision owns its attempts.
	if err := insertRouteAttempt(db, "att-1", "dec-pro"); err != nil {
		t.Fatalf("insert route_attempts under dec-pro: %v, want success", err)
	}
	if err := insertRouteAttempt(db, "att-orphan", "dec-missing"); err == nil {
		t.Fatalf("insert route_attempts under a missing decision succeeded, want FK rejection")
	}
	if _, err := db.Conn().Exec(`DELETE FROM route_decisions WHERE id = ?`, "dec-pro"); err != nil {
		t.Fatalf("delete route decision: %v, want success (cascades to its attempts)", err)
	}
	var count int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM route_attempts WHERE route_decision_id = ?`, "dec-pro").Scan(&count); err != nil {
		t.Fatalf("count attempts after decision delete: %v", err)
	}
	if count != 0 {
		t.Fatalf("attempts after decision delete = %d, want 0 (cascade)", count)
	}
}
