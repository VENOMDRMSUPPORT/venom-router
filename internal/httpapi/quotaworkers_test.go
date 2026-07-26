package httpapi

// quotaworkers_test.go exercises the P3b-JOBS-001 reconciliation +
// janitor tick workers (internal/httpapi/quotaworkers.go). Functional
// tests build a QuotaWorkers directly over a fresh migrated DB —
// mirroring newQuotaFixture/newDiagnosticsHTTPFixture's posture. One
// test additionally reads a resulting job through the real ControlMux
// (GET /jobs/{job_id}), proving the workers' job rows are readable
// through the same canonical surface every other job in this package
// uses.

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// quotaWorkersTestTimeout bounds every test in this file per the
// deadlock hazard documented on storage's quota lifecycle/reconciliation
// repos (SetMaxOpenConns(1)): a fast, loud failure instead of a hang.
const quotaWorkersTestTimeout = 10 * time.Second

type quotaWorkersFixture struct {
	workers        *QuotaWorkers
	db             *storage.DB
	jobs           *storage.JobRepo
	reconciliation *storage.ReconciliationRepo
	lifecycle      *storage.QuotaLifecycleRepo
	accountID      string
	windowID       string
}

// newQuotaWorkersFixture seeds a provider + connected account + one
// provider-evidence quota window (reserved=15, enough headroom for
// several 3-unit test reservations) over a fresh migrated DB, and wires
// a QuotaWorkers over it under owner.
func newQuotaWorkersFixture(t *testing.T, clock func() time.Time, owner string) *quotaWorkersFixture {
	t.Helper()
	db := testControlDB(t)

	const providerID = "prov-workers"
	const accountID = "acct-workers"
	const windowID = "win-workers"

	if _, err := db.Conn().Exec(
		`INSERT INTO providers (id, display_name, auth_mode, funding_mode, funding_locked, created_at, updated_at) VALUES (?, ?, 'api_key', 'owner_policy', 0, 0, 0)`,
		providerID, providerID,
	); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if _, err := db.Conn().Exec(
		`INSERT INTO accounts (id, provider_id, external_id, auth_type, connection_state, health_state, created_at, updated_at)
		 VALUES (?, ?, ?, 'api_key', 'connected', 'healthy', 0, 0)`,
		accountID, providerID, accountID,
	); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := db.Conn().Exec(
		`INSERT INTO quota_windows (id, account_id, source, unit, window_type, window_key, reserved, version, confidence, freshness_state, observed_at, created_at, updated_at)
		 VALUES (?, ?, 'provider_evidence', 'requests', 'rolling_5h', '5h', 15, 1, 0.9, 'fresh', 0, 0, 0)`,
		windowID, accountID,
	); err != nil {
		t.Fatalf("seed window: %v", err)
	}

	jobRepo := storage.NewJobRepo(db)
	lifecycle := storage.NewQuotaLifecycleRepo(db, clock, nil)
	reconciliation := storage.NewReconciliationRepo(db, clock, quota.DefaultReconciliationPolicy(), lifecycle, nil)
	audit := newAuditEmitter(db, nil)
	workers := NewQuotaWorkers(reconciliation, lifecycle, jobRepo, audit, quota.DefaultReconciliationPolicy(), owner, clock, quotaIDCounter())

	return &quotaWorkersFixture{
		workers:        workers,
		db:             db,
		jobs:           jobRepo,
		reconciliation: reconciliation,
		lifecycle:      lifecycle,
		accountID:      accountID,
		windowID:       windowID,
	}
}

// seedPending inserts a reconciliation_pending reservation (already
// dispatched, past its processing deadline — the only legal way a real
// reservation reaches this state) plus its single allocation against the
// fixture's window.
func (f *quotaWorkersFixture) seedPending(t *testing.T, id string, expiresAt, createdAt int64, estimatedCost float64) {
	t.Helper()
	if _, err := f.db.Conn().Exec(
		`INSERT INTO quota_reservations (id, account_id, request_id, attempt_id, state, dispatched_at, expires_at, created_at)
		 VALUES (?, ?, ?, ?, 'reconciliation_pending', ?, ?, ?)`,
		id, f.accountID, id+"-req", id+"-attempt", createdAt-50, expiresAt, createdAt,
	); err != nil {
		t.Fatalf("seed pending reservation %s: %v", id, err)
	}
	if _, err := f.db.Conn().Exec(
		`INSERT INTO quota_reservation_allocations (reservation_id, window_id, unit, estimated_cost, estimate_source, actual_cost, state)
		 VALUES (?, ?, 'requests', ?, 'from_request', NULL, 'reserved')`,
		id, f.windowID, estimatedCost,
	); err != nil {
		t.Fatalf("seed pending allocation %s: %v", id, err)
	}
}

