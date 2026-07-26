package storage

import (
	"context"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

// seedPendingReservation seeds a reconciliation_pending reservation
// (dispatched, already past its processing deadline — the only legal way
// a real reservation ever reaches this state, per the janitor's Branch B)
// with one allocation against windowID.
func seedPendingReservation(t *testing.T, db *DB, id, accountID, windowID string, dispatchedAt, expiresAt, createdAt int64, estimatedCost float64) {
	t.Helper()
	seedJanitorReservation(t, db, id, accountID, windowID, "reconciliation_pending", &dispatchedAt, expiresAt, createdAt, estimatedCost)
}

func TestReconcileOne_Settles(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-reconcile-settle")
	insertAccount(t, db, "acct-reconcile-settle", "prov-reconcile-settle")
	seedWindowFull(t, db, "win-reconcile-settle", "acct-reconcile-settle", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 5, 1)
	// now=2000; expires_at=1000 -> 1000s elapsed, well under the default
	// policy's retry-exhaustion boundary (5 * 30s = 150s).
	seedPendingReservation(t, db, "res-reconcile-settle", "acct-reconcile-settle", "win-reconcile-settle", 950, 1900, 900, 5)

	lifecycle := NewQuotaLifecycleRepo(db, fixedQuotaClock(2000), nil)
	repo := NewReconciliationRepo(db, fixedQuotaClock(2000), quota.DefaultReconciliationPolicy(), lifecycle, nil)

	pending := PendingReservation{ReservationID: "res-reconcile-settle", AccountID: "acct-reconcile-settle", ExpiresAt: 1900}
	outcome, err := repo.ReconcileOne(ctx, pending)
	if err != nil {
		t.Fatalf("ReconcileOne: %v", err)
	}
	if outcome.Outcome != quota.ReservationSettled {
		t.Fatalf("outcome.Outcome = %q, want settled", outcome.Outcome)
	}
	state, settledAt := readReservationState(t, db, "res-reconcile-settle")
	if state != "settled" || !settledAt.Valid {
		t.Fatalf("reservation (state=%s settledAt.Valid=%v), want (settled, true)", state, settledAt.Valid)
	}
	_, _, reserved, _ := readWindowFull(t, db, "win-reconcile-settle")
	if reserved != 0 {
		t.Fatalf("window reserved = %v, want 0 (settled at its own estimate)", reserved)
	}
}

func TestReconcileOne_Idempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-reconcile-idem")
	insertAccount(t, db, "acct-reconcile-idem", "prov-reconcile-idem")
	seedWindowFull(t, db, "win-reconcile-idem", "acct-reconcile-idem", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 5, 1)
	seedPendingReservation(t, db, "res-reconcile-idem", "acct-reconcile-idem", "win-reconcile-idem", 950, 1900, 900, 5)

	lifecycle := NewQuotaLifecycleRepo(db, fixedQuotaClock(2000), nil)
	repo := NewReconciliationRepo(db, fixedQuotaClock(2000), quota.DefaultReconciliationPolicy(), lifecycle, nil)
	pending := PendingReservation{ReservationID: "res-reconcile-idem", AccountID: "acct-reconcile-idem", ExpiresAt: 1900}

	first, err := repo.ReconcileOne(ctx, pending)
	if err != nil {
		t.Fatalf("first ReconcileOne: %v", err)
	}
	before := snapshotWindow(t, db, "win-reconcile-idem")

	second, err := repo.ReconcileOne(ctx, pending)
	if err != nil {
		t.Fatalf("second ReconcileOne: %v, want success (no-op)", err)
	}
	if second.Outcome != first.Outcome {
		t.Fatalf("second outcome = %q, want %q (same as first)", second.Outcome, first.Outcome)
	}
	after := snapshotWindow(t, db, "win-reconcile-idem")
	if !after.equal(before) {
		t.Fatalf("second ReconcileOne changed the window: before=%+v after=%+v", before, after)
	}
}

