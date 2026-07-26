package storage

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

// seedWindowWithUsed is seedWindowFull's twin with an explicit `used`
// column, needed by the settle tests to prove used/remaining are only
// ever adjusted where already known.
func seedWindowWithUsed(t *testing.T, db *DB, id, accountID, source, unit, windowType, key string, used, remaining, limitValue *float64, reserved float64, version int64) {
	t.Helper()
	_, err := db.Conn().Exec(
		`INSERT INTO quota_windows
		    (id, account_id, source, unit, window_type, window_key, used, remaining, limit_value, reserved, version, confidence, freshness_state, observed_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, 'fresh', 0, 0, 0)`,
		id, accountID, source, unit, windowType, key, used, remaining, limitValue, reserved, version,
	)
	if err != nil {
		t.Fatalf("seed window %s: %v", id, err)
	}
}

func readWindowFull(t *testing.T, db *DB, windowID string) (used, remaining *float64, reserved float64, version int64) {
	t.Helper()
	var u, rem sql.NullFloat64
	if err := db.Conn().QueryRow(
		`SELECT used, remaining, reserved, version FROM quota_windows WHERE id = ?`, windowID,
	).Scan(&u, &rem, &reserved, &version); err != nil {
		t.Fatalf("read window %s: %v", windowID, err)
	}
	if u.Valid {
		v := u.Float64
		used = &v
	}
	if rem.Valid {
		v := rem.Float64
		remaining = &v
	}
	return
}

func readAllocation(t *testing.T, db *DB, reservationID, windowID string) (state string, actualCost *float64) {
	t.Helper()
	var s string
	var ac sql.NullFloat64
	if err := db.Conn().QueryRow(
		`SELECT state, actual_cost FROM quota_reservation_allocations WHERE reservation_id = ? AND window_id = ?`,
		reservationID, windowID,
	).Scan(&s, &ac); err != nil {
		t.Fatalf("read allocation (%s,%s): %v", reservationID, windowID, err)
	}
	if ac.Valid {
		v := ac.Float64
		actualCost = &v
	}
	return s, actualCost
}

func readReservationState(t *testing.T, db *DB, reservationID string) (state string, settledAt sql.NullInt64) {
	t.Helper()
	if err := db.Conn().QueryRow(
		`SELECT state, settled_at FROM quota_reservations WHERE id = ?`, reservationID,
	).Scan(&state, &settledAt); err != nil {
		t.Fatalf("read reservation %s: %v", reservationID, err)
	}
	return state, settledAt
}

// reserveFixture seeds a provider window (with known used/remaining) and
// the two mandatory local-safety windows, then drives a real Reserve
// through QuotaReservationRepo so every lifecycle test exercises real
// fixtures rather than hand-crafted rows.
func reserveFixture(t *testing.T, ctx context.Context, db *DB, accountID, reqID, attemptID string) (reservationID, providerWindowID, concurrencyWindowID string) {
	t.Helper()
	providerWindowID = "win-provider-" + accountID
	concurrencyWindowID = "win-concurrency-" + accountID

	insertProvider(t, db, "prov-"+accountID)
	insertAccount(t, db, accountID, "prov-"+accountID)
	seedWindowWithUsed(t, db, providerWindowID, accountID, "provider_evidence", "requests", "rpm", "provider:rpm", float64Ptr(100), float64Ptr(900), nil, 0, 1)
	seedWindowFull(t, db, concurrencyWindowID, accountID, "local_safety", "concurrency", "concurrency", "local:concurrency", nil, float64Ptr(1), 0, 1)

	allocations := []quota.Allocation{
		{Unit: quota.UnitRequests, Cost: 1, Source: quota.EstimateSourceFromRequest},
		{Unit: quota.UnitConcurrency, Cost: 1, Source: quota.EstimateSourceFromRequest},
	}
	repo := NewQuotaReservationRepo(db, fixedQuotaClock(1000))
	result, err := repo.Reserve(ctx, ReserveParams{AccountID: accountID, RequestID: reqID, AttemptID: attemptID, Allocations: allocations})
	if err != nil {
		t.Fatalf("fixture Reserve: %v", err)
	}
	return result.ReservationID, providerWindowID, concurrencyWindowID
}

