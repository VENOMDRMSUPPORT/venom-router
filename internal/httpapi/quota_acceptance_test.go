package httpapi

// quota_acceptance_test.go is P3b-TEST-001: THE P3b phase gate,
// mechanized. Every unit it certifies (QUOTA-001..008, CAPI-001, CAPI-002,
// JOBS-001, UI-001) already has its own unit tests (internal/quota,
// internal/storage's quota_reservations_test.go / quota_lifecycle_test.go
// / quota_janitor_test.go / quota_reconciliation_test.go,
// internal/httpapi's quotaworkers_test.go / accounts_test.go); this suite
// does NOT re-test any of their internals. It proves docs/06-roadmap.md's
// "Phase 3b — Quota & consumption accounting" Gate paragraph END-TO-END,
// composing ONLY already-shipped, already-reviewed production seams — this
// file adds ZERO production code (mirrors discovery_acceptance_test.go's
// P3a-TEST-001 precedent exactly: one new test file, no production diff).
//
// One shared fixture (quotaGateFixture) composes the real reservation
// stack — QuotaWindowRepo, QuotaReservationRepo, QuotaLifecycleRepo
// (WithPolicy), ReconciliationRepo, JobRepo, AuditEventRepo, and
// QuotaWorkers — over ONE temp SQLite DB with ONE injected, explicitly
// advanceable clock, built fresh per test (never shared ACROSS tests) so
// each TestP3bGate_* is independently deterministic. Every account is
// seeded via the REAL provider/account insert + EnsureLocalSafetyWindows
// path — never a hand-written row that bypasses it. A provider-evidence
// window has no analogous "ensure" seam (a real one is only ever created
// via ReconciliationRepo.SyncQuotaWindows), so where a provider window's
// exact starting numbers matter, this file seeds it directly — the same
// precedent internal/storage/quota_reservations_test.go's own
// seedWindowFull and internal/httpapi/quotaworkers_test.go's own
// newQuotaWorkersFixture already establish.
//
// Every test asserts a POSITIVE CONTROL — that the good path genuinely
// happened — before it asserts the criterion's negative half, and every
// "nothing changed" claim is read back from the database with a raw
// SELECT, never inferred from a method's return value alone.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// gateTestTimeout bounds every test in this file per the SetMaxOpenConns(1)
// deadlock hazard documented throughout internal/storage's quota
// repositories: a fast, loud failure instead of a hang.
const gateTestTimeout = 10 * time.Second

// quotaGateFixture composes the real P3b reservation stack over one temp
// SQLite DB, sharing one injected, explicitly-advanceable clock — exactly
// as ControlMux and quotaworkers_test.go's newQuotaWorkersFixture wire the
// same repos, so this fixture is not a parallel stack, only this file's
// own construction of the same pieces.
type quotaGateFixture struct {
	t         *testing.T
	db        *storage.DB
	clockUnix int64
	policy    quota.ReconciliationPolicy

	windows        *storage.QuotaWindowRepo
	reservations   *storage.QuotaReservationRepo
	auditRepo      *storage.AuditEventRepo
	lifecycle      *storage.QuotaLifecycleRepo
	reconciliation *storage.ReconciliationRepo
	jobs           *storage.JobRepo
	workers        *QuotaWorkers
}

// newQuotaGateFixture builds a fresh fixture over a fresh migrated temp
// DB, under the given reconciliation policy (both the lifecycle janitor's
// and the reconciliation worker's — the SAME policy value, since
// quota.RetryExhausted requires both callers to agree).
func newQuotaGateFixture(t *testing.T, policy quota.ReconciliationPolicy) *quotaGateFixture {
	t.Helper()
	db := testControlDB(t)
	f := &quotaGateFixture{t: t, db: db, clockUnix: 1_700_000_000, policy: policy}

	f.windows = storage.NewQuotaWindowRepo(db, nil, f.clock)
	f.reservations = storage.NewQuotaReservationRepo(db, f.clock)
	f.auditRepo = storage.NewAuditEventRepo(db)
	f.lifecycle = storage.NewQuotaLifecycleRepo(db, f.clock, f.auditRepo).WithPolicy(policy)
	f.reconciliation = storage.NewReconciliationRepo(db, f.clock, policy, f.lifecycle, f.auditRepo)
	f.jobs = storage.NewJobRepo(db)
	f.workers = NewQuotaWorkers(f.reconciliation, f.lifecycle, f.jobs, newAuditEmitter(db, nil), policy, "gate-worker", f.clock, quotaIDCounter())
	return f
}

func newQuotaGateFixtureDefault(t *testing.T) *quotaGateFixture {
	return newQuotaGateFixture(t, quota.DefaultReconciliationPolicy())
}

func (f *quotaGateFixture) clock() time.Time        { return time.Unix(f.clockUnix, 0).UTC() }
func (f *quotaGateFixture) advance(d time.Duration) { f.clockUnix += int64(d.Seconds()) }

// seedProvider inserts one providers row. No context: a single
// pool-scoped statement issued before any transaction is open, exactly
// like every other seed helper's provider insert in this package.
func (f *quotaGateFixture) seedProvider(id string) {
	f.t.Helper()
	if _, err := f.db.Conn().Exec(
		`INSERT INTO providers (id, display_name, auth_mode, funding_mode, funding_locked, created_at, updated_at) VALUES (?, ?, 'api_key', 'owner_policy', 0, 0, 0)`,
		id, id,
	); err != nil {
		f.t.Fatalf("seed provider %s: %v", id, err)
	}
}

// seedAccount inserts one connected account row under providerID, then
// gives it its mandatory local-safety windows through the REAL
// EnsureLocalSafetyWindows path under policy — never a hand-written row.
func (f *quotaGateFixture) seedAccount(ctx context.Context, id, providerID string, policy quota.LocalSafetyPolicy) {
	f.t.Helper()
	if _, err := f.db.Conn().Exec(
		`INSERT INTO accounts (id, provider_id, external_id, auth_type, connection_state, health_state, created_at, updated_at)
		 VALUES (?, ?, ?, 'api_key', 'connected', 'healthy', ?, ?)`,
		id, providerID, id, f.clockUnix, f.clockUnix,
	); err != nil {
		f.t.Fatalf("seed account %s: %v", id, err)
	}
	specs, err := policy.MandatoryWindows()
	if err != nil {
		f.t.Fatalf("MandatoryWindows for %s: %v", id, err)
	}
	if err := f.windows.EnsureLocalSafetyWindows(ctx, id, specs); err != nil {
		f.t.Fatalf("EnsureLocalSafetyWindows for %s: %v", id, err)
	}
}

// seedProviderWindow inserts one provider_evidence quota_windows row
// directly — provider-evidence windows have no "ensure" analogue
// (EnsureLocalSafetyWindows is local_safety-only by construction); a real
// one is only ever created via ReconciliationRepo.SyncQuotaWindows, which
// TestP3bGate_WindowKeyNormalizationIsDeterministic drives directly where
// the realistic path matters. Elsewhere, where this file needs to control
// a provider window's exact starting numbers (including deliberately
// unknown ones), it seeds directly — the same precedent
// quota_reservations_test.go's seedWindowFull and quotaworkers_test.go's
// newQuotaWorkersFixture already establish for this exact table.
func (f *quotaGateFixture) seedProviderWindow(id, accountID, unit, windowType, key string, remaining, limitValue, used *float64, reserved float64, version int64) {
	f.t.Helper()
	if _, err := f.db.Conn().Exec(
		`INSERT INTO quota_windows
		    (id, account_id, source, unit, window_type, window_key, used, remaining, limit_value, reserved, version, confidence, freshness_state, observed_at, created_at, updated_at)
		 VALUES (?, ?, 'provider_evidence', ?, ?, ?, ?, ?, ?, ?, ?, 1, 'fresh', ?, ?, ?)`,
		id, accountID, unit, windowType, key, used, remaining, limitValue, reserved, version, f.clockUnix, f.clockUnix, f.clockUnix,
	); err != nil {
		f.t.Fatalf("seed provider window %s: %v", id, err)
	}
}

func (f *quotaGateFixture) windowIDByType(accountID, windowType string) string {
	f.t.Helper()
	var id string
	if err := f.db.Conn().QueryRow(`SELECT id FROM quota_windows WHERE account_id = ? AND window_type = ?`, accountID, windowType).Scan(&id); err != nil {
		f.t.Fatalf("find window (account=%s type=%s): %v", accountID, windowType, err)
	}
	return id
}

func (f *quotaGateFixture) windowReservedVersion(windowID string) (float64, int64) {
	f.t.Helper()
	var reserved float64
	var version int64
	if err := f.db.Conn().QueryRow(`SELECT reserved, version FROM quota_windows WHERE id = ?`, windowID).Scan(&reserved, &version); err != nil {
		f.t.Fatalf("read window %s: %v", windowID, err)
	}
	return reserved, version
}

func (f *quotaGateFixture) reservationState(id string) string {
	f.t.Helper()
	var s string
	if err := f.db.Conn().QueryRow(`SELECT state FROM quota_reservations WHERE id = ?`, id).Scan(&s); err != nil {
		f.t.Fatalf("read reservation %s: %v", id, err)
	}
	return s
}