func (f *quotaWorkersFixture) reservationState(t *testing.T, id string) string {
	t.Helper()
	var state string
	if err := f.db.Conn().QueryRow(`SELECT state FROM quota_reservations WHERE id = ?`, id).Scan(&state); err != nil {
		t.Fatalf("read reservation %s state: %v", id, err)
	}
	return state
}

func (f *quotaWorkersFixture) onlyJob(t *testing.T) (kind, status, resultRef string) {
	t.Helper()
	var n int
	if err := f.db.Conn().QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&n); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if n != 1 {
		t.Fatalf("jobs row count = %d, want exactly 1", n)
	}
	if err := f.db.Conn().QueryRow(`SELECT kind, status, COALESCE(result_ref, '') FROM jobs`).Scan(&kind, &status, &resultRef); err != nil {
		t.Fatalf("read the only job row: %v", err)
	}
	return
}

// --- ReconcileTick ---

func TestReconcileTick_ProcessesABatchAndRecordsAJob(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), quotaWorkersTestTimeout)
	defer cancel()

	clock := func() time.Time { return time.Unix(2000, 0) }
	f := newQuotaWorkersFixture(t, clock, "worker-batch")
	ids := []string{"res-batch-1", "res-batch-2", "res-batch-3"}
	for _, id := range ids {
		f.seedPending(t, id, 1000, 900, 3)
	}

	result, err := f.workers.ReconcileTick(ctx)
	if err != nil {
		t.Fatalf("ReconcileTick: %v", err)
	}
	if result.Claimed != 3 || result.Settled != 3 || result.UnknownConsumption != 0 || result.LeaseLost != 0 {
		t.Fatalf("result = %+v, want {Claimed:3 Settled:3 UnknownConsumption:0 LeaseLost:0}", result)
	}
	for _, id := range ids {
		if got := f.reservationState(t, id); got != "settled" {
			t.Fatalf("%s state = %q, want settled", id, got)
		}
	}

	kind, status, resultRef := f.onlyJob(t)
	if kind != string(storage.JobKindReconciliation) {
		t.Fatalf("job kind = %q, want reconciliation", kind)
	}
	if status != string(storage.JobCompleted) {
		t.Fatalf("job status = %q, want completed", status)
	}
	const wantRef = "claimed=3,settled=3,unknown_consumption=0,lease_lost=0"
	if resultRef != wantRef {
		t.Fatalf("result_ref = %q, want the exact counts-only string %q", resultRef, wantRef)
	}
	// No-content assertion (09 §3.12): result_ref carries counts ONLY —
	// never a reservation id or any other content.
	for _, id := range ids {
		if strings.Contains(resultRef, id) {
			t.Fatalf("result_ref leaked a reservation id: %s", resultRef)
		}
	}
}

func TestReconcileTick_EmptyQueueSucceedsWithZeroCounts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), quotaWorkersTestTimeout)
	defer cancel()

	clock := func() time.Time { return time.Unix(2000, 0) }
	f := newQuotaWorkersFixture(t, clock, "worker-empty")

	result, err := f.workers.ReconcileTick(ctx)
	if err != nil {
		t.Fatalf("ReconcileTick with nothing claimable returned an error: %v", err)
	}
	if (result != ReconcileTickResult{}) {
		t.Fatalf("result = %+v, want the zero value", result)
	}

	_, status, resultRef := f.onlyJob(t)
	if status != string(storage.JobCompleted) {
		t.Fatalf("job status = %q, want completed (never stuck running, never an error)", status)
	}
	const wantRef = "claimed=0,settled=0,unknown_consumption=0,lease_lost=0"
	if resultRef != wantRef {
		t.Fatalf("result_ref = %q, want %q", resultRef, wantRef)
	}
}