func TestSettle_ConvertsHoldToConsumptionAcrossEveryWindow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	reservationID, providerWindowID, concurrencyWindowID := reserveFixture(t, ctx, db, "acct-settle", "req-settle", "attempt-1")

	// Simulate a second, concurrent reservation's hold on the SAME
	// provider window (reserved 1 -> 11) BEFORE settling this one. If
	// settle ever subtracted the ACTUAL (3) instead of the ESTIMATE (1),
	// the two computations would still coincidentally agree at reserved=0
	// when the only hold present were this allocation's own — flooring at
	// 0 masks the bug. With another 10 units of headroom debited
	// alongside it, estimate-subtraction (11-1=10) and actual-subtraction
	// (11-3=8) diverge, so this is the scenario that actually catches a
	// wrong-quantity settle.
	if _, err := db.Conn().Exec(`UPDATE quota_windows SET reserved = reserved + 10, version = version + 1 WHERE id = ?`, providerWindowID); err != nil {
		t.Fatalf("simulate concurrent hold: %v", err)
	}

	lifecycle := NewQuotaLifecycleRepo(db, fixedQuotaClock(2000), nil)
	actuals := map[string]float64{providerWindowID: 3} // estimate was 1; actual differs
	if err := lifecycle.Settle(ctx, reservationID, actuals); err != nil {
		t.Fatalf("Settle: %v", err)
	}

	used, remaining, reserved, version := readWindowFull(t, db, providerWindowID)
	if reserved != 10 {
		t.Fatalf("provider window reserved = %v, want 10 (11 - the ESTIMATE of 1, not the actual of 3)", reserved)
	}
	if version != 4 { // 1 (seed) -> 2 (reserve) -> 3 (simulated concurrent hold) -> 4 (settle)
		t.Fatalf("provider window version = %v, want 4", version)
	}
	if used == nil || *used != 103 {
		t.Fatalf("provider window used = %v, want 103 (100 + ACTUAL 3)", used)
	}
	if remaining == nil || *remaining != 897 {
		t.Fatalf("provider window remaining = %v, want 897 (900 - ACTUAL 3)", remaining)
	}
	allocState, actualCost := readAllocation(t, db, reservationID, providerWindowID)
	if allocState != "settled" || actualCost == nil || *actualCost != 3 {
		t.Fatalf("provider allocation = (state=%s actual=%v), want (settled, 3)", allocState, actualCost)
	}

	// concurrency window: no actuals entry supplied, so it settles at its
	// own estimate (1), and it never carried used/remaining to begin with.
	usedC, remainingC, reservedC, versionC := readWindowFull(t, db, concurrencyWindowID)
	if reservedC != 0 {
		t.Fatalf("concurrency window reserved = %v, want 0", reservedC)
	}
	if versionC != 3 {
		t.Fatalf("concurrency window version = %v, want 3", versionC)
	}
	if usedC != nil || remainingC != nil {
		t.Fatalf("concurrency window used/remaining = (%v,%v), want (nil,nil)", usedC, remainingC)
	}

	state, settledAt := readReservationState(t, db, reservationID)
	if state != "settled" {
		t.Fatalf("reservation state = %q, want settled", state)
	}
	if !settledAt.Valid {
		t.Fatalf("settled_at not set")
	}
}

// TestSettle_NeverSeedsUnknownUsedOrRemaining proves settling a window
// whose used/remaining are NULL (only limit_value known — the
// local-safety shape) leaves them NULL: reserved still drops by the
// estimate, but no locally-derived baseline is ever fabricated as
// provider evidence.
func TestSettle_NeverSeedsUnknownUsedOrRemaining(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	reservationID, _, concurrencyWindowID := reserveFixture(t, ctx, db, "acct-nullsettle", "req-nullsettle", "attempt-1")

	lifecycle := NewQuotaLifecycleRepo(db, fixedQuotaClock(2000), nil)
	if err := lifecycle.Settle(ctx, reservationID, nil); err != nil {
		t.Fatalf("Settle: %v", err)
	}

	used, remaining, reserved, _ := readWindowFull(t, db, concurrencyWindowID)
	if used != nil {
		t.Fatalf("used = %v, want nil (never seeded from a NULL baseline)", *used)
	}
	if remaining != nil {
		t.Fatalf("remaining = %v, want nil (never seeded from a NULL baseline)", *remaining)
	}
	if reserved != 0 {
		t.Fatalf("reserved = %v, want 0", reserved)
	}
}