func (f *quotaGateFixture) reservationDispatchedAt(id string) sql.NullInt64 {
	f.t.Helper()
	var v sql.NullInt64
	if err := f.db.Conn().QueryRow(`SELECT dispatched_at FROM quota_reservations WHERE id = ?`, id).Scan(&v); err != nil {
		f.t.Fatalf("read dispatched_at for %s: %v", id, err)
	}
	return v
}

func (f *quotaGateFixture) reconcileAttempts(id string) int64 {
	f.t.Helper()
	var n int64
	if err := f.db.Conn().QueryRow(`SELECT reconcile_attempts FROM quota_reservations WHERE id = ?`, id).Scan(&n); err != nil {
		f.t.Fatalf("read reconcile_attempts for %s: %v", id, err)
	}
	return n
}

func (f *quotaGateFixture) allocationStatesFor(reservationID string) map[string]string {
	f.t.Helper()
	rows, err := f.db.Conn().Query(`SELECT window_id, state FROM quota_reservation_allocations WHERE reservation_id = ?`, reservationID)
	if err != nil {
		f.t.Fatalf("query allocations for %s: %v", reservationID, err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var w, s string
		if err := rows.Scan(&w, &s); err != nil {
			f.t.Fatalf("scan allocation for %s: %v", reservationID, err)
		}
		out[w] = s
	}
	if err := rows.Err(); err != nil {
		f.t.Fatalf("iterate allocations for %s: %v", reservationID, err)
	}
	return out
}

func (f *quotaGateFixture) allocationRow(reservationID, windowID string) (state string, actualCost sql.NullFloat64, confidence sql.NullString) {
	f.t.Helper()
	if err := f.db.Conn().QueryRow(
		`SELECT state, actual_cost, actual_confidence FROM quota_reservation_allocations WHERE reservation_id = ? AND window_id = ?`,
		reservationID, windowID,
	).Scan(&state, &actualCost, &confidence); err != nil {
		f.t.Fatalf("read allocation (%s,%s): %v", reservationID, windowID, err)
	}
	return
}

func (f *quotaGateFixture) countRows(table, whereCol, whereVal string) int {
	f.t.Helper()
	var n int
	q := fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE %s = ?`, table, whereCol)
	if err := f.db.Conn().QueryRow(q, whereVal).Scan(&n); err != nil {
		f.t.Fatalf("count %s where %s: %v", table, whereCol, err)
	}
	return n
}

func (f *quotaGateFixture) countAudit(action, entityID string) int {
	f.t.Helper()
	var n int
	var err error
	if entityID == "" {
		err = f.db.Conn().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = ?`, action).Scan(&n)
	} else {
		err = f.db.Conn().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = ? AND entity_id = ?`, action, entityID).Scan(&n)
	}
	if err != nil {
		f.t.Fatalf("count audit %s: %v", action, err)
	}
	return n
}

// localSafetyAllocations is the two-window debit every default-policy
// account's mandatory local-safety windows both apply to: concurrency
// (unit=concurrency) and estimated_consumption (unit=requests, per
// quota.DefaultLocalSafetyPolicy's EstimatedConsumptionUnit).
func localSafetyAllocations() []quota.Allocation {
	return []quota.Allocation{
		{Unit: quota.UnitConcurrency, Cost: 1, Source: quota.EstimateSourceFromRequest},
		{Unit: quota.UnitRequests, Cost: 1, Source: quota.EstimateSourceFromRequest},
	}
}

// assertTableHasNoCanary scans each of cols in table for canary as a
// substring (SQL LIKE with a bound parameter — never string-concatenated,
// so this is not an injection vector), tolerating NULLs.
func assertTableHasNoCanary(t *testing.T, db *storage.DB, table string, cols []string, canary string) {
	t.Helper()
	for _, col := range cols {
		query := fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE %s LIKE '%%' || ? || '%%'`, table, col)
		var n int
		if err := db.Conn().QueryRow(query, canary).Scan(&n); err != nil {
			t.Fatalf("scan %s.%s for canary: %v", table, col, err)
		}
		if n > 0 {
			t.Fatalf("canary leaked into %s.%s (%d matching row(s))", table, col, n)
		}
	}
}

// ============================================================================
// 1. Pre-dispatch deadline expiry releases and audits (Branch A)
// ============================================================================

func TestP3bGate_PreDispatchDeadlineExpiryReleasesAndAudits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), gateTestTimeout)
	defer cancel()

	f := newQuotaGateFixtureDefault(t)
	f.seedProvider("prov-gate-a")
	const accountID = "acct-gate-a"
	f.seedAccount(ctx, accountID, "prov-gate-a", quota.DefaultLocalSafetyPolicy())
	concurrencyID := f.windowIDByType(accountID, "concurrency")
	consumptionID := f.windowIDByType(accountID, "estimated_consumption")

	result, err := f.reservations.Reserve(ctx, storage.ReserveParams{
		AccountID: accountID, RequestID: "req-a", AttemptID: "attempt-1", Allocations: localSafetyAllocations(),
	})
	if err != nil {
		t.Fatalf("Reserve: %v, want success", err)
	}

	// POSITIVE CONTROL: the reservation really is `reserved` with headroom
	// really debited, BEFORE the janitor ever runs.
	if state := f.reservationState(result.ReservationID); state != string(quota.ReservationReserved) {
		t.Fatalf("reservation state = %q, want reserved (positive control)", state)
	}
	if dispatched := f.reservationDispatchedAt(result.ReservationID); dispatched.Valid {
		t.Fatalf("dispatched_at = %v, want NULL (branch A precondition)", dispatched.Int64)
	}
	concBefore, _ := f.windowReservedVersion(concurrencyID)
	consBefore, _ := f.windowReservedVersion(consumptionID)
	if concBefore != 1 || consBefore != 1 {
		t.Fatalf("windows after reserve = (concurrency=%v consumption=%v), want (1,1) — positive control", concBefore, consBefore)
	}

	// Never MarkDispatched. Advance past the processing deadline.
	f.advance(quota.DefaultProcessingDeadline + time.Second)

	sweep, err := f.lifecycle.Janitor(ctx)
	if err != nil {
		t.Fatalf("Janitor: %v", err)
	}
	if sweep.Released != 1 || sweep.Pended != 0 || sweep.Reclaimed != 0 || sweep.UnknownConsumption != 0 {
		t.Fatalf("sweep = %+v, want ONLY Released=1", sweep)
	}

	if state := f.reservationState(result.ReservationID); state != string(quota.ReservationReleased) {
		t.Fatalf("reservation state after janitor = %q, want released", state)
	}
	for windowID, state := range f.allocationStatesFor(result.ReservationID) {
		if state != string(quota.AllocationReleased) {
			t.Fatalf("allocation for window %s state = %q, want released", windowID, state)
		}
	}
	concAfter, _ := f.windowReservedVersion(concurrencyID)
	consAfter, _ := f.windowReservedVersion(consumptionID)
	if concAfter != 0 || consAfter != 0 {
		t.Fatalf("windows after janitor = (concurrency=%v consumption=%v), want (0,0) — headroom freed by EXACTLY the estimate", concAfter, consAfter)
	}
	if n := f.countAudit("quota_janitor_released", ""); n != 1 {
		t.Fatalf("quota_janitor_released audit rows = %d, want 1", n)
	}
}

// ============================================================================
// 2. Post-dispatch ambiguity keeps headroom debited (never auto-released)
// ============================================================================

