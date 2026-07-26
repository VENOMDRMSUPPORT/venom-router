package storage

import (
	"context"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

// seedReconciliationReservationRow seeds a quota_reservations row with
// full control over reconcile_attempts/lease_owner/lease_expires_at —
// the columns seedJanitorReservation (quota_janitor_test.go) does not
// expose — so this file's tests get full control over the exact
// combinations ListReconciliationItems needs to project.
func seedReconciliationReservationRow(t *testing.T, db *DB, id, accountID, state string, attempts int, leaseOwner *string, leaseExpiresAt, dispatchedAt *int64, expiresAt, createdAt int64) {
	t.Helper()
	if _, err := db.Conn().Exec(
		`INSERT INTO quota_reservations
		    (id, account_id, request_id, attempt_id, state, dispatched_at, expires_at, created_at, reconcile_attempts, lease_owner, lease_expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, accountID, id+"-req", id+"-attempt", state, dispatchedAt, expiresAt, createdAt, attempts, leaseOwner, leaseExpiresAt,
	); err != nil {
		t.Fatalf("seed reconciliation reservation %s: %v", id, err)
	}
}

func seedReconciliationAllocationRow(t *testing.T, db *DB, reservationID, windowID, unit string, estimatedCost float64, actualCost *float64, actualConfidence *string, state string) {
	t.Helper()
	if _, err := db.Conn().Exec(
		`INSERT INTO quota_reservation_allocations
		    (reservation_id, window_id, unit, estimated_cost, estimate_source, actual_cost, actual_confidence, state)
		 VALUES (?, ?, ?, ?, 'from_request', ?, ?, ?)`,
		reservationID, windowID, unit, estimatedCost, actualCost, actualConfidence, state,
	); err != nil {
		t.Fatalf("seed reconciliation allocation (%s,%s): %v", reservationID, windowID, err)
	}
}

func newDiagnosticsFixture(t *testing.T, clockUnix int64) (*DB, *ReconciliationRepo, string, string) {
	t.Helper()
	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-diag")
	insertAccount(t, db, "acct-diag", "prov-diag")
	seedWindowFull(t, db, "win-diag", "acct-diag", "provider_evidence", "requests", "rolling_5h", "5h", nil, nil, 0, 1)

	lifecycle := NewQuotaLifecycleRepo(db, fixedQuotaClock(clockUnix), nil)
	repo := NewReconciliationRepo(db, fixedQuotaClock(clockUnix), quota.DefaultReconciliationPolicy(), lifecycle, nil)
	return db, repo, "acct-diag", "win-diag"
}

func strPtr(s string) *string { return &s }
func i64Ptr(v int64) *int64   { return &v }

// TestListReconciliationItems_OnlyPendingAndUnknown proves reserved,
// settled, and released reservations are absent from the projection,
// while reconciliation_pending and unknown_consumption are both present.
func TestListReconciliationItems_OnlyPendingAndUnknown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db, repo, accountID, windowID := newDiagnosticsFixture(t, 1000)

	seedReconciliationReservationRow(t, db, "res-reserved", accountID, "reserved", 0, nil, nil, nil, 2000, 900)
	seedReconciliationAllocationRow(t, db, "res-reserved", windowID, "requests", 1, nil, nil, "reserved")

	seedReconciliationReservationRow(t, db, "res-settled", accountID, "settled", 0, nil, nil, i64Ptr(950), 2000, 900)
	seedReconciliationAllocationRow(t, db, "res-settled", windowID, "requests", 1, floatPtr(1), strPtr("high"), "settled")

	seedReconciliationReservationRow(t, db, "res-released", accountID, "released", 0, nil, nil, nil, 2000, 900)
	seedReconciliationAllocationRow(t, db, "res-released", windowID, "requests", 1, nil, nil, "released")

	seedReconciliationReservationRow(t, db, "res-pending", accountID, "reconciliation_pending", 1, nil, nil, i64Ptr(950), 1900, 900)
	seedReconciliationAllocationRow(t, db, "res-pending", windowID, "requests", 1, nil, nil, "reserved")

	seedReconciliationReservationRow(t, db, "res-unknown", accountID, "unknown_consumption", 5, nil, nil, i64Ptr(950), 1800, 900)
	seedReconciliationAllocationRow(t, db, "res-unknown", windowID, "requests", 1, nil, nil, "unknown_consumption")

	items, _, err := repo.ListReconciliationItems(ctx, 50, "")
	if err != nil {
		t.Fatalf("ListReconciliationItems: %v", err)
	}

	got := make(map[string]bool, len(items))
	for _, it := range items {
		got[it.ReservationID] = true
	}
	for _, wantAbsent := range []string{"res-reserved", "res-settled", "res-released"} {
		if got[wantAbsent] {
			t.Fatalf("reservation %q present in reconciliation diagnostics, want absent", wantAbsent)
		}
	}
	for _, wantPresent := range []string{"res-pending", "res-unknown"} {
		if !got[wantPresent] {
			t.Fatalf("reservation %q absent from reconciliation diagnostics, want present", wantPresent)
		}
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}
}

func floatPtr(v float64) *float64 { return &v }

// TestListReconciliationItems_OrderedAndPaged seeds reservations in the
// REVERSE of the canonical (expires_at, id) order and asserts the
// canonical order explicitly, with the cursor round-tripping without
// repeating or skipping an item — SQLite's incidental row order has
// masked a missing ORDER BY twice in this project.
func TestListReconciliationItems_OrderedAndPaged(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db, repo, accountID, windowID := newDiagnosticsFixture(t, 1000)

	ids := []string{"res-c", "res-b", "res-a"} // seeded reverse of expected order
	expiresAts := map[string]int64{"res-a": 1700, "res-b": 1800, "res-c": 1900}
	for _, id := range ids {
		seedReconciliationReservationRow(t, db, id, accountID, "reconciliation_pending", 0, nil, nil, i64Ptr(950), expiresAts[id], 900)
		seedReconciliationAllocationRow(t, db, id, windowID, "requests", 1, nil, nil, "reserved")
	}

	var all []string
	cursor := ""
	for i := 0; i < 5; i++ { // bounded loop guard against an infinite-paging bug
		page, next, err := repo.ListReconciliationItems(ctx, 1, cursor)
		if err != nil {
			t.Fatalf("ListReconciliationItems page %d: %v", i, err)
		}
		for _, it := range page {
			all = append(all, it.ReservationID)
		}
		if next == "" {
			break
		}
		cursor = next
	}

	want := []string{"res-a", "res-b", "res-c"}
	if len(all) != len(want) {
		t.Fatalf("paged result = %v, want %v", all, want)
	}
	for i := range want {
		if all[i] != want[i] {
			t.Fatalf("paged result = %v, want %v (canonical expires_at order)", all, want)
		}
	}
}

// TestListReconciliationItems_CarriesCostsAndConfidence proves a settled
// allocation's actual_confidence surfaces verbatim (e.g. "low"), and an
// unsettled allocation's actual_confidence surfaces as nil — NEVER
// defaulted to "high".
func TestListReconciliationItems_CarriesCostsAndConfidence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db, repo, accountID, windowID := newDiagnosticsFixture(t, 1000)

	seedReconciliationReservationRow(t, db, "res-low-conf", accountID, "reconciliation_pending", 1, nil, nil, i64Ptr(950), 1900, 900)
	seedReconciliationAllocationRow(t, db, "res-low-conf", windowID, "requests", 5, floatPtr(5), strPtr("low"), "reserved")

	seedReconciliationReservationRow(t, db, "res-unsettled", accountID, "reconciliation_pending", 0, nil, nil, i64Ptr(950), 1850, 900)
	seedReconciliationAllocationRow(t, db, "res-unsettled", windowID, "requests", 3, nil, nil, "reserved")

	items, _, err := repo.ListReconciliationItems(ctx, 50, "")
	if err != nil {
		t.Fatalf("ListReconciliationItems: %v", err)
	}

	byID := make(map[string]ReconciliationItem, len(items))
	for _, it := range items {
		byID[it.ReservationID] = it
	}

	lowConf := byID["res-low-conf"]
	if len(lowConf.Allocations) != 1 {
		t.Fatalf("res-low-conf allocations = %+v, want exactly 1", lowConf.Allocations)
	}
	if lowConf.Allocations[0].ActualConfidence == nil || *lowConf.Allocations[0].ActualConfidence != "low" {
		t.Fatalf("res-low-conf ActualConfidence = %v, want \"low\"", lowConf.Allocations[0].ActualConfidence)
	}
	if lowConf.Allocations[0].ActualCost == nil || *lowConf.Allocations[0].ActualCost != 5 {
		t.Fatalf("res-low-conf ActualCost = %v, want 5", lowConf.Allocations[0].ActualCost)
	}

	unsettled := byID["res-unsettled"]
	if len(unsettled.Allocations) != 1 {
		t.Fatalf("res-unsettled allocations = %+v, want exactly 1", unsettled.Allocations)
	}
	if unsettled.Allocations[0].ActualConfidence != nil {
		t.Fatalf("res-unsettled ActualConfidence = %v, want nil (never defaulted to \"high\")", *unsettled.Allocations[0].ActualConfidence)
	}
	if unsettled.Allocations[0].ActualCost != nil {
		t.Fatalf("res-unsettled ActualCost = %v, want nil", *unsettled.Allocations[0].ActualCost)
	}
}

// TestListReconciliationItems_ReportsRebaselineAndLease proves both
// flags reflect reality: a lease that has EXPIRED reports Leased ==
// false, an actively-held lease reports true, and RebaselineFlagged
// mirrors quota_rebaseline_flags exactly.
func TestListReconciliationItems_ReportsRebaselineAndLease(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	const clockUnix = 2000
	db, repo, accountID, windowID := newDiagnosticsFixture(t, clockUnix)

	// Actively leased (expires in the future) + rebaseline-flagged.
	seedReconciliationReservationRow(t, db, "res-leased-flagged", accountID, "reconciliation_pending", 1, strPtr("worker-1"), i64Ptr(clockUnix+300), i64Ptr(1900), 1950, 900)
	seedReconciliationAllocationRow(t, db, "res-leased-flagged", windowID, "requests", 1, nil, nil, "reserved")
	if err := repo.FlagRebaseline(ctx, accountID, "usage_gap"); err != nil {
		t.Fatalf("FlagRebaseline: %v", err)
	}

	items, _, err := repo.ListReconciliationItems(ctx, 50, "")
	if err != nil {
		t.Fatalf("ListReconciliationItems: %v", err)
	}
	byID := make(map[string]ReconciliationItem, len(items))
	for _, it := range items {
		byID[it.ReservationID] = it
	}

	leasedFlagged := byID["res-leased-flagged"]
	if !leasedFlagged.Leased {
		t.Fatalf("res-leased-flagged Leased = false, want true (lease not yet expired)")
	}
	if !leasedFlagged.RebaselineFlagged {
		t.Fatalf("res-leased-flagged RebaselineFlagged = false, want true")
	}

	// A second account, second reservation with an EXPIRED lease and no
	// rebaseline flag — proves both fields are per-item/per-account, not
	// globally true once the fixture above sets them.
	insertProvider(t, db, "prov-diag-2")
	insertAccount(t, db, "acct-diag-2", "prov-diag-2")
	seedWindowFull(t, db, "win-diag-2", "acct-diag-2", "provider_evidence", "requests", "rolling_5h", "5h", nil, nil, 0, 1)
	seedReconciliationReservationRow(t, db, "res-expired-unflagged", "acct-diag-2", "reconciliation_pending", 1, strPtr("worker-1"), i64Ptr(clockUnix-300), i64Ptr(1900), 1950, 900)
	seedReconciliationAllocationRow(t, db, "res-expired-unflagged", "win-diag-2", "requests", 1, nil, nil, "reserved")

	items2, _, err := repo.ListReconciliationItems(ctx, 50, "")
	if err != nil {
		t.Fatalf("ListReconciliationItems (2): %v", err)
	}
	byID2 := make(map[string]ReconciliationItem, len(items2))
	for _, it := range items2 {
		byID2[it.ReservationID] = it
	}
	expiredUnflagged := byID2["res-expired-unflagged"]
	if expiredUnflagged.Leased {
		t.Fatalf("res-expired-unflagged Leased = true, want false (lease_expires_at %d < now %d)", clockUnix-300, clockUnix)
	}
	if expiredUnflagged.RebaselineFlagged {
		t.Fatalf("res-expired-unflagged RebaselineFlagged = true, want false")
	}
}