func TestSettle_DefaultsToEstimateForUnlistedWindow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	reservationID, providerWindowID, concurrencyWindowID := reserveFixture(t, ctx, db, "acct-unlisted", "req-unlisted", "attempt-1")

	lifecycle := NewQuotaLifecycleRepo(db, fixedQuotaClock(2000), nil)
	// Only the provider window is listed; the concurrency window is
	// unlisted and must settle at its own estimate (1).
	if err := lifecycle.Settle(ctx, reservationID, map[string]float64{providerWindowID: 5}); err != nil {
		t.Fatalf("Settle: %v", err)
	}

	_, actualCost := readAllocation(t, db, reservationID, concurrencyWindowID)
	if actualCost == nil || *actualCost != 1 {
		t.Fatalf("unlisted window actual_cost = %v, want 1 (its own estimate)", actualCost)
	}
	_, _, reservedC, _ := readWindowFull(t, db, concurrencyWindowID)
	if reservedC != 0 {
		t.Fatalf("unlisted window reserved = %v, want 0 (dropped by its estimate)", reservedC)
	}
}

func TestSettle_RejectsUnknownWindowIDAndBadActual(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	reservationID, providerWindowID, _ := reserveFixture(t, ctx, db, "acct-badactual", "req-badactual", "attempt-1")
	lifecycle := NewQuotaLifecycleRepo(db, fixedQuotaClock(2000), nil)

	t.Run("unknown window id", func(t *testing.T) {
		err := lifecycle.Settle(ctx, reservationID, map[string]float64{"win-does-not-exist": 1})
		if !errors.Is(err, ErrInvalidSettlement) {
			t.Fatalf("Settle error = %v, want ErrInvalidSettlement", err)
		}
		state, _ := readReservationState(t, db, reservationID)
		if state != "reserved" {
			t.Fatalf("reservation state = %q, want reserved (nothing written)", state)
		}
	})

	t.Run("negative actual cost", func(t *testing.T) {
		err := lifecycle.Settle(ctx, reservationID, map[string]float64{providerWindowID: -1})
		if !errors.Is(err, ErrInvalidSettlement) {
			t.Fatalf("Settle error = %v, want ErrInvalidSettlement", err)
		}
		state, _ := readReservationState(t, db, reservationID)
		if state != "reserved" {
			t.Fatalf("reservation state = %q, want reserved (nothing written)", state)
		}
	})
}

func TestRelease_FreesHeadroomWithoutConsuming(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	reservationID, providerWindowID, concurrencyWindowID := reserveFixture(t, ctx, db, "acct-release", "req-release", "attempt-1")

	lifecycle := NewQuotaLifecycleRepo(db, fixedQuotaClock(2000), nil)
	if err := lifecycle.Release(ctx, reservationID); err != nil {
		t.Fatalf("Release: %v", err)
	}

	usedP, remainingP, reservedP, _ := readWindowFull(t, db, providerWindowID)
	if reservedP != 0 {
		t.Fatalf("provider reserved = %v, want 0", reservedP)
	}
	if usedP == nil || *usedP != 100 || remainingP == nil || *remainingP != 900 {
		t.Fatalf("provider used/remaining = (%v,%v), want (100,900) untouched", usedP, remainingP)
	}
	allocState, actualCost := readAllocation(t, db, reservationID, providerWindowID)
	if allocState != "released" || actualCost != nil {
		t.Fatalf("provider allocation = (state=%s actual=%v), want (released, nil)", allocState, actualCost)
	}

	_, _, reservedC, _ := readWindowFull(t, db, concurrencyWindowID)
	if reservedC != 0 {
		t.Fatalf("concurrency reserved = %v, want 0", reservedC)
	}

	state, settledAt := readReservationState(t, db, reservationID)
	if state != "released" || !settledAt.Valid {
		t.Fatalf("reservation = (state=%s settled_at valid=%v), want (released, true)", state, settledAt.Valid)
	}
}