func TestP3bGate_PostDispatchAmbiguityKeepsHeadroomDebited(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), gateTestTimeout)
	defer cancel()

	f := newQuotaGateFixtureDefault(t)
	f.seedProvider("prov-gate-b")
	const accountID = "acct-gate-b"
	f.seedAccount(ctx, accountID, "prov-gate-b", quota.DefaultLocalSafetyPolicy())
	concurrencyID := f.windowIDByType(accountID, "concurrency")
	consumptionID := f.windowIDByType(accountID, "estimated_consumption")

	result, err := f.reservations.Reserve(ctx, storage.ReserveParams{
		AccountID: accountID, RequestID: "req-b", AttemptID: "attempt-1", Allocations: localSafetyAllocations(),
	})
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := f.lifecycle.MarkDispatched(ctx, result.ReservationID); err != nil {
		t.Fatalf("MarkDispatched: %v", err)
	}

	// POSITIVE CONTROL: genuinely reserved, genuinely dispatched, headroom
	// genuinely debited, before the janitor ever runs.
	if state := f.reservationState(result.ReservationID); state != string(quota.ReservationReserved) {
		t.Fatalf("reservation state = %q, want reserved (positive control)", state)
	}
	if dispatched := f.reservationDispatchedAt(result.ReservationID); !dispatched.Valid {
		t.Fatalf("dispatched_at = NULL, want set (positive control)")
	}
	concBefore, concVersionBefore := f.windowReservedVersion(concurrencyID)
	consBefore, consVersionBefore := f.windowReservedVersion(consumptionID)
	if concBefore != 1 || consBefore != 1 {
		t.Fatalf("windows before janitor = (concurrency=%v consumption=%v), want (1,1) — positive control", concBefore, consBefore)
	}

	f.advance(quota.DefaultProcessingDeadline + time.Second)

	// Sweep 1: the JANITOR ITSELF must discriminate branch A (never
	// dispatched -> released) from branch B (dispatched -> pended) on
	// dispatched_at — this reservation IS dispatched, so it must move to
	// reconciliation_pending, NEVER released. Driving this transition
	// through the real janitor (rather than a direct Transition call)
	// is what makes the branch-A/B discrimination itself observable —
	// a manual Transition call would bypass dispatched_at entirely and
	// could never catch a janitor that releases dispatched reservations
	// by mistake.
	sweep1, err := f.lifecycle.Janitor(ctx)
	if err != nil {
		t.Fatalf("Janitor sweep 1: %v", err)
	}
	if sweep1.Pended != 1 {
		t.Fatalf("sweep 1 Pended = %d, want 1 (dispatched + expired -> reconciliation_pending)", sweep1.Pended)
	}
	if sweep1.Released != 0 || sweep1.UnknownConsumption != 0 {
		t.Fatalf("sweep 1 = %+v, want Released=0 and UnknownConsumption=0 — a dispatched reservation must NEVER be released", sweep1)
	}
	// Branch C1's SELECT does see the row branch B just moved to
	// reconciliation_pending in this same transaction, but it must NOT be
	// counted as reclaimed: reclaiming means taking back an ABANDONED
	// LEASE (02 §3 branch 3), and this row never held one. Reclaimed is
	// reported in a tracked job's result_ref, so counting a never-leased
	// row there would be a dishonest metric.
	if sweep1.Reclaimed != 0 {
		t.Fatalf("sweep 1 Reclaimed = %d, want 0 — a row that never held a lease has nothing to reclaim", sweep1.Reclaimed)
	}
	if state := f.reservationState(result.ReservationID); state != string(quota.ReservationReconciliationPending) {
		t.Fatalf("state after sweep 1 = %q, want reconciliation_pending", state)
	}
	concAfterPend, concVersionAfterPend := f.windowReservedVersion(concurrencyID)
	consAfterPend, consVersionAfterPend := f.windowReservedVersion(consumptionID)
	if concAfterPend != concBefore || concVersionAfterPend != concVersionBefore {
		t.Fatalf("after sweep 1: concurrency window (reserved=%v version=%v), want unchanged (%v,%v) — pending keeps headroom debited", concAfterPend, concVersionAfterPend, concBefore, concVersionBefore)
	}
	if consAfterPend != consBefore || consVersionAfterPend != consVersionBefore {
		t.Fatalf("after sweep 1: consumption window (reserved=%v version=%v), want unchanged (%v,%v)", consAfterPend, consVersionAfterPend, consBefore, consVersionBefore)
	}

	// Sweeps 2 and 3: now reconciliation_pending, run the janitor TWICE
	// MORE, past the SAME expires_at (no further time advance needed —
	// the reconciliation_pending branches never consult expires_at, only
	// lease state and quota.RetryExhausted). Neither sweep may release or
	// terminalize it: this is the "never auto-released on deadline"
	// criterion, the single most important no-leak rule in the phase.
	for sweepNum := 2; sweepNum <= 3; sweepNum++ {
		sweep, err := f.lifecycle.Janitor(ctx)
		if err != nil {
			t.Fatalf("Janitor sweep %d: %v", sweepNum, err)
		}
		if sweep.Released != 0 || sweep.Pended != 0 || sweep.UnknownConsumption != 0 {
			t.Fatalf("sweep %d = %+v, want NEVER released/pended/terminalized", sweepNum, sweep)
		}
		if sweep.Reclaimed != 0 {
			t.Fatalf("sweep %d Reclaimed = %d, want 0 — this reservation never held a lease, so there is nothing to reclaim", sweepNum, sweep.Reclaimed)
		}

		if state := f.reservationState(result.ReservationID); state != string(quota.ReservationReconciliationPending) {
			t.Fatalf("sweep %d: reservation state = %q, want STILL reconciliation_pending — never auto-released on deadline", sweepNum, state)
		}
		for windowID, state := range f.allocationStatesFor(result.ReservationID) {
			if state != string(quota.AllocationReserved) {
				t.Fatalf("sweep %d: allocation for window %s state = %q, want STILL reserved", sweepNum, windowID, state)
			}
		}
		concAfter, concVersionAfter := f.windowReservedVersion(concurrencyID)
		consAfter, consVersionAfter := f.windowReservedVersion(consumptionID)
		if concAfter != concBefore || concVersionAfter != concVersionBefore {
			t.Fatalf("sweep %d: concurrency window (reserved=%v version=%v), want BYTE-FOR-BYTE unchanged (%v,%v)", sweepNum, concAfter, concVersionAfter, concBefore, concVersionBefore)
		}
		if consAfter != consBefore || consVersionAfter != consVersionBefore {
			t.Fatalf("sweep %d: consumption window (reserved=%v version=%v), want BYTE-FOR-BYTE unchanged (%v,%v)", sweepNum, consAfter, consVersionAfter, consBefore, consVersionBefore)
		}
	}
}

// ============================================================================
// 3. Reconciliation settles — low-confidence estimate AND confirmed actual
// ============================================================================

func TestP3bGate_ReconciliationSettles(t *testing.T) {
	t.Run("low_confidence_estimate_via_the_real_worker_path", testP3bGateReconciliationSettlesLowConfidence)
	// actual_confirmed_cost is driven DIRECTLY via QuotaLifecycleRepo.Settle
	// rather than end-to-end: no provider usage-confirmation adapter exists
	// at this layer (03 §1 defines no such QuotaAdapter method), so nothing
	// in this codebase can supply a CONFIRMED actual cost through a real
	// end-to-end call today. This sub-test proves the system settles
	// CORRECTLY once such evidence exists — it does not fabricate a
	// provider that "proves" a cost, and does not weaken the criterion to
	// something else, per this unit's explicit instruction.
	t.Run("confirmed_actual_cost_via_Settle_directly", testP3bGateReconciliationSettlesConfirmedActual)
}

func testP3bGateReconciliationSettlesLowConfidence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), gateTestTimeout)
	defer cancel()

	f := newQuotaGateFixtureDefault(t)
	f.seedProvider("prov-gate-c1")
	const accountID = "acct-gate-c1"
	f.seedAccount(ctx, accountID, "prov-gate-c1", quota.DefaultLocalSafetyPolicy())
	concurrencyID := f.windowIDByType(accountID, "concurrency")
	consumptionID := f.windowIDByType(accountID, "estimated_consumption")

	result, err := f.reservations.Reserve(ctx, storage.ReserveParams{
		AccountID: accountID, RequestID: "req-c1", AttemptID: "attempt-1", Allocations: localSafetyAllocations(),
	})
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := f.lifecycle.MarkDispatched(ctx, result.ReservationID); err != nil {
		t.Fatalf("MarkDispatched: %v", err)
	}
	if err := f.lifecycle.Transition(ctx, result.ReservationID, quota.ReservationReconciliationPending); err != nil {
		t.Fatalf("Transition: %v", err)
	}

	// POSITIVE CONTROL: genuinely pending, headroom genuinely debited.
	if state := f.reservationState(result.ReservationID); state != string(quota.ReservationReconciliationPending) {
		t.Fatalf("state = %q, want reconciliation_pending (positive control)", state)
	}

	f.advance(quota.DefaultProcessingDeadline + f.policy.BaseBackoff + time.Second)

	claimed, err := f.reconciliation.ClaimPending(ctx, "gate-worker-c1", quota.DefaultLeaseTTL)
	if err != nil {
		t.Fatalf("ClaimPending: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("len(claimed) = %d, want 1", len(claimed))
	}
	outcome, err := f.reconciliation.ReconcileOne(ctx, "gate-worker-c1", claimed[0])
	if err != nil {
		t.Fatalf("ReconcileOne: %v", err)
	}
	if outcome.Outcome != quota.ReservationSettled {
		t.Fatalf("outcome = %q, want settled", outcome.Outcome)
	}
	if state := f.reservationState(result.ReservationID); state != string(quota.ReservationSettled) {
		t.Fatalf("reservation state = %q, want settled", state)
	}
	for _, windowID := range []string{concurrencyID, consumptionID} {
		assertSettledAtEstimateLowConfidence(t, f, result.ReservationID, windowID)
	}
}

// assertSettledAtEstimateLowConfidence checks one window's allocation
// settled at its estimate with low confidence and freed its headroom —
// factored out of testP3bGateReconciliationSettlesLowConfidence purely to
// keep that function's cyclomatic complexity within golangci-lint's gocyclo
// budget; it asserts no different behavior than before the extraction.
func assertSettledAtEstimateLowConfidence(t *testing.T, f *quotaGateFixture, reservationID, windowID string) {
	t.Helper()
	state, actualCost, confidence := f.allocationRow(reservationID, windowID)
	if state != string(quota.AllocationSettled) {
		t.Fatalf("window %s allocation state = %q, want settled", windowID, state)
	}
	if !confidence.Valid || confidence.String != string(quota.ConfidenceLow) {
		t.Fatalf("window %s actual_confidence = %+v, want low (no provider-confirmed cost exists at this layer)", windowID, confidence)
	}
	if !actualCost.Valid || actualCost.Float64 != 1 {
		t.Fatalf("window %s actual_cost = %+v, want 1 (settled at the estimate)", windowID, actualCost)
	}
	if reserved, _ := f.windowReservedVersion(windowID); reserved != 0 {
		t.Fatalf("window %s reserved = %v, want 0 (freed)", windowID, reserved)
	}
}