func TestReconcileOne_TerminalRetryBoundary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-reconcile-terminal")
	insertAccount(t, db, "acct-reconcile-terminal", "prov-reconcile-terminal")
	seedWindowFull(t, db, "win-reconcile-terminal", "acct-reconcile-terminal", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 5, 1)
	// now=100000; expires_at=1000 -> 99000s elapsed, far past the default
	// policy's retry-exhaustion boundary (5 * 30s = 150s).
	seedPendingReservation(t, db, "res-reconcile-terminal", "acct-reconcile-terminal", "win-reconcile-terminal", 950, 1000, 900, 5)

	lifecycle := NewQuotaLifecycleRepo(db, fixedQuotaClock(100000), nil)
	repo := NewReconciliationRepo(db, fixedQuotaClock(100000), quota.DefaultReconciliationPolicy(), lifecycle, nil)
	pending := PendingReservation{ReservationID: "res-reconcile-terminal", AccountID: "acct-reconcile-terminal", ExpiresAt: 1000}

	outcome, err := repo.ReconcileOne(ctx, pending)
	if err != nil {
		t.Fatalf("ReconcileOne: %v", err)
	}
	if outcome.Outcome != quota.ReservationUnknownConsumption {
		t.Fatalf("outcome.Outcome = %q, want unknown_consumption", outcome.Outcome)
	}
	state, settledAt := readReservationState(t, db, "res-reconcile-terminal")
	if state != "unknown_consumption" || !settledAt.Valid {
		t.Fatalf("reservation (state=%s settledAt.Valid=%v), want (unknown_consumption, true)", state, settledAt.Valid)
	}
	_, _, reserved, _ := readWindowFull(t, db, "win-reconcile-terminal")
	if reserved != 5 {
		t.Fatalf("window reserved = %v, want 5 (still debited — usage gap never discarded)", reserved)
	}
}

// TestReconcileOne_TerminalRetryBoundary_EmitsUsageGap pins the other half
// of 02 §3's usage_gap requirement: the WORKER's terminal path must record
// it too, not just the janitor's. Without this, a reservation whose retry
// budget the worker exhausts would reach unknown_consumption with no
// record of the gap anywhere.
func TestReconcileOne_TerminalRetryBoundary_EmitsUsageGap(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-gap-worker")
	insertAccount(t, db, "acct-gap-worker", "prov-gap-worker")
	seedWindowFull(t, db, "win-gap-worker", "acct-gap-worker", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 5, 1)
	seedPendingReservation(t, db, "res-gap-worker", "acct-gap-worker", "win-gap-worker", 950, 1000, 900, 5)

	audit := NewAuditEventRepo(db)
	lifecycle := NewQuotaLifecycleRepo(db, fixedQuotaClock(100000), audit)
	repo := NewReconciliationRepo(db, fixedQuotaClock(100000), quota.DefaultReconciliationPolicy(), lifecycle, audit)

	outcome, err := repo.ReconcileOne(ctx, PendingReservation{
		ReservationID: "res-gap-worker", AccountID: "acct-gap-worker", ExpiresAt: 1000,
	})
	if err != nil {
		t.Fatalf("ReconcileOne: %v", err)
	}
	if outcome.Outcome != quota.ReservationUnknownConsumption {
		t.Fatalf("outcome = %q, want unknown_consumption", outcome.Outcome)
	}

	var n int
	if err := db.Conn().QueryRow(
		`SELECT COUNT(*) FROM audit_events WHERE action = ? AND entity_id = ?`,
		AuditActionUsageGap, "res-gap-worker",
	).Scan(&n); err != nil {
		t.Fatalf("count usage_gap rows: %v", err)
	}
	if n != 1 {
		t.Fatalf("usage_gap rows = %d, want exactly 1", n)
	}
}

