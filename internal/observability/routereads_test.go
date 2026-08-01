package observability

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"
)

// seedDecisionAt writes one decision through the REAL RouteRecorder at the
// given created_at, so every read test below reads exactly what production
// writes (never a hand-rolled INSERT that could drift from the writer).
func seedDecisionAt(t *testing.T, rec *RouteRecorder, id, requestID string, at time.Time) RouteDecision {
	t.Helper()
	d := sampleDecision(id)
	d.RequestID = requestID
	d.CreatedAt = at
	if err := rec.RecordDecision(context.Background(), d); err != nil {
		t.Fatalf("RecordDecision(%s): %v", id, err)
	}
	return d
}

func seedAttempt(t *testing.T, rec *RouteRecorder, a RouteAttempt) {
	t.Helper()
	if err := rec.RecordAttempt(context.Background(), a); err != nil {
		t.Fatalf("RecordAttempt(%s): %v", a.ID, err)
	}
}

// TestRouteReader_ListDecisions_NewestFirstDeterministicTieBreak pins the
// ordering contract: newest first, and two rows sharing an identical
// created_at still come back in a FIXED order (the secondary id key). Without
// the secondary ORDER BY key SQLite is free to return the tied pair in either
// order, so a paginated client could see the same row twice or miss one.
func TestRouteReader_ListDecisions_NewestFirstDeterministicTieBreak(t *testing.T) {
	db := obsTestDB(t)
	rec := NewRouteRecorder(db, Default())
	reader := NewRouteReader(db)

	// dec-a and dec-b deliberately share obsNow; dec-c is newer.
	seedDecisionAt(t, rec, "dec-a", "req-a", obsNow)
	seedDecisionAt(t, rec, "dec-b", "req-b", obsNow)
	seedDecisionAt(t, rec, "dec-c", "req-c", obsNow.Add(time.Minute))

	// Read repeatedly: a non-deterministic ORDER BY may well agree with the
	// expectation once by chance, so the assertion is on a stable answer
	// across calls as well as on the exact expected sequence.
	want := []string{"dec-c", "dec-b", "dec-a"}
	for i := 0; i < 3; i++ {
		got, err := reader.ListDecisions(context.Background(), 10, 0)
		if err != nil {
			t.Fatalf("ListDecisions: %v", err)
		}
		ids := make([]string, 0, len(got))
		for _, d := range got {
			ids = append(ids, d.ID)
		}
		if !reflect.DeepEqual(ids, want) {
			t.Fatalf("call %d: decision order = %v, want %v", i, ids, want)
		}
	}
}

// TestRouteReader_ListDecisions_LimitOffset pins the paging window and the
// out-of-range posture: past the end is an EMPTY list, never an error.
func TestRouteReader_ListDecisions_LimitOffset(t *testing.T) {
	db := obsTestDB(t)
	rec := NewRouteRecorder(db, Default())
	reader := NewRouteReader(db)

	for i, id := range []string{"dec-1", "dec-2", "dec-3"} {
		seedDecisionAt(t, rec, id, "req-"+id, obsNow.Add(time.Duration(i)*time.Minute))
	}

	tests := []struct {
		name   string
		limit  int
		offset int
		want   []string
	}{
		{name: "first page", limit: 2, offset: 0, want: []string{"dec-3", "dec-2"}},
		{name: "second page", limit: 2, offset: 2, want: []string{"dec-1"}},
		{name: "offset past the end is empty, not an error", limit: 2, offset: 99, want: []string{}},
		{name: "negative offset clamps to the first page", limit: 1, offset: -5, want: []string{"dec-3"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := reader.ListDecisions(context.Background(), tc.limit, tc.offset)
			if err != nil {
				t.Fatalf("ListDecisions: %v", err)
			}
			ids := make([]string, 0, len(got))
			for _, d := range got {
				ids = append(ids, d.ID)
			}
			if !reflect.DeepEqual(ids, tc.want) {
				t.Fatalf("ids = %v, want %v", ids, tc.want)
			}
		})
	}
}