func testP3bGateReconciliationSettlesConfirmedActual(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), gateTestTimeout)
	defer cancel()

	f := newQuotaGateFixtureDefault(t)
	f.seedProvider("prov-gate-c2")
	const accountID = "acct-gate-c2"
	f.seedAccount(ctx, accountID, "prov-gate-c2", quota.DefaultLocalSafetyPolicy())

	// A provider-evidence window on a unit local-safety does NOT cover
	// (output_tokens) — this allocation debits ONLY this one window,
	// isolating the used/remaining arithmetic.
	const windowID = "win-gate-c2-provider"
	f.seedProviderWindow(windowID, accountID, "output_tokens", "rolling", "provider:daily", qf64(100), nil, qf64(0), 0, 1)

	result, err := f.reservations.Reserve(ctx, storage.ReserveParams{
		AccountID: accountID, RequestID: "req-c2", AttemptID: "attempt-1",
		Allocations: []quota.Allocation{{Unit: quota.UnitOutputTokens, Cost: 1, Source: quota.EstimateSourceFromRequest}},
	})
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := f.lifecycle.MarkDispatched(ctx, result.ReservationID); err != nil {
		t.Fatalf("MarkDispatched: %v", err)
	}
	if err := f.lifecycle.Transition(ctx, result.ReservationID, quota.ReservationReconciliationPending); err != nil {
		t.Fatalf("Transition: %v", err)
	}

	// POSITIVE CONTROL: genuinely pending, genuinely debited by the
	// ESTIMATE (1), before Settle is ever called.
	if state := f.reservationState(result.ReservationID); state != string(quota.ReservationReconciliationPending) {
		t.Fatalf("state = %q, want reconciliation_pending (positive control)", state)
	}
	if reservedBefore, _ := f.windowReservedVersion(windowID); reservedBefore != 1 {
		t.Fatalf("reserved before settle = %v, want 1 (positive control)", reservedBefore)
	}

	// Confirmed actual cost (2) DIFFERS from the estimate (1) — proves
	// reserved is freed by the ESTIMATE while used/remaining move by the
	// ACTUAL.
	if err := f.lifecycle.Settle(ctx, result.ReservationID, map[string]float64{windowID: 2}); err != nil {
		t.Fatalf("Settle: %v", err)
	}

	assertConfirmedSettlement(t, f, result.ReservationID, windowID)
}

// assertConfirmedSettlement checks the confirmed-actual-cost settlement
// outcome — factored out purely to keep
// testP3bGateReconciliationSettlesConfirmedActual's cyclomatic complexity
// within golangci-lint's gocyclo budget; it asserts no different behavior
// than before the extraction.
func assertConfirmedSettlement(t *testing.T, f *quotaGateFixture, reservationID, windowID string) {
	t.Helper()
	if state := f.reservationState(reservationID); state != string(quota.ReservationSettled) {
		t.Fatalf("reservation state = %q, want settled", state)
	}
	state, actualCost, confidence := f.allocationRow(reservationID, windowID)
	if state != string(quota.AllocationSettled) {
		t.Fatalf("allocation state = %q, want settled", state)
	}
	if !confidence.Valid || confidence.String != string(quota.ConfidenceHigh) {
		t.Fatalf("actual_confidence = %+v, want high (provider-confirmed)", confidence)
	}
	if !actualCost.Valid || actualCost.Float64 != 2 {
		t.Fatalf("actual_cost = %+v, want 2 (the confirmed actual, not the estimate)", actualCost)
	}
	var used, remaining float64
	if err := f.db.Conn().QueryRow(`SELECT used, remaining FROM quota_windows WHERE id = ?`, windowID).Scan(&used, &remaining); err != nil {
		t.Fatalf("read window used/remaining: %v", err)
	}
	if used != 2 {
		t.Fatalf("used = %v, want 2 (moved by the ACTUAL, since it was already known)", used)
	}
	if remaining != 98 {
		t.Fatalf("remaining = %v, want 98 (100 - actual 2)", remaining)
	}
	if reservedAfter, _ := f.windowReservedVersion(windowID); reservedAfter != 0 {
		t.Fatalf("reserved after settle = %v, want 0 (freed by the ESTIMATE, 1 — not the actual, 2)", reservedAfter)
	}
}

// ============================================================================
// 4. Proven-no-consumption releases (the system's half of the contract)
// ============================================================================

func TestP3bGate_ProvenNoConsumptionReleases(t *testing.T) {
	// The ACQUISITION of "the provider proved no consumption" evidence is a
	// documented gap (no provider usage-confirmation adapter exists at
	// this layer) — this test covers only the system's half of the
	// contract: that Release, once legitimately called from
	// reconciliation_pending, behaves correctly.
	ctx, cancel := context.WithTimeout(context.Background(), gateTestTimeout)
	defer cancel()

	f := newQuotaGateFixtureDefault(t)
	f.seedProvider("prov-gate-d")
	const accountID = "acct-gate-d"
	f.seedAccount(ctx, accountID, "prov-gate-d", quota.DefaultLocalSafetyPolicy())
	concurrencyID := f.windowIDByType(accountID, "concurrency")
	consumptionID := f.windowIDByType(accountID, "estimated_consumption")

	result, err := f.reservations.Reserve(ctx, storage.ReserveParams{
		AccountID: accountID, RequestID: "req-d", AttemptID: "attempt-1", Allocations: localSafetyAllocations(),
	})
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := f.lifecycle.MarkDispatched(ctx, result.ReservationID); err != nil {
		t.Fatalf("MarkDispatched: %v", err)
	}
	if err := f.lifecycle.Transition(ctx, result.ReservationID, quota.ReservationReconciliationPending); err != nil {
		t.Fatalf("Transition: %v", err)
	}

	// POSITIVE CONTROL: genuinely pending, genuinely debited.
	if state := f.reservationState(result.ReservationID); state != string(quota.ReservationReconciliationPending) {
		t.Fatalf("state = %q, want reconciliation_pending (positive control)", state)
	}
	concBefore, _ := f.windowReservedVersion(concurrencyID)
	consBefore, _ := f.windowReservedVersion(consumptionID)
	if concBefore != 1 || consBefore != 1 {
		t.Fatalf("windows before release = (concurrency=%v consumption=%v), want (1,1) — positive control", concBefore, consBefore)
	}

	if err := f.lifecycle.Release(ctx, result.ReservationID); err != nil {
		t.Fatalf("Release: %v", err)
	}

	if state := f.reservationState(result.ReservationID); state != string(quota.ReservationReleased) {
		t.Fatalf("reservation state = %q, want released", state)
	}
	for _, windowID := range []string{concurrencyID, consumptionID} {
		state, actualCost, _ := f.allocationRow(result.ReservationID, windowID)
		if state != string(quota.AllocationReleased) {
			t.Fatalf("window %s allocation state = %q, want released", windowID, state)
		}
		if actualCost.Valid {
			t.Fatalf("window %s actual_cost = %v, want NULL (never consumed)", windowID, actualCost.Float64)
		}
		if reserved, _ := f.windowReservedVersion(windowID); reserved != 0 {
			t.Fatalf("window %s reserved = %v, want 0 (freed)", windowID, reserved)
		}
	}
	var usedConc, usedCons sql.NullFloat64
	if err := f.db.Conn().QueryRow(`SELECT used FROM quota_windows WHERE id = ?`, concurrencyID).Scan(&usedConc); err != nil {
		t.Fatalf("read used: %v", err)
	}
	if err := f.db.Conn().QueryRow(`SELECT used FROM quota_windows WHERE id = ?`, consumptionID).Scan(&usedCons); err != nil {
		t.Fatalf("read used: %v", err)
	}
	if usedConc.Valid || usedCons.Valid {
		t.Fatalf("used = (%+v,%+v), want both STILL NULL (release never touches used)", usedConc, usedCons)
	}
}

// ============================================================================
// 5. Terminal retry boundary records a usage_gap and flags re-baseline
// ============================================================================