func TestPendingReservations_ReturnsOnlyPendingPastDeadlineOrderedAndBatched(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-pending-list")
	insertAccount(t, db, "acct-pending-list", "prov-pending-list")
	seedWindowFull(t, db, "win-pending-list", "acct-pending-list", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 5, 1)

	seedPendingReservation(t, db, "res-pending-2", "acct-pending-list", "win-pending-list", 950, 1200, 900, 1)
	seedPendingReservation(t, db, "res-pending-1", "acct-pending-list", "win-pending-list", 950, 1100, 900, 1)
	// A reserved (not yet pending) row must never be returned.
	seedJanitorReservation(t, db, "res-not-pending", "acct-pending-list", "win-pending-list", "reserved", nil, 1100, 900, 1)

	policy := quota.DefaultReconciliationPolicy()
	policy.BatchSize = 1
	repo := NewReconciliationRepo(db, fixedQuotaClock(2000), policy, NewQuotaLifecycleRepo(db, fixedQuotaClock(2000), nil), nil)

	pending, err := repo.PendingReservations(ctx)
	if err != nil {
		t.Fatalf("PendingReservations: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("len(pending) = %d, want 1 (BatchSize=1)", len(pending))
	}
	if pending[0].ReservationID != "res-pending-1" {
		t.Fatalf("pending[0].ReservationID = %q, want res-pending-1 (earliest expires_at first)", pending[0].ReservationID)
	}
}

func TestPendingReservation_LoadAllocations(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-pending-allocs")
	insertAccount(t, db, "acct-pending-allocs", "prov-pending-allocs")
	seedWindowFull(t, db, "win-pending-allocs", "acct-pending-allocs", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 7, 1)
	seedPendingReservation(t, db, "res-pending-allocs", "acct-pending-allocs", "win-pending-allocs", 950, 1900, 900, 7)

	pending := PendingReservation{ReservationID: "res-pending-allocs"}
	allocs, err := pending.LoadAllocations(ctx, db)
	if err != nil {
		t.Fatalf("LoadAllocations: %v", err)
	}
	if len(allocs) != 1 || allocs[0].WindowID != "win-pending-allocs" || allocs[0].Estimated != 7 {
		t.Fatalf("allocs = %+v, want [{win-pending-allocs 7}]", allocs)
	}
}

func float64PtrRecon(v float64) *float64 { return &v }
func intPtrRecon(v int) *int             { return &v }

// TestSync_Upsert proves SyncQuotaWindows creates a provider_evidence
// window on first sync, then UPDATES the SAME row (never a duplicate) on
// a second sync with different values.
func TestSync_Upsert(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-sync-upsert")
	insertAccount(t, db, "acct-sync-upsert", "prov-sync-upsert")

	repo := NewReconciliationRepo(db, fixedQuotaClock(1000), quota.DefaultReconciliationPolicy(), NewQuotaLifecycleRepo(db, fixedQuotaClock(1000), nil), nil)

	first := []providers.QuotaWindow{
		{Unit: "requests", WindowType: "rpm", WindowKey: "rpm", Used: float64PtrRecon(10), Remaining: float64PtrRecon(90), Total: float64PtrRecon(100), Confidence: 0.9},
	}
	if err := repo.SyncQuotaWindows(ctx, "acct-sync-upsert", first, nil, nil); err != nil {
		t.Fatalf("first SyncQuotaWindows: %v", err)
	}

	var countAfterFirst int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM quota_windows WHERE account_id = ? AND source = 'provider_evidence'`, "acct-sync-upsert").Scan(&countAfterFirst); err != nil {
		t.Fatalf("count after first sync: %v", err)
	}
	if countAfterFirst != 1 {
		t.Fatalf("rows after first sync = %d, want 1", countAfterFirst)
	}

	second := []providers.QuotaWindow{
		{Unit: "requests", WindowType: "rpm", WindowKey: "rpm", Used: float64PtrRecon(20), Remaining: float64PtrRecon(80), Total: float64PtrRecon(100), Confidence: 0.95},
	}
	if err := repo.SyncQuotaWindows(ctx, "acct-sync-upsert", second, nil, nil); err != nil {
		t.Fatalf("second SyncQuotaWindows: %v", err)
	}

	var countAfterSecond int
	var used, remaining, confidence float64
	var version int64
	if err := db.Conn().QueryRow(
		`SELECT used, remaining, confidence, version FROM quota_windows WHERE account_id = ? AND source = 'provider_evidence'`,
		"acct-sync-upsert",
	).Scan(&used, &remaining, &confidence, &version); err != nil {
		t.Fatalf("read window after second sync: %v", err)
	}
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM quota_windows WHERE account_id = ? AND source = 'provider_evidence'`, "acct-sync-upsert").Scan(&countAfterSecond); err != nil {
		t.Fatalf("count after second sync: %v", err)
	}
	if countAfterSecond != 1 {
		t.Fatalf("rows after second sync = %d, want still 1 (UPSERT, never a duplicate)", countAfterSecond)
	}
	if used != 20 || remaining != 80 || confidence != 0.95 {
		t.Fatalf("window after second sync = (used=%v remaining=%v confidence=%v), want (20,80,0.95) — the SECOND call's values", used, remaining, confidence)
	}
	if version != 2 {
		t.Fatalf("version = %d, want 2 (incremented on update)", version)
	}
}