// TestReconcileTick_SkipsAnItemWhoseLeaseWasStolen uses the test-only
// testAfterClaimHook seam to deterministically steal one claimed item's
// lease (simulating another worker, or the janitor, winning that race)
// between claim and reconcile, and asserts the OTHER items still
// resolve and the job still succeeds.
func TestReconcileTick_SkipsAnItemWhoseLeaseWasStolen(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), quotaWorkersTestTimeout)
	defer cancel()

	clock := func() time.Time { return time.Unix(2000, 0) }
	f := newQuotaWorkersFixture(t, clock, "worker-steal")
	ids := []string{"res-steal-a", "res-steal-b", "res-steal-c"}
	for _, id := range ids {
		f.seedPending(t, id, 1000, 900, 3)
	}

	var stolenID string
	f.workers.testAfterClaimHook = func(claimed []storage.PendingReservation) {
		if len(claimed) == 0 {
			return
		}
		stolenID = claimed[0].ReservationID
		if _, err := f.db.Conn().Exec(`UPDATE quota_reservations SET lease_owner = 'thief' WHERE id = ?`, stolenID); err != nil {
			t.Fatalf("steal lease for %s: %v", stolenID, err)
		}
	}

	result, err := f.workers.ReconcileTick(ctx)
	if err != nil {
		t.Fatalf("ReconcileTick: %v", err)
	}
	if result.Claimed != 3 {
		t.Fatalf("Claimed = %d, want 3", result.Claimed)
	}
	if result.LeaseLost != 1 {
		t.Fatalf("LeaseLost = %d, want 1", result.LeaseLost)
	}
	if result.Settled != 2 {
		t.Fatalf("Settled = %d, want 2 (the OTHER items still resolve despite the stolen one)", result.Settled)
	}
	if stolenID == "" {
		t.Fatalf("test hook never ran — no items were claimed")
	}
	if got := f.reservationState(t, stolenID); got != "reconciliation_pending" {
		t.Fatalf("stolen reservation %s state = %q, want unchanged reconciliation_pending", stolenID, got)
	}

	_, status, _ := f.onlyJob(t)
	if status != string(storage.JobCompleted) {
		t.Fatalf("job status = %q, want completed (one lost race must not fail the whole tick)", status)
	}
}

// TestReconcileTick_CrashRecovery simulates a tick that claims items and
// then crashes before resolving anything (a bare ClaimPending call,
// exactly what a real tick's first step does, with nothing further) —
// the claimed items are left re-claimable once their lease expires, and
// a SECOND real ReconcileTick, using the SAME stable owner, picks them
// up and actually resolves them.
func TestReconcileTick_CrashRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), quotaWorkersTestTimeout)
	defer cancel()

	clockUnix := int64(2000)
	clock := func() time.Time { return time.Unix(clockUnix, 0) }
	f := newQuotaWorkersFixture(t, clock, "worker-crash")
	ids := []string{"res-crash-1", "res-crash-2"}
	for _, id := range ids {
		f.seedPending(t, id, 1000, 900, 3)
	}

	claimed, err := f.reconciliation.ClaimPending(ctx, "worker-crash", quota.DefaultLeaseTTL)
	if err != nil {
		t.Fatalf("simulated crashed tick's ClaimPending: %v", err)
	}
	if len(claimed) != 2 {
		t.Fatalf("len(claimed) = %d, want 2 (the crashed tick's own claim)", len(claimed))
	}
	// "crash": nothing further is done with claimed — the lease is left
	// to expire on its own.

	clockUnix += int64(quota.DefaultLeaseTTL.Seconds()) + 60

	result, err := f.workers.ReconcileTick(ctx)
	if err != nil {
		t.Fatalf("recovery ReconcileTick: %v", err)
	}
	if result.Claimed != 2 {
		t.Fatalf("Claimed = %d, want 2 (re-picked once the crashed lease expired)", result.Claimed)
	}
	if result.Settled != 2 {
		t.Fatalf("Settled = %d, want 2 (the recovery tick actually resolves them)", result.Settled)
	}
	for _, id := range ids {
		if got := f.reservationState(t, id); got != "settled" {
			t.Fatalf("%s state = %q, want settled after crash recovery", id, got)
		}
	}
}

