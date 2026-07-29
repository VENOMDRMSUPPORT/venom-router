package observability

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

func obsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := storage.Migrate(context.Background(), db); err != nil {
		t.Fatalf("storage.Migrate: %v", err)
	}
	return db.Conn()
}

var obsNow = time.Unix(1_700_000_000, 0)

func sampleDecision(id string) RouteDecision {
	return RouteDecision{
		ID:                    id,
		RequestID:             "req-1",
		Tier:                  "pro",
		WorkloadProfileBucket: "standard",
		CandidateSummary:      CandidateSummary{TotalCandidates: 5, EligibleGroups: 2, GroupKeys: []string{"P1/M1", "P2/M2"}},
		ExclusionReasons:      map[string]int{"funding_ineligible": 1, "capability_uncertified": 2},
		ChosenProviderID:      "P1",
		ChosenProviderModelID: "M1",
		ChosenFunding:         "free",
		Scores:                map[string]float64{"chosen_quality": 0.82},
		RequestedThinking:     "extended",
		AppliedThinking:       "extended",
		TierClamped:           false,
		CertifiedClamped:      true,
		CreatedAt:             obsNow,
	}
}

// TestRouteRecorder_DecisionAndAttemptsRoundTrip proves a decision and its
// attempts are written and read back with every field intact.
func TestRouteRecorder_DecisionAndAttemptsRoundTrip(t *testing.T) {
	db := obsTestDB(t)
	rec := NewRouteRecorder(db, Default())
	ctx := context.Background()

	d := sampleDecision("dec-1")
	if err := rec.RecordDecision(ctx, d); err != nil {
		t.Fatalf("RecordDecision: %v", err)
	}
	lat := 123
	fin := obsNow.Add(2 * time.Second)
	a := RouteAttempt{
		ID: "att-1", RouteDecisionID: "dec-1", AttemptNumber: 1,
		ProviderID: "P1", AccountID: "acc-1", OfferingOperationID: "off-1",
		LatencyMS: &lat, Status: RouteStatusSuccess, ThinkingClamped: true,
		ReservationID: "resv-1", StartedAt: obsNow, FinishedAt: &fin,
	}
	if err := rec.RecordAttempt(ctx, a); err != nil {
		t.Fatalf("RecordAttempt: %v", err)
	}

	var (
		tier, bucket, chosenProv, chosenFunding, summary, reasons, scores string
		certClamped                                                       int
	)
	row := db.QueryRow(`SELECT tier, workload_profile_bucket, chosen_provider_id, chosen_funding, candidate_summary, exclusion_reasons, scores, certified_clamped FROM route_decisions WHERE id = ?`, "dec-1")
	if err := row.Scan(&tier, &bucket, &chosenProv, &chosenFunding, &summary, &reasons, &scores, &certClamped); err != nil {
		t.Fatalf("scan decision: %v", err)
	}
	if tier != "pro" || bucket != "standard" || chosenProv != "P1" || chosenFunding != "free" || certClamped != 1 {
		t.Fatalf("decision fields wrong: tier=%q bucket=%q prov=%q funding=%q certClamped=%d", tier, bucket, chosenProv, chosenFunding, certClamped)
	}
	if !strings.Contains(summary, `"total_candidates":5`) || !strings.Contains(reasons, `"funding_ineligible":1`) || !strings.Contains(scores, `"chosen_quality":0.82`) {
		t.Fatalf("JSON fields wrong: summary=%s reasons=%s scores=%s", summary, reasons, scores)
	}

	var attNum, latMS, thinkClamped int
	var provID, accID, offID, status, resvID string
	var finished int64
	ar := db.QueryRow(`SELECT attempt_number, provider_id, account_id, offering_operation_id, latency_ms, status, thinking_clamped, reservation_id, finished_at FROM route_attempts WHERE id = ?`, "att-1")
	if err := ar.Scan(&attNum, &provID, &accID, &offID, &latMS, &status, &thinkClamped, &resvID, &finished); err != nil {
		t.Fatalf("scan attempt: %v", err)
	}
	if attNum != 1 || provID != "P1" || accID != "acc-1" || offID != "off-1" || latMS != 123 || status != "success" || thinkClamped != 1 || resvID != "resv-1" || finished != fin.Unix() {
		t.Fatalf("attempt fields wrong: %d %q %q %q %d %q %d %q %d", attNum, provID, accID, offID, latMS, status, thinkClamped, resvID, finished)
	}
}