func TestReconciliationPending_KeepsHeadroomDebited(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	reservationID, providerWindowID, concurrencyWindowID := reserveFixture(t, ctx, db, "acct-pending", "req-pending", "attempt-1")

	before := map[string][2]any{}
	for _, id := range []string{providerWindowID, concurrencyWindowID} {
		_, _, reserved, version := readWindowFull(t, db, id)
		before[id] = [2]any{reserved, version}
	}

	lifecycle := NewQuotaLifecycleRepo(db, fixedQuotaClock(2000), nil)
	if err := lifecycle.Transition(ctx, reservationID, quota.ReservationReconciliationPending); err != nil {
		t.Fatalf("Transition to reconciliation_pending: %v", err)
	}

	state, _ := readReservationState(t, db, reservationID)
	if state != "reconciliation_pending" {
		t.Fatalf("reservation state = %q, want reconciliation_pending", state)
	}
	for _, id := range []string{providerWindowID, concurrencyWindowID} {
		allocState, _ := readAllocation(t, db, reservationID, id)
		if allocState != "reserved" {
			t.Fatalf("allocation %s state = %q, want reserved (headroom stays debited)", id, allocState)
		}
		_, _, reserved, version := readWindowFull(t, db, id)
		wantReserved, wantVersion := before[id][0], before[id][1]
		if reserved != wantReserved || version != wantVersion {
			t.Fatalf("window %s (reserved=%v version=%v), want (%v,%v) byte-for-byte unchanged", id, reserved, version, wantReserved, wantVersion)
		}
	}

	// Advance to unknown_consumption: allocations move, reserved STILL
	// not freed.
	if err := lifecycle.Transition(ctx, reservationID, quota.ReservationUnknownConsumption); err != nil {
		t.Fatalf("Transition to unknown_consumption: %v", err)
	}
	state, _ = readReservationState(t, db, reservationID)
	if state != "unknown_consumption" {
		t.Fatalf("reservation state = %q, want unknown_consumption", state)
	}
	for _, id := range []string{providerWindowID, concurrencyWindowID} {
		allocState, _ := readAllocation(t, db, reservationID, id)
		if allocState != "unknown_consumption" {
			t.Fatalf("allocation %s state = %q, want unknown_consumption", id, allocState)
		}
		_, _, reserved, _ := readWindowFull(t, db, id)
		if reserved == 0 {
			t.Fatalf("window %s reserved = 0, want still debited (never freed silently)", id)
		}
	}
}

func TestTransition_IllegalIsRejectedAuditedAndLeavesStateUnchanged(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	reservationID, providerWindowID, concurrencyWindowID := reserveFixture(t, ctx, db, "acct-illegal", "req-illegal", "attempt-1")

	audit := NewAuditEventRepo(db)
	lifecycle := NewQuotaLifecycleRepo(db, fixedQuotaClock(2000), audit)

	if err := lifecycle.Settle(ctx, reservationID, nil); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	beforeP := snapshotWindow(t, db, providerWindowID)
	beforeC := snapshotWindow(t, db, concurrencyWindowID)

	err := lifecycle.Transition(ctx, reservationID, quota.ReservationReserved)
	if !errors.Is(err, quota.ErrIllegalTransition) {
		t.Fatalf("Transition(settled->reserved) error = %v, want ErrIllegalTransition", err)
	}
	state, _ := readReservationState(t, db, reservationID)
	if state != "settled" {
		t.Fatalf("reservation state = %q, want settled (unchanged)", state)
	}
	if got := snapshotWindow(t, db, providerWindowID); !got.equal(beforeP) {
		t.Fatalf("provider window changed by a rejected transition: before=%+v after=%+v", beforeP, got)
	}
	if got := snapshotWindow(t, db, concurrencyWindowID); !got.equal(beforeC) {
		t.Fatalf("concurrency window changed by a rejected transition: before=%+v after=%+v", beforeC, got)
	}

	var auditCount int
	if err := db.Conn().QueryRow(
		`SELECT COUNT(*) FROM audit_events WHERE entity_id = ? AND result = 'rejected'`, reservationID,
	).Scan(&auditCount); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("rejected-transition audit rows = %d, want 1", auditCount)
	}

	// reconciliation_pending -> reserved is also illegal (named in 02 §3).
	reservationID2, _, _ := reserveFixture(t, ctx, db, "acct-illegal2", "req-illegal2", "attempt-1")
	if err := lifecycle.Transition(ctx, reservationID2, quota.ReservationReconciliationPending); err != nil {
		t.Fatalf("Transition to reconciliation_pending: %v", err)
	}
	err = lifecycle.Transition(ctx, reservationID2, quota.ReservationReserved)
	if !errors.Is(err, quota.ErrIllegalTransition) {
		t.Fatalf("Transition(reconciliation_pending->reserved) error = %v, want ErrIllegalTransition", err)
	}
	state2, _ := readReservationState(t, db, reservationID2)
	if state2 != "reconciliation_pending" {
		t.Fatalf("reservation2 state = %q, want reconciliation_pending (unchanged)", state2)
	}
}