func TestP3bGate_TerminalRetryBoundaryRecordsUsageGapAndRebaseline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), gateTestTimeout)
	defer cancel()

	policy := quota.ReconciliationPolicy{MaxRetries: 2, BaseBackoff: 30 * time.Second, MaxBackoff: 30 * time.Minute, BatchSize: 20}
	f := newQuotaGateFixture(t, policy)
	f.seedProvider("prov-gate-e")
	const accountID = "acct-gate-e"
	f.seedAccount(ctx, accountID, "prov-gate-e", quota.DefaultLocalSafetyPolicy())
	concurrencyID := f.windowIDByType(accountID, "concurrency")
	consumptionID := f.windowIDByType(accountID, "estimated_consumption")

	result, err := f.reservations.Reserve(ctx, storage.ReserveParams{
		AccountID: accountID, RequestID: "req-e", AttemptID: "attempt-1", Allocations: localSafetyAllocations(),
	})
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := f.lifecycle.MarkDispatched(ctx, result.ReservationID); err != nil {
		t.Fatalf("MarkDispatched: %v", err)
	}
	if err := f.lifecycle.Transition(ctx, result.ReservationID, quota.ReservationReconciliationPending); err != nil {
		t.Fatalf("Transition: %v", err)
	}

	// Seed reconcile_attempts = MaxRetries-1 directly. This mirrors the
	// EXISTING, already-reviewed storage-level precedent
	// (TestReconcileOne_TerminalRetryBoundary in
	// internal/storage/quota_reconciliation_test.go): ReconcileOne's own
	// non-exhausted branch always resolves a reservation TERMINALLY (via
	// SettleEstimate) on its very first successful call, so there is no
	// live-flow path that leaves a reservation reconciliation_pending with
	// attempts > 0 short of a transient storage failure between the
	// attempt increment and the settle call — a seeded attempts count
	// stands in for "N-1 such failures already happened", exactly as the
	// existing storage-level test already establishes and this suite is
	// instructed to reuse rather than reinvent.
	if _, err := f.db.Conn().Exec(`UPDATE quota_reservations SET reconcile_attempts = ? WHERE id = ?`, policy.MaxRetries-1, result.ReservationID); err != nil {
		t.Fatalf("seed reconcile_attempts: %v", err)
	}

	// POSITIVE CONTROL: genuinely pending, genuinely debited, and nothing
	// recorded yet — BEFORE the terminal boundary is crossed.
	if state := f.reservationState(result.ReservationID); state != string(quota.ReservationReconciliationPending) {
		t.Fatalf("state = %q, want reconciliation_pending (positive control)", state)
	}
	concBefore, _ := f.windowReservedVersion(concurrencyID)
	consBefore, _ := f.windowReservedVersion(consumptionID)
	if concBefore != 1 || consBefore != 1 {
		t.Fatalf("windows before terminal reconcile = (%v,%v), want (1,1) — positive control", concBefore, consBefore)
	}
	if n := f.countAudit(storage.AuditActionUsageGap, result.ReservationID); n != 0 {
		t.Fatalf("usage_gap rows before terminal reconcile = %d, want 0 (positive control: nothing recorded yet)", n)
	}

	f.advance(quota.DefaultProcessingDeadline + policy.MaxBackoff + time.Second)

	claimed, err := f.reconciliation.ClaimPending(ctx, "gate-worker-e", quota.DefaultLeaseTTL)
	if err != nil {
		t.Fatalf("ClaimPending: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("len(claimed) = %d, want 1", len(claimed))
	}
	outcome, err := f.reconciliation.ReconcileOne(ctx, "gate-worker-e", claimed[0])
	if err != nil {
		t.Fatalf("ReconcileOne: %v", err)
	}
	if outcome.Outcome != quota.ReservationUnknownConsumption {
		t.Fatalf("outcome = %q, want unknown_consumption", outcome.Outcome)
	}

	if state := f.reservationState(result.ReservationID); state != string(quota.ReservationUnknownConsumption) {
		t.Fatalf("reservation state = %q, want unknown_consumption", state)
	}
	concAfter, _ := f.windowReservedVersion(concurrencyID)
	consAfter, _ := f.windowReservedVersion(consumptionID)
	if concAfter != concBefore || consAfter != consBefore {
		t.Fatalf("windows after terminal reconcile = (%v,%v), want STILL (%v,%v) — headroom never freed", concAfter, consAfter, concBefore, consBefore)
	}
	if n := f.countAudit(storage.AuditActionUsageGap, result.ReservationID); n != 1 {
		t.Fatalf("usage_gap rows = %d, want exactly 1", n)
	}

	flagged, err := f.reconciliation.RebaselineFlagged(ctx)
	if err != nil {
		t.Fatalf("RebaselineFlagged: %v", err)
	}
	if !containsString(flagged, accountID) {
		t.Fatalf("RebaselineFlagged = %v, want it to contain %q", flagged, accountID)
	}

	// A successful SyncQuotaWindows clears the flag — the "re-baselined at
	// the next quota sync" half of the criterion.
	if err := f.reconciliation.SyncQuotaWindows(ctx, accountID, nil, nil); err != nil {
		t.Fatalf("SyncQuotaWindows: %v", err)
	}
	flaggedAfter, err := f.reconciliation.RebaselineFlagged(ctx)
	if err != nil {
		t.Fatalf("RebaselineFlagged after sync: %v", err)
	}
	if containsString(flaggedAfter, accountID) {
		t.Fatalf("RebaselineFlagged after sync = %v, want %q cleared", flaggedAfter, accountID)
	}
}

// ============================================================================
// 6. Worker-crash lease expiry reclaims (never terminalizes), then recovers
// ============================================================================

func TestP3bGate_WorkerCrashLeaseExpiryReclaims(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), gateTestTimeout)
	defer cancel()

	f := newQuotaGateFixtureDefault(t)
	f.seedProvider("prov-gate-f")
	const accountID = "acct-gate-f"
	f.seedAccount(ctx, accountID, "prov-gate-f", quota.DefaultLocalSafetyPolicy())
	concurrencyID := f.windowIDByType(accountID, "concurrency")
	consumptionID := f.windowIDByType(accountID, "estimated_consumption")

	result, err := f.reservations.Reserve(ctx, storage.ReserveParams{
		AccountID: accountID, RequestID: "req-f", AttemptID: "attempt-1", Allocations: localSafetyAllocations(),
	})
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := f.lifecycle.MarkDispatched(ctx, result.ReservationID); err != nil {
		t.Fatalf("MarkDispatched: %v", err)
	}
	if err := f.lifecycle.Transition(ctx, result.ReservationID, quota.ReservationReconciliationPending); err != nil {
		t.Fatalf("Transition: %v", err)
	}

	f.advance(quota.DefaultProcessingDeadline + f.policy.BaseBackoff + time.Second)

	claimed, err := f.reconciliation.ClaimPending(ctx, "worker-crash", quota.DefaultLeaseTTL)
	if err != nil {
		t.Fatalf("ClaimPending (simulated crashed tick): %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("len(claimed) = %d, want 1", len(claimed))
	}
	// "crash": nothing further is done with claimed — the lease is left to
	// expire on its own.

	// POSITIVE CONTROL: the claim really happened (lease really set)
	// before we advance past its TTL.
	var leaseOwner sql.NullString
	if err := f.db.Conn().QueryRow(`SELECT lease_owner FROM quota_reservations WHERE id = ?`, result.ReservationID).Scan(&leaseOwner); err != nil {
		t.Fatalf("read lease_owner: %v", err)
	}
	if !leaseOwner.Valid || leaseOwner.String != "worker-crash" {
		t.Fatalf("lease_owner = %+v, want worker-crash (positive control)", leaseOwner)
	}
	concBefore, _ := f.windowReservedVersion(concurrencyID)
	consBefore, _ := f.windowReservedVersion(consumptionID)
	attemptsBefore := f.reconcileAttempts(result.ReservationID)

	f.advance(quota.DefaultLeaseTTL + time.Second)

	sweep, err := f.lifecycle.Janitor(ctx)
	if err != nil {
		t.Fatalf("Janitor: %v", err)
	}
	if sweep.Reclaimed != 1 {
		t.Fatalf("Reclaimed = %d, want 1", sweep.Reclaimed)
	}
	if sweep.Released != 0 || sweep.Pended != 0 || sweep.UnknownConsumption != 0 {
		t.Fatalf("sweep = %+v, want ONLY Reclaimed=1", sweep)
	}

	if err := f.db.Conn().QueryRow(`SELECT lease_owner FROM quota_reservations WHERE id = ?`, result.ReservationID).Scan(&leaseOwner); err != nil {
		t.Fatalf("read lease_owner after janitor: %v", err)
	}
	if leaseOwner.Valid {
		t.Fatalf("lease_owner after janitor = %q, want cleared", leaseOwner.String)
	}
	if state := f.reservationState(result.ReservationID); state != string(quota.ReservationReconciliationPending) {
		t.Fatalf("state after janitor = %q, want STILL reconciliation_pending", state)
	}
	for windowID, state := range f.allocationStatesFor(result.ReservationID) {
		if state != string(quota.AllocationReserved) {
			t.Fatalf("allocation for %s state = %q, want STILL reserved", windowID, state)
		}
	}
	concAfter, _ := f.windowReservedVersion(concurrencyID)
	consAfter, _ := f.windowReservedVersion(consumptionID)
	if concAfter != concBefore || consAfter != consBefore {
		t.Fatalf("windows after reclaim = (%v,%v), want unchanged (%v,%v)", concAfter, consAfter, concBefore, consBefore)
	}
	if got := f.reconcileAttempts(result.ReservationID); got != attemptsBefore {
		t.Fatalf("reconcile_attempts after reclaim = %d, want unchanged %d (reclaiming is not an attempt)", got, attemptsBefore)
	}

	// A SECOND ClaimPending now succeeds.
	recovered, err := f.reconciliation.ClaimPending(ctx, "worker-recovery", quota.DefaultLeaseTTL)
	if err != nil {
		t.Fatalf("second ClaimPending: %v", err)
	}
	if len(recovered) != 1 {
		t.Fatalf("len(recovered) = %d, want 1 (must be re-claimable once the crashed lease expired)", len(recovered))
	}
	outcome, err := f.reconciliation.ReconcileOne(ctx, "worker-recovery", recovered[0])
	if err != nil {
		t.Fatalf("ReconcileOne (recovery): %v", err)
	}
	if outcome.Outcome != quota.ReservationSettled {
		t.Fatalf("recovery outcome = %q, want settled", outcome.Outcome)
	}
	// Every allocation ends in ONE consistent state — never half-moved.
	for windowID, state := range f.allocationStatesFor(result.ReservationID) {
		if state != string(quota.AllocationSettled) {
			t.Fatalf("after recovery, allocation for %s state = %q, want settled (never half-moved)", windowID, state)
		}
	}
}