// TestRouteRecorder_CascadeDeletesAttempts proves the ON DELETE CASCADE from a
// decision to its attempts holds.
//
// Mutation row O-M3: orphan attempts (drop the cascade FK) → this test RED.
func TestRouteRecorder_CascadeDeletesAttempts(t *testing.T) {
	db := obsTestDB(t)
	rec := NewRouteRecorder(db, Default())
	ctx := context.Background()

	_ = rec.RecordDecision(ctx, sampleDecision("dec-c"))
	for i := 1; i <= 3; i++ {
		_ = rec.RecordAttempt(ctx, RouteAttempt{
			ID: "att-c-" + string(rune('0'+i)), RouteDecisionID: "dec-c", AttemptNumber: i,
			ProviderID: "P1", AccountID: "acc", OfferingOperationID: "off", Status: RouteStatusFailure, StartedAt: obsNow,
		})
	}

	var before int
	_ = db.QueryRow(`SELECT COUNT(*) FROM route_attempts WHERE route_decision_id = ?`, "dec-c").Scan(&before)
	if before != 3 {
		t.Fatalf("expected 3 attempts before delete, got %d", before)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM route_decisions WHERE id = ?`, "dec-c"); err != nil {
		t.Fatalf("delete decision: %v", err)
	}
	var after int
	_ = db.QueryRow(`SELECT COUNT(*) FROM route_attempts WHERE route_decision_id = ?`, "dec-c").Scan(&after)
	if after != 0 {
		t.Fatalf("ON DELETE CASCADE did not remove attempts; %d remain", after)
	}
}

// TestRouteRecorder_GracefulDegradation proves a write failure never aborts the
// caller: with no tables present the recorder logs and returns nil.
//
// Mutation row O-M2: return the write error instead of nil → this test RED.
func TestRouteRecorder_GracefulDegradation(t *testing.T) {
	// A DB with NO migrations → the route_* tables do not exist → every insert
	// fails. The recorder must still return nil.
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var buf bytes.Buffer
	rec := NewRouteRecorder(db.Conn(), New(slog.NewJSONHandler(&buf, nil)))
	ctx := context.Background()

	if err := rec.RecordDecision(ctx, sampleDecision("dec-x")); err != nil {
		t.Fatalf("RecordDecision must not abort routing, got %v", err)
	}
	if err := rec.RecordAttempt(ctx, RouteAttempt{ID: "att-x", RouteDecisionID: "dec-x", AttemptNumber: 1, ProviderID: "P", AccountID: "a", OfferingOperationID: "o", Status: RouteStatusFailure, StartedAt: obsNow}); err != nil {
		t.Fatalf("RecordAttempt must not abort routing, got %v", err)
	}
	if !strings.Contains(buf.String(), "record failed") {
		t.Fatalf("expected a logged write failure, got: %s", buf.String())
	}
}

// TestRouteRecorder_SecretFree plants a credential-shaped token AND a plain
// marker in the free-form status input, writes, then reads back EVERY column of
// both tables via SELECT * and asserts neither canary appears anywhere — the
// status is normalized to a closed value and no column has a content path.
//
// Mutation row O-M1: store the raw status verbatim → the canary appears in the
// status column → this test RED.
func TestRouteRecorder_SecretFree(t *testing.T) {
	const credCanary = "Bearer sk-SECRETcanary1234567890"
	const plainCanary = "PLAIN-CANARY-marker-7f3a"

	db := obsTestDB(t)
	rec := NewRouteRecorder(db, Default())
	ctx := context.Background()

	_ = rec.RecordDecision(ctx, sampleDecision("dec-s"))
	_ = rec.RecordAttempt(ctx, RouteAttempt{
		ID: "att-s", RouteDecisionID: "dec-s", AttemptNumber: 1,
		ProviderID: "P1", AccountID: "acc", OfferingOperationID: "off",
		// A raw provider error smuggled into status — must be normalized away.
		Status:    RouteStatus("raw provider error: " + credCanary + " " + plainCanary),
		StartedAt: obsNow,
	})

	for _, table := range []string{"route_decisions", "route_attempts"} {
		assertNoCanary(t, db, table, credCanary, plainCanary, "sk-SECRET")
	}
}

// assertNoCanary reads every column of every row of table via SELECT * and
// fails if any cell contains any of the markers.
func assertNoCanary(t *testing.T, db *sql.DB, table string, markers ...string) {
	t.Helper()
	rows, err := db.Query("SELECT * FROM " + table) //nolint:gosec // table is a fixed test constant
	if err != nil {
		t.Fatalf("select * from %s: %v", table, err)
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns: %v", err)
	}
	for rows.Next() {
		cells := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("scan %s: %v", table, err)
		}
		for i, cell := range cells {
			var s string
			switch v := cell.(type) {
			case string:
				s = v
			case []byte:
				s = string(v)
			default:
				continue
			}
			for _, m := range markers {
				if strings.Contains(s, m) {
					t.Fatalf("canary %q leaked into %s.%s: %q", m, table, cols[i], s)
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err %s: %v", table, err)
	}
}