// snapshotWindow packs a window's mutable fields into a comparable value
// for before/after equality checks.
type windowSnapshot struct {
	used, remaining *float64
	reserved        float64
	version         int64
}

func snapshotWindow(t *testing.T, db *DB, windowID string) windowSnapshot {
	t.Helper()
	used, remaining, reserved, version := readWindowFull(t, db, windowID)
	return windowSnapshot{used: used, remaining: remaining, reserved: reserved, version: version}
}

// equal compares two snapshots by VALUE — used/remaining are *float64,
// and readWindowFull mints a fresh pointer on every call, so a plain
// (a == b) struct comparison would compare pointer addresses (always
// unequal) rather than the numbers they point to.
func (a windowSnapshot) equal(b windowSnapshot) bool {
	return floatPtrEqual(a.used, b.used) && floatPtrEqual(a.remaining, b.remaining) &&
		a.reserved == b.reserved && a.version == b.version
}

func floatPtrEqual(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func TestLifecycle_IsIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	lifecycle := NewQuotaLifecycleRepo(db, fixedQuotaClock(2000), nil)

	t.Run("settle twice is a no-op the second time", func(t *testing.T) {
		reservationID, providerWindowID, _ := reserveFixture(t, ctx, db, "acct-idem-settle", "req-idem-settle", "attempt-1")
		if err := lifecycle.Settle(ctx, reservationID, nil); err != nil {
			t.Fatalf("first Settle: %v", err)
		}
		before := snapshotWindow(t, db, providerWindowID)
		if err := lifecycle.Settle(ctx, reservationID, nil); err != nil {
			t.Fatalf("second Settle: %v, want success (no-op)", err)
		}
		if got := snapshotWindow(t, db, providerWindowID); !got.equal(before) {
			t.Fatalf("second Settle changed the window: before=%v after=%v", before, got)
		}
	})

	t.Run("release twice is a no-op the second time", func(t *testing.T) {
		reservationID, providerWindowID, _ := reserveFixture(t, ctx, db, "acct-idem-release", "req-idem-release", "attempt-1")
		if err := lifecycle.Release(ctx, reservationID); err != nil {
			t.Fatalf("first Release: %v", err)
		}
		before := snapshotWindow(t, db, providerWindowID)
		if err := lifecycle.Release(ctx, reservationID); err != nil {
			t.Fatalf("second Release: %v, want success (no-op)", err)
		}
		if got := snapshotWindow(t, db, providerWindowID); !got.equal(before) {
			t.Fatalf("second Release changed the window: before=%v after=%v", before, got)
		}
	})

	t.Run("MarkDispatched twice leaves the first timestamp", func(t *testing.T) {
		reservationID, _, _ := reserveFixture(t, ctx, db, "acct-idem-dispatch", "req-idem-dispatch", "attempt-1")
		first := NewQuotaLifecycleRepo(db, fixedQuotaClock(1000), nil)
		if err := first.MarkDispatched(ctx, reservationID); err != nil {
			t.Fatalf("first MarkDispatched: %v", err)
		}
		var dispatchedAt1 int64
		if err := db.Conn().QueryRow(`SELECT dispatched_at FROM quota_reservations WHERE id = ?`, reservationID).Scan(&dispatchedAt1); err != nil {
			t.Fatalf("read dispatched_at: %v", err)
		}
		second := NewQuotaLifecycleRepo(db, fixedQuotaClock(9999), nil)
		if err := second.MarkDispatched(ctx, reservationID); err != nil {
			t.Fatalf("second MarkDispatched: %v", err)
		}
		var dispatchedAt2 int64
		if err := db.Conn().QueryRow(`SELECT dispatched_at FROM quota_reservations WHERE id = ?`, reservationID).Scan(&dispatchedAt2); err != nil {
			t.Fatalf("read dispatched_at: %v", err)
		}
		if dispatchedAt2 != dispatchedAt1 {
			t.Fatalf("dispatched_at after second call = %d, want unchanged %d", dispatchedAt2, dispatchedAt1)
		}
		if dispatchedAt1 != 1000 {
			t.Fatalf("dispatched_at = %d, want 1000 (the first clock)", dispatchedAt1)
		}
	})

	t.Run("settle after release is rejected as illegal", func(t *testing.T) {
		reservationID, _, _ := reserveFixture(t, ctx, db, "acct-idem-mix", "req-idem-mix", "attempt-1")
		if err := lifecycle.Release(ctx, reservationID); err != nil {
			t.Fatalf("Release: %v", err)
		}
		err := lifecycle.Settle(ctx, reservationID, nil)
		if !errors.Is(err, quota.ErrIllegalTransition) {
			t.Fatalf("Settle after Release error = %v, want ErrIllegalTransition", err)
		}
	})
}