// ============================================================================
// 7. Concurrent reservations never overcommit any window
// ============================================================================

func TestP3bGate_ConcurrentReservationsNeverOvercommitAnyWindow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), gateTestTimeout)
	defer cancel()

	f := newQuotaGateFixtureDefault(t)
	f.seedProvider("prov-gate-g")
	const accountID = "acct-gate-g"
	const capacity = 3
	policy := quota.LocalSafetyPolicy{MaxConcurrency: capacity, EstimatedConsumptionUnit: quota.UnitRequests, EstimatedConsumptionLimit: 1000, EstimatedConsumptionWindow: time.Hour}
	f.seedAccount(ctx, accountID, "prov-gate-g", policy)
	concurrencyID := f.windowIDByType(accountID, "concurrency")

	const callers = 10
	allocations := []quota.Allocation{{Unit: quota.UnitConcurrency, Cost: 1, Source: quota.EstimateSourceFromRequest}}

	// SetMaxOpenConns(1) serializes these callers at the connection-pool
	// level, so this does not exercise interleaved SQL execution — that is
	// TestReserveOnWindow_VersionGuardBites's job in the storage package —
	// but it DOES prove the OUTCOME: repeated real Reserve calls against a
	// shared, capacity-limited window never let more attempts through than
	// the window can hold.
	var wg sync.WaitGroup
	results := make([]error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := f.reservations.Reserve(ctx, storage.ReserveParams{
				AccountID: accountID, RequestID: fmt.Sprintf("req-g-%d", i), AttemptID: fmt.Sprintf("attempt-%d", i),
				Allocations: allocations,
			})
			results[i] = err
		}(i)
	}
	wg.Wait()

	succeeded, rejected := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, storage.ErrReservationRejected):
			rejected++
		default:
			t.Fatalf("unexpected Reserve error: %v", err)
		}
	}
	// POSITIVE CONTROL FIRST: exactly K succeed — NOT "at most K", which a
	// system that admits ZERO reservations would also satisfy.
	if succeeded != capacity {
		t.Fatalf("succeeded = %d, want exactly %d", succeeded, capacity)
	}
	if rejected != callers-capacity {
		t.Fatalf("rejected = %d, want exactly %d", rejected, callers-capacity)
	}

	reserved, _ := f.windowReservedVersion(concurrencyID)
	if reserved != capacity {
		t.Fatalf("final reserved = %v, want exactly %d (never above capacity)", reserved, capacity)
	}
	if n := f.countRows("quota_reservations", "account_id", accountID); n != capacity {
		t.Fatalf("quota_reservations rows = %d, want exactly %d", n, capacity)
	}
	if n := f.countRows("quota_reservation_allocations", "window_id", concurrencyID); n != capacity {
		t.Fatalf("quota_reservation_allocations rows = %d, want exactly %d (one per successful reservation)", n, capacity)
	}
}

// ============================================================================
// 8. Window-key normalization is deterministic, never NULL/empty
// ============================================================================

func TestP3bGate_WindowKeyNormalizationIsDeterministic(t *testing.T) {
	in := quota.WindowKeyInput{ProviderKey: "RPM-Limit!", Unit: quota.UnitRequests}
	first, err := quota.NormalizeWindowKey(in)
	if err != nil {
		t.Fatalf("NormalizeWindowKey: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := quota.NormalizeWindowKey(in)
		if err != nil || again != first {
			t.Fatalf("repeat %d = (%q,%v), want (%q,nil) — same inputs must yield the SAME key", i, again, err, first)
		}
	}
	independent, err := quota.NormalizeWindowKey(quota.WindowKeyInput{ProviderKey: "RPM-Limit!", Unit: quota.UnitRequests})
	if err != nil || independent != first {
		t.Fatalf("independently-constructed input = (%q,%v), want (%q,nil)", independent, err, first)
	}
	if first != "provider:rpm_limit" {
		t.Fatalf("provider-key form = %q, want %q", first, "provider:rpm_limit")
	}

	durationSeconds := 3600
	rolling, err := quota.NormalizeWindowKey(quota.WindowKeyInput{DurationSeconds: &durationSeconds, Unit: quota.UnitTokens})
	if err != nil || rolling != "rolling:3600s" {
		t.Fatalf("empty-provider-key-with-duration form = (%q,%v), want (%q,nil)", rolling, err, "rolling:3600s")
	}

	local, err := quota.NormalizeWindowKey(quota.WindowKeyInput{Unit: quota.UnitBalance})
	if err != nil || local != "local:balance" {
		t.Fatalf("empty-provider-key-no-duration form = (%q,%v), want (%q,nil)", local, err, "local:balance")
	}

	// Never empty, even for a degenerate provider key (all punctuation)
	// that normalizes to the empty token and must fall through.
	degenerate, err := quota.NormalizeWindowKey(quota.WindowKeyInput{ProviderKey: "!!!", Unit: quota.UnitCredits})
	if err != nil {
		t.Fatalf("NormalizeWindowKey(degenerate): %v", err)
	}
	if degenerate == "" {
		t.Fatalf("degenerate provider key produced an EMPTY window_key, want the fallback form")
	}
	if degenerate != "local:credits" {
		t.Fatalf("degenerate form = %q, want fallback %q", degenerate, "local:credits")
	}

	// The persistence half: a window written through the REAL repo (via
	// WindowsFromProviderResult + SyncQuotaWindows, never a hand-written
	// row) always has a non-empty window_key in the database.
	ctx, cancel := context.WithTimeout(context.Background(), gateTestTimeout)
	defer cancel()
	f := newQuotaGateFixtureDefault(t)
	f.seedProvider("prov-gate-key")
	const accountID = "acct-gate-key"
	f.seedAccount(ctx, accountID, "prov-gate-key", quota.DefaultLocalSafetyPolicy())

	result := providers.QuotaResult{Windows: []providers.QuotaWindow{
		{Unit: "requests", WindowType: "rpm", WindowKey: "", DurationSeconds: nil, Confidence: 0.9},
	}}
	specs, err := quota.WindowsFromProviderResult(result, f.clock())
	if err != nil {
		t.Fatalf("WindowsFromProviderResult: %v", err)
	}
	if err := f.reconciliation.SyncQuotaWindows(ctx, accountID, specs, nil); err != nil {
		t.Fatalf("SyncQuotaWindows: %v", err)
	}

	var persistedKey string
	if err := f.db.Conn().QueryRow(
		`SELECT window_key FROM quota_windows WHERE account_id = ? AND source = 'provider_evidence'`, accountID,
	).Scan(&persistedKey); err != nil {
		t.Fatalf("read persisted window_key: %v", err)
	}
	if persistedKey == "" {
		t.Fatalf("persisted window_key is EMPTY, want the deterministic local:requests fallback")
	}
	if persistedKey != "local:requests" {
		t.Fatalf("persisted window_key = %q, want %q", persistedKey, "local:requests")
	}
}

// ============================================================================
// 9. Any window short rejects before execution with NOTHING debited
// ============================================================================

func TestP3bGate_AnyWindowShortRejectsBeforeExecutionWithNothingDebited(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), gateTestTimeout)
	defer cancel()

	f := newQuotaGateFixtureDefault(t)
	f.seedProvider("prov-gate-i")
	const accountID = "acct-gate-i"
	f.seedAccount(ctx, accountID, "prov-gate-i", quota.DefaultLocalSafetyPolicy()) // concurrency cap 1, consumption cap 50
	concurrencyID := f.windowIDByType(accountID, "concurrency")
	consumptionID := f.windowIDByType(accountID, "estimated_consumption")

	// POSITIVE CONTROL: the SAME account reserves successfully once the
	// concurrency window has room.
	first, err := f.reservations.Reserve(ctx, storage.ReserveParams{
		AccountID: accountID, RequestID: "req-i-1", AttemptID: "attempt-1", Allocations: localSafetyAllocations(),
	})
	if err != nil {
		t.Fatalf("first Reserve: %v, want success (positive control)", err)
	}

	concBefore, concVersionBefore := f.windowReservedVersion(concurrencyID)
	consBefore, consVersionBefore := f.windowReservedVersion(consumptionID)
	if concBefore != 1 {
		t.Fatalf("concurrency reserved = %v, want 1 (exhausted by the first reservation)", concBefore)
	}

	// SECOND attempt: concurrency (cap 1) is now exhausted, even though
	// consumption (cap 50) has ample headroom.
	_, err = f.reservations.Reserve(ctx, storage.ReserveParams{
		AccountID: accountID, RequestID: "req-i-2", AttemptID: "attempt-2", Allocations: localSafetyAllocations(),
	})
	if !errors.Is(err, storage.ErrReservationRejected) {
		t.Fatalf("second Reserve error = %v, want ErrReservationRejected", err)
	}

	concAfter, concVersionAfter := f.windowReservedVersion(concurrencyID)
	consAfter, consVersionAfter := f.windowReservedVersion(consumptionID)
	if concAfter != concBefore || concVersionAfter != concVersionBefore {
		t.Fatalf("concurrency window (reserved=%v version=%v), want BYTE-FOR-BYTE unchanged (%v,%v)", concAfter, concVersionAfter, concBefore, concVersionBefore)
	}
	if consAfter != consBefore || consVersionAfter != consVersionBefore {
		t.Fatalf("consumption window (reserved=%v version=%v), want BYTE-FOR-BYTE unchanged (%v,%v) — a headroom-having window must not be debited when ANY applicable window is short", consAfter, consVersionAfter, consBefore, consVersionBefore)
	}
	if n := f.countRows("quota_reservations", "account_id", accountID); n != 1 {
		t.Fatalf("quota_reservations rows = %d, want exactly 1 (only the first, successful reservation)", n)
	}
	if n := f.countRows("quota_reservation_allocations", "reservation_id", first.ReservationID); n != 2 {
		t.Fatalf("allocation rows for the first reservation = %d, want 2", n)
	}
}