// --- JanitorTick ---

func TestJanitorTick_RecordsAJobAndReclaims(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), quotaWorkersTestTimeout)
	defer cancel()

	const clockUnix = 2000
	clock := func() time.Time { return time.Unix(clockUnix, 0) }
	f := newQuotaWorkersFixture(t, clock, "worker-janitor")
	f.seedPending(t, "res-janitor-reclaim", 1000, 900, 3)
	// Lease held by a now-dead worker, expired well before clockUnix.
	if _, err := f.db.Conn().Exec(
		`UPDATE quota_reservations SET lease_owner = 'dead-worker', lease_expires_at = ? WHERE id = ?`,
		clockUnix-500, "res-janitor-reclaim",
	); err != nil {
		t.Fatalf("seed expired lease: %v", err)
	}

	result, err := f.workers.JanitorTick(ctx)
	if err != nil {
		t.Fatalf("JanitorTick: %v", err)
	}
	if result.Reclaimed != 1 {
		t.Fatalf("Reclaimed = %d, want 1", result.Reclaimed)
	}
	if result.Released != 0 || result.Pended != 0 || result.UnknownConsumption != 0 {
		t.Fatalf("result = %+v, want only Reclaimed=1", result)
	}

	var leaseOwner sql.NullString
	if err := f.db.Conn().QueryRow(`SELECT lease_owner FROM quota_reservations WHERE id = ?`, "res-janitor-reclaim").Scan(&leaseOwner); err != nil {
		t.Fatalf("read lease_owner: %v", err)
	}
	if leaseOwner.Valid {
		t.Fatalf("lease_owner = %q, want cleared (reclaimed)", leaseOwner.String)
	}

	kind, status, resultRef := f.onlyJob(t)
	if kind != string(storage.JobKindReconciliation) {
		t.Fatalf("job kind = %q, want reconciliation (no distinct janitor kind exists in the frozen vocabulary)", kind)
	}
	if status != string(storage.JobCompleted) {
		t.Fatalf("job status = %q, want completed", status)
	}
	const wantRef = "released=0,pended=0,reclaimed=1,unknown_consumption=0"
	if resultRef != wantRef {
		t.Fatalf("result_ref = %q, want %q", resultRef, wantRef)
	}
}

// TestWorkerJobs_UseRegisteredKinds proves storage.ParseJobKind accepts
// the kind these workers write, and that the resulting rows are readable
// through GET /jobs/{job_id} via the REAL composed ControlMux — not just
// this package's own JobRepo.
func TestWorkerJobs_UseRegisteredKinds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), quotaWorkersTestTimeout)
	defer cancel()

	clock := func() time.Time { return time.Unix(2000, 0) }
	f := newQuotaWorkersFixture(t, clock, "worker-kinds")
	f.seedPending(t, "res-kinds-1", 1000, 900, 3)

	reconcileResult, err := f.workers.ReconcileTick(ctx)
	if err != nil {
		t.Fatalf("ReconcileTick: %v", err)
	}
	if reconcileResult.Settled != 1 {
		t.Fatalf("Settled = %d, want 1", reconcileResult.Settled)
	}

	var jobID, kind string
	if err := f.db.Conn().QueryRow(`SELECT id, kind FROM jobs LIMIT 1`).Scan(&jobID, &kind); err != nil {
		t.Fatalf("read job row: %v", err)
	}
	if _, err := storage.ParseJobKind(kind); err != nil {
		t.Fatalf("ParseJobKind(%q): %v, want it accepted", kind, err)
	}

	mux := ControlMux(testAllowedHost, fakeSPA(), f.db, testKeyring(t))
	cookie := setupOwner(t, mux, testSetupPassword)
	req := newAuthRequest(t, http.MethodGet, "/api/control/v1/jobs/"+jobID, nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /jobs/%s through the real ControlMux status = %d, want 200; body = %q", jobID, rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), string(storage.JobCompleted)) {
		t.Fatalf("GET /jobs/%s body = %q, want it to reflect the completed job", jobID, rec.Body.String())
	}
}