// TestRouteReader_GetExplanation_FullFidelity proves every decision field
// survives the round trip and that attempts come back ordered by attempt
// number regardless of the order they were written in.
func TestRouteReader_GetExplanation_FullFidelity(t *testing.T) {
	db := obsTestDB(t)
	rec := NewRouteRecorder(db, Default())
	reader := NewRouteReader(db)
	ctx := context.Background()

	want := seedDecisionAt(t, rec, "dec-1", "req-fidelity", obsNow)

	lat := 123
	fin := obsNow.Add(2 * time.Second)
	// Written third, second, first — the reader must sort them.
	seedAttempt(t, rec, RouteAttempt{
		ID: "att-3", RouteDecisionID: "dec-1", AttemptNumber: 3,
		ProviderID: "P3", AccountID: "acc-3", OfferingOperationID: "off-3",
		Status: RouteStatusSuccess, StartedAt: obsNow.Add(4 * time.Second),
	})
	seedAttempt(t, rec, RouteAttempt{
		ID: "att-2", RouteDecisionID: "dec-1", AttemptNumber: 2,
		ProviderID: "P2", AccountID: "acc-2", OfferingOperationID: "off-2",
		Status: RouteStatusTimeout, ThinkingClamped: true, StartedAt: obsNow.Add(2 * time.Second),
	})
	seedAttempt(t, rec, RouteAttempt{
		ID: "att-1", RouteDecisionID: "dec-1", AttemptNumber: 1,
		ProviderID: "P1", AccountID: "acc-1", OfferingOperationID: "off-1",
		LatencyMS: &lat, Status: RouteStatusFailure, ReservationID: "resv-1",
		StartedAt: obsNow, FinishedAt: &fin,
	})

	got, err := reader.GetExplanation(ctx, "req-fidelity")
	if err != nil {
		t.Fatalf("GetExplanation: %v", err)
	}

	if !reflect.DeepEqual(got.Decision, want) {
		t.Fatalf("decision round trip mismatch:\n got %+v\nwant %+v", got.Decision, want)
	}

	if len(got.Attempts) != 3 {
		t.Fatalf("attempts = %d, want 3", len(got.Attempts))
	}
	for i, a := range got.Attempts {
		if a.AttemptNumber != i+1 {
			t.Fatalf("attempt[%d].AttemptNumber = %d, want %d", i, a.AttemptNumber, i+1)
		}
		if a.RouteDecisionID != "dec-1" {
			t.Fatalf("attempt[%d].RouteDecisionID = %q, want dec-1", i, a.RouteDecisionID)
		}
	}

	first := got.Attempts[0]
	if first.ID != "att-1" || first.ProviderID != "P1" || first.AccountID != "acc-1" ||
		first.OfferingOperationID != "off-1" || first.Status != RouteStatusFailure ||
		first.ReservationID != "resv-1" || first.ThinkingClamped {
		t.Fatalf("attempt 1 fidelity mismatch: %+v", first)
	}
	if first.LatencyMS == nil || *first.LatencyMS != lat {
		t.Fatalf("attempt 1 LatencyMS = %v, want %d", first.LatencyMS, lat)
	}
	if first.FinishedAt == nil || !first.FinishedAt.Equal(fin) {
		t.Fatalf("attempt 1 FinishedAt = %v, want %v", first.FinishedAt, fin)
	}
	if !first.StartedAt.Equal(obsNow) {
		t.Fatalf("attempt 1 StartedAt = %v, want %v", first.StartedAt, obsNow)
	}

	// Attempt 2 wrote no latency and no finish time: those must read back as
	// nil (unknown), never as a fabricated zero.
	second := got.Attempts[1]
	if second.LatencyMS != nil {
		t.Fatalf("attempt 2 LatencyMS = %v, want nil (NULL must not become 0)", *second.LatencyMS)
	}
	if second.FinishedAt != nil {
		t.Fatalf("attempt 2 FinishedAt = %v, want nil (NULL must not become a zero time)", second.FinishedAt)
	}
	if !second.ThinkingClamped {
		t.Fatalf("attempt 2 ThinkingClamped = false, want true")
	}
}