// ============================================================================
// 10. Unknown provider quota is still bounded by local safety (both directions)
// ============================================================================

func TestP3bGate_UnknownProviderQuotaIsStillBoundedByLocalSafety(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), gateTestTimeout)
	defer cancel()

	f := newQuotaGateFixtureDefault(t)
	f.seedProvider("prov-gate-j")
	const accountID = "acct-gate-j"
	f.seedAccount(ctx, accountID, "prov-gate-j", quota.DefaultLocalSafetyPolicy())
	const providerWindowID = "win-gate-j-provider"
	// Genuinely unknown: NULL remaining AND NULL limit_value.
	f.seedProviderWindow(providerWindowID, accountID, "requests", "rpm", "provider:rpm", nil, nil, nil, 0, 1)

	allocations := []quota.Allocation{
		{Unit: quota.UnitRequests, Cost: 1, Source: quota.EstimateSourceFromRequest},
		{Unit: quota.UnitConcurrency, Cost: 1, Source: quota.EstimateSourceFromRequest},
	}

	// Direction 1: unknown provider quota never blocks. Healthy
	// local-safety windows admit the reservation; the unknown window is
	// left completely untouched.
	result, err := f.reservations.Reserve(ctx, storage.ReserveParams{
		AccountID: accountID, RequestID: "req-j-1", AttemptID: "attempt-1", Allocations: allocations,
	})
	if err != nil {
		t.Fatalf("Reserve: %v, want success (unknown provider quota must not block)", err)
	}
	reservedUnknown, versionUnknown := f.windowReservedVersion(providerWindowID)
	if reservedUnknown != 0 || versionUnknown != 1 {
		t.Fatalf("unknown-capacity window = (reserved=%v version=%v), want (0,1) untouched", reservedUnknown, versionUnknown)
	}
	for _, d := range result.Debits {
		if d.WindowID == providerWindowID {
			t.Fatalf("unknown-capacity window was debited: %+v", result.Debits)
		}
	}

	// Direction 2: this SAME account, now that its local-safety
	// concurrency window (cap 1) is exhausted by the reservation above, is
	// rejected — unknown provider quota is never unlimited.
	_, err = f.reservations.Reserve(ctx, storage.ReserveParams{
		AccountID: accountID, RequestID: "req-j-2", AttemptID: "attempt-2", Allocations: allocations,
	})
	if !errors.Is(err, storage.ErrReservationRejected) {
		t.Fatalf("second Reserve error = %v, want ErrReservationRejected once local-safety concurrency is exhausted", err)
	}
}

// ============================================================================
// 11. Quota renders per account per window through the REAL ControlMux
// ============================================================================

type gateQuotaWindowJSON struct {
	Source     string   `json:"source"`
	Unit       string   `json:"unit"`
	WindowType string   `json:"window_type"`
	WindowKey  string   `json:"window_key"`
	State      string   `json:"state"`
	Freshness  string   `json:"freshness"`
	Used       *float64 `json:"used"`
	Remaining  *float64 `json:"remaining"`
	ResetAt    *int64   `json:"reset_at"`
}

type gateAccountListItemJSON struct {
	ID    string                `json:"id"`
	Quota []gateQuotaWindowJSON `json:"quota"`
}