// TestSync_Staleness proves a provider_evidence window that existed
// before a sync but is NOT present in the new fetch result is marked
// stale — never silently left as fresh, and never deleted.
func TestSync_Staleness(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-sync-stale")
	insertAccount(t, db, "acct-sync-stale", "prov-sync-stale")

	repo := NewReconciliationRepo(db, fixedQuotaClock(1000), quota.DefaultReconciliationPolicy(), NewQuotaLifecycleRepo(db, fixedQuotaClock(1000), nil), nil)

	initial := []providers.QuotaWindow{
		{Unit: "requests", WindowType: "rpm", WindowKey: "rpm", Remaining: float64PtrRecon(90), Confidence: 0.9},
		{Unit: "tokens", WindowType: "tpm", WindowKey: "tpm", Remaining: float64PtrRecon(900), Confidence: 0.9},
	}
	if err := repo.SyncQuotaWindows(ctx, "acct-sync-stale", initial, nil, nil); err != nil {
		t.Fatalf("initial SyncQuotaWindows: %v", err)
	}

	// Second fetch reports only the rpm window; tpm is now missing.
	onlyRPM := []providers.QuotaWindow{
		{Unit: "requests", WindowType: "rpm", WindowKey: "rpm", Remaining: float64PtrRecon(85), Confidence: 0.9},
	}
	if err := repo.SyncQuotaWindows(ctx, "acct-sync-stale", onlyRPM, nil, nil); err != nil {
		t.Fatalf("second SyncQuotaWindows: %v", err)
	}

	var rpmFreshness, tpmFreshness string
	if err := db.Conn().QueryRow(`SELECT freshness_state FROM quota_windows WHERE account_id = ? AND window_type = 'rpm'`, "acct-sync-stale").Scan(&rpmFreshness); err != nil {
		t.Fatalf("read rpm freshness: %v", err)
	}
	if err := db.Conn().QueryRow(`SELECT freshness_state FROM quota_windows WHERE account_id = ? AND window_type = 'tpm'`, "acct-sync-stale").Scan(&tpmFreshness); err != nil {
		t.Fatalf("read tpm freshness: %v", err)
	}
	if rpmFreshness != "fresh" {
		t.Fatalf("rpm freshness = %q, want fresh (present in the latest fetch)", rpmFreshness)
	}
	if tpmFreshness != "stale" {
		t.Fatalf("tpm freshness = %q, want stale (missing from the latest fetch)", tpmFreshness)
	}

	var tpmCount int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM quota_windows WHERE account_id = ? AND window_type = 'tpm'`, "acct-sync-stale").Scan(&tpmCount); err != nil {
		t.Fatalf("count tpm rows: %v", err)
	}
	if tpmCount != 1 {
		t.Fatalf("tpm rows = %d, want 1 (marked stale, never deleted)", tpmCount)
	}
}

// TestSync_RateLimitCallback proves a caller-supplied RateLimitSignal
// invokes onRateLimit exactly once with the signal's own fields, and
// that a sync with no signal never calls it.
func TestSync_RateLimitCallback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-sync-429")
	insertAccount(t, db, "acct-sync-429", "prov-sync-429")

	repo := NewReconciliationRepo(db, fixedQuotaClock(1000), quota.DefaultReconciliationPolicy(), NewQuotaLifecycleRepo(db, fixedQuotaClock(1000), nil), nil)

	var calls int
	var gotScope string
	var gotAccountID *string
	var gotRetryAfter *int
	onRateLimit := func(scope string, accountID, offeringOperationID, providerID *string, retryAfter *int) {
		calls++
		gotScope = scope
		gotAccountID = accountID
		gotRetryAfter = retryAfter
	}

	accountID := "acct-sync-429"
	signal := &RateLimitSignal{Scope: "account", AccountID: &accountID, RetryAfter: intPtrRecon(30)}
	if err := repo.SyncQuotaWindows(ctx, "acct-sync-429", nil, signal, onRateLimit); err != nil {
		t.Fatalf("SyncQuotaWindows: %v", err)
	}
	if calls != 1 {
		t.Fatalf("onRateLimit calls = %d, want 1", calls)
	}
	if gotScope != "account" || gotAccountID == nil || *gotAccountID != accountID || gotRetryAfter == nil || *gotRetryAfter != 30 {
		t.Fatalf("onRateLimit args = (scope=%q accountID=%v retryAfter=%v), want (account, %q, 30)", gotScope, gotAccountID, gotRetryAfter, accountID)
	}

	// No signal -> never called.
	calls = 0
	if err := repo.SyncQuotaWindows(ctx, "acct-sync-429", nil, nil, onRateLimit); err != nil {
		t.Fatalf("SyncQuotaWindows (no signal): %v", err)
	}
	if calls != 0 {
		t.Fatalf("onRateLimit calls = %d with no signal, want 0", calls)
	}
}