// TestRouteReader_GetExplanation_UnknownRequestID proves an absent request id
// is a TYPED error, never a zero value with a nil error — the fail-closed
// posture that keeps a 404 from becoming an empty 200.
func TestRouteReader_GetExplanation_UnknownRequestID(t *testing.T) {
	db := obsTestDB(t)
	reader := NewRouteReader(db)

	got, err := reader.GetExplanation(context.Background(), "no-such-request")
	if !errors.Is(err, ErrRouteDecisionNotFound) {
		t.Fatalf("err = %v, want ErrRouteDecisionNotFound", err)
	}
	if !reflect.DeepEqual(got, RouteExplanation{}) {
		t.Fatalf("explanation = %+v, want the zero value alongside the error", got)
	}
}

// TestRouteReader_JunkStatusNormalizesOnRead proves the reader is as
// fail-closed as the writer: a status value outside the closed vocabulary —
// however it got into the column — reads back as RouteStatusUnknown, so raw
// provider text can never reach a diagnostics payload through this field.
func TestRouteReader_JunkStatusNormalizesOnRead(t *testing.T) {
	db := obsTestDB(t)
	rec := NewRouteRecorder(db, Default())
	reader := NewRouteReader(db)
	ctx := context.Background()

	seedDecisionAt(t, rec, "dec-junk", "req-junk", obsNow)

	// Bypass the recorder deliberately: this is the one thing the writer's
	// own normalization makes unreachable, and the read path must not trust
	// the column's contents either.
	const junk = "upstream said: Bearer sk-live-DEADBEEF expired"
	if _, err := db.ExecContext(ctx,
		`INSERT INTO route_attempts (id, route_decision_id, attempt_number, provider_id,
			account_id, offering_operation_id, status, thinking_clamped, started_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?)`,
		"att-junk", "dec-junk", 1, "P1", "acc-1", "off-1", junk, obsNow.Unix(),
	); err != nil {
		t.Fatalf("insert junk status: %v", err)
	}

	got, err := reader.GetExplanation(ctx, "req-junk")
	if err != nil {
		t.Fatalf("GetExplanation: %v", err)
	}
	if len(got.Attempts) != 1 {
		t.Fatalf("attempts = %d, want 1", len(got.Attempts))
	}
	if got.Attempts[0].Status != RouteStatusUnknown {
		t.Fatalf("status = %q, want %q", got.Attempts[0].Status, RouteStatusUnknown)
	}
}

// TestRouteReader_ListDecisions_NullScoresStayNil proves a NULL scores column
// reads back as a nil map (unknown), never as an empty-but-present map that a
// client could mistake for "scored, with no dimensions".
func TestRouteReader_ListDecisions_NullScoresStayNil(t *testing.T) {
	db := obsTestDB(t)
	rec := NewRouteRecorder(db, Default())
	reader := NewRouteReader(db)

	d := sampleDecision("dec-noscore")
	d.RequestID = "req-noscore"
	d.Scores = nil
	d.ChosenProviderID = ""
	d.ChosenProviderModelID = ""
	d.ChosenFunding = ""
	d.RequestedThinking = ""
	d.AppliedThinking = ""
	if err := rec.RecordDecision(context.Background(), d); err != nil {
		t.Fatalf("RecordDecision: %v", err)
	}

	got, err := reader.GetExplanation(context.Background(), "req-noscore")
	if err != nil {
		t.Fatalf("GetExplanation: %v", err)
	}
	if got.Decision.Scores != nil {
		t.Fatalf("Scores = %v, want nil for a NULL column", got.Decision.Scores)
	}
	if got.Decision.ChosenProviderID != "" || got.Decision.ChosenFunding != "" || got.Decision.AppliedThinking != "" {
		t.Fatalf("NULL text columns must read back as empty strings: %+v", got.Decision)
	}
	if got.Attempts == nil {
		t.Fatalf("Attempts = nil, want an empty non-nil slice for a decision with no attempts")
	}
}

// obsSQLDB is a compile-time reminder that this reader takes the same
// *sql.DB the recorder does (never a *storage.DB), so both sit on one
// connection pool.
var _ func(*sql.DB) *RouteReader = NewRouteReader