func TestP3bGate_QuotaRendersPerAccountPerWindowThroughTheMux(t *testing.T) {
	db := testControlDB(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))
	cookie := setupOwner(t, mux, testSetupPassword)

	const providerID = "prov-gate-k"
	const accountID = "acct-gate-k"
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

	windowRepo := storage.NewQuotaWindowRepo(db, nil, nil)
	specs, err := quota.DefaultLocalSafetyPolicy().MandatoryWindows()
	if err != nil {
		t.Fatalf("MandatoryWindows: %v", err)
	}
	if err := windowRepo.EnsureLocalSafetyWindows(context.Background(), accountID, specs); err != nil {
		t.Fatalf("EnsureLocalSafetyWindows: %v", err)
	}
	// A provider-evidence window with genuinely unknown numerics
	// (used/remaining/total/limit_value all NULL, freshness unknown).
	if _, err := db.Conn().Exec(
		`INSERT INTO quota_windows (id, account_id, source, unit, window_type, window_key, reserved, version, confidence, freshness_state, observed_at, created_at, updated_at)
		 VALUES (?, ?, 'provider_evidence', 'requests', 'rpm', 'provider:rpm', 0, 1, 0.5, 'unknown', 0, 0, 0)`,
		"win-gate-k-provider", accountID,
	); err != nil {
		t.Fatalf("seed provider window: %v", err)
	}

	req := newAuthRequest(t, http.MethodGet, "/api/control/v1/accounts", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /accounts status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}
	raw := rec.Body.String()

	if !strings.Contains(raw, `"used":null`) {
		t.Fatalf("response missing a JSON null used for the unknown window: %q", raw)
	}
	if strings.Contains(raw, `"used":0`) {
		t.Fatalf("response fabricated a 0 for an unknown numeric: %q", raw)
	}

	var body struct {
		Data struct {
			Accounts []gateAccountListItemJSON `json:"accounts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body = %q", err, raw)
	}
	var acct *gateAccountListItemJSON
	for i := range body.Data.Accounts {
		if body.Data.Accounts[i].ID == accountID {
			acct = &body.Data.Accounts[i]
		}
	}
	if acct == nil {
		t.Fatalf("account %s not found in list: %+v", accountID, body.Data.Accounts)
	}
	if len(acct.Quota) != 3 {
		t.Fatalf("len(quota) = %d, want 3 (2 local-safety + 1 provider-evidence)", len(acct.Quota))
	}

	bySource := map[string][]string{}
	for _, w := range acct.Quota {
		bySource[w.Source] = append(bySource[w.Source], w.WindowKey)
		if w.WindowKey == "provider:rpm" {
			if w.Used != nil {
				t.Fatalf("provider:rpm used = %v, want nil (unknown)", *w.Used)
			}
			if w.State != "unknown" {
				t.Fatalf("provider:rpm state = %q, want unknown", w.State)
			}
		}
	}
	if len(bySource["local_safety"]) != 2 {
		t.Fatalf("local_safety windows = %v, want 2", bySource["local_safety"])
	}
	if len(bySource["provider_evidence"]) != 1 {
		t.Fatalf("provider_evidence windows = %v, want 1 (distinguishable by source from local_safety)", bySource["provider_evidence"])
	}
}

// ============================================================================
// 12. Exhaustion is scoped to one account
// ============================================================================

func TestP3bGate_ExhaustionIsScopedToOneAccount(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), gateTestTimeout)
	defer cancel()

	f := newQuotaGateFixtureDefault(t)
	f.seedProvider("prov-gate-l")
	const accountA = "acct-gate-l-a"
	const accountB = "acct-gate-l-b"
	f.seedAccount(ctx, accountA, "prov-gate-l", quota.DefaultLocalSafetyPolicy()) // cap 1
	bPolicy := quota.LocalSafetyPolicy{MaxConcurrency: 5, EstimatedConsumptionUnit: quota.UnitRequests, EstimatedConsumptionLimit: 1000, EstimatedConsumptionWindow: time.Hour}
	f.seedAccount(ctx, accountB, "prov-gate-l", bPolicy) // cap 5, independent of A

	// POSITIVE CONTROL: reservations work normally for B, before A is
	// ever touched.
	if _, err := f.reservations.Reserve(ctx, storage.ReserveParams{
		AccountID: accountB, RequestID: "req-l-b-1", AttemptID: "attempt-1", Allocations: localSafetyAllocations(),
	}); err != nil {
		t.Fatalf("Reserve for B (1st): %v, want success (positive control)", err)
	}

	// Exhaust A's concurrency window (cap 1).
	if _, err := f.reservations.Reserve(ctx, storage.ReserveParams{
		AccountID: accountA, RequestID: "req-l-a-1", AttemptID: "attempt-1", Allocations: localSafetyAllocations(),
	}); err != nil {
		t.Fatalf("Reserve for A (1st): %v, want success", err)
	}
	if _, err := f.reservations.Reserve(ctx, storage.ReserveParams{
		AccountID: accountA, RequestID: "req-l-a-2", AttemptID: "attempt-2", Allocations: localSafetyAllocations(),
	}); !errors.Is(err, storage.ErrReservationRejected) {
		t.Fatalf("Reserve for A (2nd, exhausted) error = %v, want ErrReservationRejected", err)
	}

	// B, with its own independent windows, still reserves successfully in
	// the SAME database — A's exhaustion/rejection never leaked into B.
	if _, err := f.reservations.Reserve(ctx, storage.ReserveParams{
		AccountID: accountB, RequestID: "req-l-b-2", AttemptID: "attempt-2", Allocations: localSafetyAllocations(),
	}); err != nil {
		t.Fatalf("Reserve for B (2nd): %v, want success (A's exhaustion must not affect B)", err)
	}

	aConcID := f.windowIDByType(accountA, "concurrency")
	bConcID := f.windowIDByType(accountB, "concurrency")
	aReserved, _ := f.windowReservedVersion(aConcID)
	bReserved, _ := f.windowReservedVersion(bConcID)
	if aReserved != 1 {
		t.Fatalf("A concurrency reserved = %v, want 1 (only its one successful reservation)", aReserved)
	}
	if bReserved != 2 {
		t.Fatalf("B concurrency reserved = %v, want 2 (both its reservations, unaffected by A)", bReserved)
	}
	if n := f.countRows("quota_reservations", "account_id", accountA); n != 1 {
		t.Fatalf("A reservation rows = %d, want 1", n)
	}
	if n := f.countRows("quota_reservations", "account_id", accountB); n != 2 {
		t.Fatalf("B reservation rows = %d, want 2", n)
	}
}

// ============================================================================
// 13. Canary — no secret or content leaks anywhere in the reservation stack
// ============================================================================

func TestP3bGate_NoSecretOrContentLeaksAnywhere(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), gateTestTimeout)
	defer cancel()

	// The canary is shaped to be PROPAGATABLE, not a bare unused string
	// (the P3a lesson): it is stored as BOTH the account's real identity
	// email AND its real encrypted credential's plaintext, through the
	// SAME CredentialService.Store path accounts_test.go's own reveal
	// tests use. The quota subsystem itself has no plausible business
	// reason to ever read credential material or identity email — so a
	// clean scan here is a genuine regression guard: if a future change
	// ever threads account identity or credential material into an audit
	// reason_code, a job's result_ref, or a quota table's free-text
	// column (e.g. a well-intentioned but leaky debug string built from
	// the account row), this test catches it.
	const canary = "CANARY-QUOTA-GATE-SECRET-8f2ad91c"

	f := newQuotaGateFixtureDefault(t)
	f.seedProvider("prov-gate-canary")
	const accountID = "acct-gate-canary"

	if _, err := f.db.Conn().Exec(
		`INSERT INTO accounts (id, provider_id, external_id, auth_type, connection_state, health_state, identity_email, created_at, updated_at)
		 VALUES (?, ?, ?, 'api_key', 'connected', 'healthy', ?, ?, ?)`,
		accountID, "prov-gate-canary", accountID, canary, f.clockUnix, f.clockUnix,
	); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	specs, err := quota.DefaultLocalSafetyPolicy().MandatoryWindows()
	if err != nil {
		t.Fatalf("MandatoryWindows: %v", err)
	}
	if err := f.windows.EnsureLocalSafetyWindows(ctx, accountID, specs); err != nil {
		t.Fatalf("EnsureLocalSafetyWindows: %v", err)
	}

	credRepo := storage.NewAccountCredentialRepo(f.db)
	kr := testKeyring(t)
	credSvc := application.NewCredentialService(credRepo, kr, f.clock)
	if _, err := credSvc.Store(ctx, application.StoreCredentialParams{
		ID: "cred-gate-canary", AccountID: accountID, ProviderID: "prov-gate-canary",
		Kind: domain.CredentialKindAPIKey, Active: true, PlaintextKey: canary,
	}); err != nil {
		t.Fatalf("store credential: %v", err)
	}

	// Full lifecycle: reserve -> dispatch -> ambiguity -> janitor -> worker
	// (settle), so quota_windows/quota_reservations/allocations/jobs/
	// audit_events all get at least one write from this account's real
	// activity.
	result, err := f.reservations.Reserve(ctx, storage.ReserveParams{
		AccountID: accountID, RequestID: "req-canary-1", AttemptID: "attempt-1", Allocations: localSafetyAllocations(),
	})
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := f.lifecycle.MarkDispatched(ctx, result.ReservationID); err != nil {
		t.Fatalf("MarkDispatched: %v", err)
	}
	if err := f.lifecycle.Transition(ctx, result.ReservationID, quota.ReservationReconciliationPending); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	f.advance(quota.DefaultProcessingDeadline + f.policy.BaseBackoff + time.Second)
	if _, err := f.lifecycle.Janitor(ctx); err != nil {
		t.Fatalf("Janitor: %v", err)
	}
	if _, err := f.workers.ReconcileTick(ctx); err != nil {
		t.Fatalf("ReconcileTick: %v", err)
	}

	// A second reservation driven to the terminal unknown_consumption
	// boundary, so the usage_gap audit path is exercised too.
	second, err := f.reservations.Reserve(ctx, storage.ReserveParams{
		AccountID: accountID, RequestID: "req-canary-2", AttemptID: "attempt-2", Allocations: localSafetyAllocations(),
	})
	if err != nil {
		t.Fatalf("second Reserve: %v", err)
	}
	if err := f.lifecycle.MarkDispatched(ctx, second.ReservationID); err != nil {
		t.Fatalf("MarkDispatched (second): %v", err)
	}
	if err := f.lifecycle.Transition(ctx, second.ReservationID, quota.ReservationReconciliationPending); err != nil {
		t.Fatalf("Transition (second): %v", err)
	}
	if _, err := f.db.Conn().Exec(`UPDATE quota_reservations SET reconcile_attempts = ? WHERE id = ?`, f.policy.MaxRetries-1, second.ReservationID); err != nil {
		t.Fatalf("seed reconcile_attempts (second): %v", err)
	}
	f.advance(2 * time.Hour)
	claimed, err := f.reconciliation.ClaimPending(ctx, "gate-canary-worker", quota.DefaultLeaseTTL)
	if err != nil {
		t.Fatalf("ClaimPending: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("len(claimed) = %d, want 1", len(claimed))
	}
	if _, err := f.reconciliation.ReconcileOne(ctx, "gate-canary-worker", claimed[0]); err != nil {
		t.Fatalf("ReconcileOne: %v", err)
	}

	// A cooldown write too, so the cooldowns table is exercised.
	trigger := &quota.CooldownTrigger{
		Scope: quota.CooldownScopeAccount, ScopeRef: accountID,
		Until: f.clock().Add(time.Hour), Source: quota.CooldownSourceDefaultBackoff, ReasonCode: "gate_canary_test",
	}
	if err := f.reconciliation.SyncQuotaWindows(ctx, accountID, nil, trigger); err != nil {
		t.Fatalf("SyncQuotaWindows with trigger: %v", err)
	}

	assertTableHasNoCanary(t, f.db, "quota_windows", []string{"id", "account_id", "source", "unit", "window_type", "window_key", "freshness_state"}, canary)
	assertTableHasNoCanary(t, f.db, "quota_reservations", []string{"id", "account_id", "request_id", "attempt_id", "state", "lease_owner"}, canary)
	assertTableHasNoCanary(t, f.db, "quota_reservation_allocations", []string{"reservation_id", "window_id", "unit", "estimate_source", "state", "actual_confidence"}, canary)
	assertTableHasNoCanary(t, f.db, "cooldowns", []string{"id", "scope", "account_id", "reason_code", "source"}, canary)
	assertTableHasNoCanary(t, f.db, "jobs", []string{"id", "kind", "status", "result_ref", "error"}, canary)
	assertTableHasNoCanary(t, f.db, "audit_events", []string{"action", "entity_type", "entity_id", "result", "reason_code"}, canary)
}