func TestMarkDispatched_OnlyFromReserved(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	lifecycle := NewQuotaLifecycleRepo(db, fixedQuotaClock(1000), nil)

	t.Run("legal from reserved", func(t *testing.T) {
		reservationID, _, _ := reserveFixture(t, ctx, db, "acct-dispatch-ok", "req-dispatch-ok", "attempt-1")
		if err := lifecycle.MarkDispatched(ctx, reservationID); err != nil {
			t.Fatalf("MarkDispatched: %v, want success", err)
		}
		var dispatchedAt sql.NullInt64
		if err := db.Conn().QueryRow(`SELECT dispatched_at FROM quota_reservations WHERE id = ?`, reservationID).Scan(&dispatchedAt); err != nil {
			t.Fatalf("read dispatched_at: %v", err)
		}
		if !dispatchedAt.Valid {
			t.Fatalf("dispatched_at not set")
		}
	})

	t.Run("rejected once terminal", func(t *testing.T) {
		reservationID, _, _ := reserveFixture(t, ctx, db, "acct-dispatch-terminal", "req-dispatch-terminal", "attempt-1")
		if err := lifecycle.Release(ctx, reservationID); err != nil {
			t.Fatalf("Release: %v", err)
		}
		err := lifecycle.MarkDispatched(ctx, reservationID)
		if !errors.Is(err, ErrCannotMarkDispatched) {
			t.Fatalf("MarkDispatched error = %v, want ErrCannotMarkDispatched", err)
		}
	})
}

// TestLifecycle_AuditEmittedAfterCommitWithoutDeadlock is the deadlock
// regression test for the audit-after-commit rule: with a REAL
// AuditEventRepo wired in (not nil), a successful settle both persists
// the state change AND appends the audit row, completing well inside
// reserveTestTimeout. If audit emission were ever moved back inside the
// transaction (sharing the pool's one connection with the open
// transaction), this test would hang until the context timeout instead
// of failing fast.
func TestLifecycle_AuditEmittedAfterCommitWithoutDeadlock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	reservationID, _, _ := reserveFixture(t, ctx, db, "acct-audit", "req-audit", "attempt-1")

	audit := NewAuditEventRepo(db)
	lifecycle := NewQuotaLifecycleRepo(db, fixedQuotaClock(2000), audit)

	start := time.Now()
	if err := lifecycle.Settle(ctx, reservationID, nil); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if elapsed := time.Since(start); elapsed > reserveTestTimeout {
		t.Fatalf("Settle took %v, want well under %v (no deadlock)", elapsed, reserveTestTimeout)
	}

	state, _ := readReservationState(t, db, reservationID)
	if state != "settled" {
		t.Fatalf("reservation state = %q, want settled", state)
	}

	var auditCount int
	if err := db.Conn().QueryRow(
		`SELECT COUNT(*) FROM audit_events WHERE entity_id = ? AND result = 'success'`, reservationID,
	).Scan(&auditCount); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("success audit rows = %d, want 1", auditCount)
	}
}
