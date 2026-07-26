package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

func float64Ptr(v float64) *float64 { return &v }

// reserveTestTimeout bounds every test in this file per the deadlock
// hazard documented in quota_reservations.go / quota_lifecycle.go: with
// SetMaxOpenConns(1), a bug that tries to acquire the pool's single
// connection while a transaction already holds it hangs forever without
// this — the timeout turns that hang into a fast, loud test failure
// instead of a CI job stuck for hours.
const reserveTestTimeout = 10 * time.Second

// seedWindowFull seeds a quota_windows row with full control over every
// column TestReserve_*/TestReserveOnWindow_* need to exercise: nullable
// remaining/limit_value, an explicit reserved amount, and an explicit
// version. Passing *float64 args (nil or non-nil) relies on
// database/sql's standard pointer-dereferencing parameter conversion.
func seedWindowFull(t *testing.T, db *DB, id, accountID, source, unit, windowType, key string, remaining, limitValue *float64, reserved float64, version int64) {
	t.Helper()
	_, err := db.Conn().Exec(
		`INSERT INTO quota_windows
		    (id, account_id, source, unit, window_type, window_key, remaining, limit_value, reserved, version, confidence, freshness_state, observed_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, 'fresh', 0, 0, 0)`,
		id, accountID, source, unit, windowType, key, remaining, limitValue, reserved, version,
	)
	if err != nil {
		t.Fatalf("seed window %s: %v", id, err)
	}
}

func readWindowReservedVersion(t *testing.T, db *DB, windowID string) (float64, int64) {
	t.Helper()
	var reserved float64
	var version int64
	if err := db.Conn().QueryRow(`SELECT reserved, version FROM quota_windows WHERE id = ?`, windowID).Scan(&reserved, &version); err != nil {
		t.Fatalf("read window %s: %v", windowID, err)
	}
	return reserved, version
}

// readWindowReservedVersionOnConn is readWindowReservedVersion's
// conn-scoped twin: it MUST be used by any test that already holds the
// pool's single connection (SetMaxOpenConns(1)) via db.Conn().Conn(ctx),
// since a read through db.Conn() directly would try to acquire a SECOND
// connection from a pool that has none free — deadlocking exactly like
// the P3a-CAPI-001 precedent this batch's constraints warn about.
func readWindowReservedVersionOnConn(ctx context.Context, t *testing.T, conn *sql.Conn, windowID string) (float64, int64) {
	t.Helper()
	var reserved float64
	var version int64
	if err := conn.QueryRowContext(ctx, `SELECT reserved, version FROM quota_windows WHERE id = ?`, windowID).Scan(&reserved, &version); err != nil {
		t.Fatalf("read window %s: %v", windowID, err)
	}
	return reserved, version
}

func countRows(t *testing.T, db *DB, table, whereCol, whereVal string) int {
	t.Helper()
	var count int
	if err := db.Conn().QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE %s = ?`, table, whereCol), whereVal).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func TestReserve_AllOrNothing_CommitsEveryWindow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-aon")
	insertAccount(t, db, "acct-aon", "prov-aon")

	seedWindowFull(t, db, "win-provider-rpm", "acct-aon", "provider_evidence", "requests", "rpm", "provider:rpm", float64Ptr(100), nil, 0, 1)
	seedWindowFull(t, db, "win-ls-concurrency", "acct-aon", "local_safety", "concurrency", "concurrency", "local:concurrency", nil, float64Ptr(1), 0, 1)
	seedWindowFull(t, db, "win-ls-consumption", "acct-aon", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 0, 1)

	allocations, err := quota.Estimate(quota.EstimateInput{}, quota.DefaultEstimatePolicy())
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}

	repo := NewQuotaReservationRepo(db, fixedQuotaClock(1000))
	result, err := repo.Reserve(ctx, ReserveParams{AccountID: "acct-aon", RequestID: "req-aon", AttemptID: "attempt-1", Allocations: allocations})
	if err != nil {
		t.Fatalf("Reserve: %v, want success", err)
	}
	if result.Idempotent {
		t.Fatalf("Idempotent = true on first call, want false")
	}
	if len(result.Debits) != 3 {
		t.Fatalf("len(Debits) = %d, want 3 (provider-rpm, ls-concurrency, ls-consumption)", len(result.Debits))
	}

	for _, id := range []string{"win-provider-rpm", "win-ls-concurrency", "win-ls-consumption"} {
		reserved, version := readWindowReservedVersion(t, db, id)
		if reserved != 1 {
			t.Fatalf("window %s reserved = %v, want 1", id, reserved)
		}
		if version != 2 {
			t.Fatalf("window %s version = %v, want 2 (incremented by exactly 1)", id, version)
		}
	}

	var state string
	var dispatchedAt sql.NullInt64
	var expiresAt, createdAt int64
	if err := db.Conn().QueryRow(
		`SELECT state, dispatched_at, expires_at, created_at FROM quota_reservations WHERE id = ?`, result.ReservationID,
	).Scan(&state, &dispatchedAt, &expiresAt, &createdAt); err != nil {
		t.Fatalf("read reservation: %v", err)
	}
	if state != "reserved" {
		t.Fatalf("reservation state = %q, want reserved", state)
	}
	if dispatchedAt.Valid {
		t.Fatalf("dispatched_at = %v, want NULL", dispatchedAt.Int64)
	}
	if expiresAt-createdAt != int64(quota.DefaultProcessingDeadline.Seconds()) {
		t.Fatalf("expires_at - created_at = %d, want %d", expiresAt-createdAt, int64(quota.DefaultProcessingDeadline.Seconds()))
	}

	rows, err := db.Conn().Query(`SELECT window_id, state, actual_cost FROM quota_reservation_allocations WHERE reservation_id = ?`, result.ReservationID)
	if err != nil {
		t.Fatalf("query allocations: %v", err)
	}
	defer func() { _ = rows.Close() }()
	count := 0
	for rows.Next() {
		var windowID, allocState string
		var actualCost sql.NullFloat64
		if err := rows.Scan(&windowID, &allocState, &actualCost); err != nil {
			t.Fatalf("scan allocation: %v", err)
		}
		count++
		if allocState != "reserved" {
			t.Fatalf("allocation for %s state = %q, want reserved", windowID, allocState)
		}
		if actualCost.Valid {
			t.Fatalf("allocation for %s actual_cost = %v, want NULL", windowID, actualCost.Float64)
		}
	}
	if count != 3 {
		t.Fatalf("allocation row count = %d, want 3 (one per debit)", count)
	}
}

func TestReserve_AnyWindowShort_RollsBackEverything(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-short")
	insertAccount(t, db, "acct-short", "prov-short")

	// Ids are deliberately chosen so the succeeding window sorts and is
	// therefore processed BEFORE the failing one: this proves the
	// transaction genuinely rolls back a real prior write, not merely
	// that it never reached the failing window.
	seedWindowFull(t, db, "win-a-provider-requests", "acct-short", "provider_evidence", "requests", "rpm", "provider:rpm", float64Ptr(1000), nil, 0, 1)
	seedWindowFull(t, db, "win-b-ls-concurrency", "acct-short", "local_safety", "concurrency", "concurrency", "local:concurrency", nil, float64Ptr(1), 1, 5)

	allocations := []quota.Allocation{
		{Unit: quota.UnitRequests, Cost: 1, Source: quota.EstimateSourceFromRequest},
		{Unit: quota.UnitConcurrency, Cost: 1, Source: quota.EstimateSourceFromRequest},
	}

	repo := NewQuotaReservationRepo(db, fixedQuotaClock(1000))
	_, err := repo.Reserve(ctx, ReserveParams{AccountID: "acct-short", RequestID: "req-short", AttemptID: "attempt-1", Allocations: allocations})
	if !errors.Is(err, ErrReservationRejected) {
		t.Fatalf("Reserve error = %v, want ErrReservationRejected", err)
	}

	reserved, version := readWindowReservedVersion(t, db, "win-a-provider-requests")
	if reserved != 0 || version != 1 {
		t.Fatalf("win-a-provider-requests (reserved=%v version=%v), want (0,1) unchanged — the rollback must undo its earlier successful debit", reserved, version)
	}
	reservedB, versionB := readWindowReservedVersion(t, db, "win-b-ls-concurrency")
	if reservedB != 1 || versionB != 5 {
		t.Fatalf("win-b-ls-concurrency (reserved=%v version=%v), want (1,5) unchanged", reservedB, versionB)
	}

	if n := countRows(t, db, "quota_reservations", "account_id", "acct-short"); n != 0 {
		t.Fatalf("quota_reservations rows = %d, want 0", n)
	}
	var allocCount int
	if err := db.Conn().QueryRow(
		`SELECT COUNT(*) FROM quota_reservation_allocations WHERE window_id IN (?, ?)`,
		"win-a-provider-requests", "win-b-ls-concurrency",
	).Scan(&allocCount); err != nil {
		t.Fatalf("count allocations: %v", err)
	}
	if allocCount != 0 {
		t.Fatalf("quota_reservation_allocations rows = %d, want 0", allocCount)
	}
}

func TestReserve_IsIdempotentPerRequestAttempt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-idem")
	insertAccount(t, db, "acct-idem", "prov-idem")
	seedWindowFull(t, db, "win-idem", "acct-idem", "local_safety", "concurrency", "concurrency", "local:concurrency", nil, float64Ptr(5), 0, 1)

	allocations := []quota.Allocation{{Unit: quota.UnitConcurrency, Cost: 1, Source: quota.EstimateSourceFromRequest}}
	repo := NewQuotaReservationRepo(db, fixedQuotaClock(1000))
	params := ReserveParams{AccountID: "acct-idem", RequestID: "req-idem", AttemptID: "attempt-1", Allocations: allocations}

	first, err := repo.Reserve(ctx, params)
	if err != nil {
		t.Fatalf("first Reserve: %v", err)
	}

	second, err := repo.Reserve(ctx, params)
	if err != nil {
		t.Fatalf("second Reserve: %v", err)
	}
	if !second.Idempotent {
		t.Fatalf("second call Idempotent = false, want true")
	}
	if second.ReservationID != first.ReservationID {
		t.Fatalf("second call ReservationID = %q, want %q (same id)", second.ReservationID, first.ReservationID)
	}
	reserved, version := readWindowReservedVersion(t, db, "win-idem")
	if reserved != 1 || version != 2 {
		t.Fatalf("after second call window = (reserved=%v version=%v), want (1,2) unchanged by the repeat", reserved, version)
	}
	if n := countRows(t, db, "quota_reservations", "account_id", "acct-idem"); n != 1 {
		t.Fatalf("quota_reservations rows = %d, want exactly 1", n)
	}

	// Drive the reservation to a terminal state directly, then prove a
	// repeat Reserve still does not re-debit and does not resurrect it.
	if _, err := db.Conn().Exec(`UPDATE quota_reservations SET state = 'settled' WHERE id = ?`, first.ReservationID); err != nil {
		t.Fatalf("force-settle reservation: %v", err)
	}

	third, err := repo.Reserve(ctx, params)
	if err != nil {
		t.Fatalf("third Reserve: %v", err)
	}
	if !third.Idempotent || third.ReservationID != first.ReservationID {
		t.Fatalf("third call = %+v, want Idempotent=true ReservationID=%q", third, first.ReservationID)
	}
	reserved, version = readWindowReservedVersion(t, db, "win-idem")
	if reserved != 1 || version != 2 {
		t.Fatalf("after third call (post-settle) window = (reserved=%v version=%v), want (1,2) still unchanged", reserved, version)
	}
	var stateAfter string
	if err := db.Conn().QueryRow(`SELECT state FROM quota_reservations WHERE id = ?`, first.ReservationID).Scan(&stateAfter); err != nil {
		t.Fatalf("read reservation state: %v", err)
	}
	if stateAfter != "settled" {
		t.Fatalf("reservation state after third call = %q, want settled (never resurrected to reserved)", stateAfter)
	}
}

func TestReserve_UnknownProviderQuotaStillNeedsLocalSafetyHeadroom(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-unknown")
	insertAccount(t, db, "acct-unknown", "prov-unknown")

	seedWindowFull(t, db, "win-provider-unknown", "acct-unknown", "provider_evidence", "requests", "rpm", "provider:rpm", nil, nil, 0, 1)
	seedWindowFull(t, db, "win-ls-concurrency", "acct-unknown", "local_safety", "concurrency", "concurrency", "local:concurrency", nil, float64Ptr(1), 0, 1)
	seedWindowFull(t, db, "win-ls-consumption", "acct-unknown", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(50), 0, 1)

	allocations := []quota.Allocation{
		{Unit: quota.UnitRequests, Cost: 1, Source: quota.EstimateSourceFromRequest},
		{Unit: quota.UnitConcurrency, Cost: 1, Source: quota.EstimateSourceFromRequest},
	}
	repo := NewQuotaReservationRepo(db, fixedQuotaClock(1000))

	result, err := repo.Reserve(ctx, ReserveParams{AccountID: "acct-unknown", RequestID: "req-unknown", AttemptID: "attempt-1", Allocations: allocations})
	if err != nil {
		t.Fatalf("Reserve: %v, want success (unknown provider quota must not block)", err)
	}
	reservedUnknown, versionUnknown := readWindowReservedVersion(t, db, "win-provider-unknown")
	if reservedUnknown != 0 || versionUnknown != 1 {
		t.Fatalf("win-provider-unknown = (reserved=%v version=%v), want (0,1) untouched", reservedUnknown, versionUnknown)
	}
	for _, d := range result.Debits {
		if d.WindowID == "win-provider-unknown" {
			t.Fatalf("unknown-capacity window was debited: %+v", result.Debits)
		}
	}

	// Now exhaust the local-safety concurrency window and prove the SAME
	// account is rejected on a new attempt: unknown provider quota is
	// bounded by local safety, never unlimited.
	if _, err := db.Conn().Exec(`UPDATE quota_windows SET reserved = 1 WHERE id = ?`, "win-ls-concurrency"); err != nil {
		t.Fatalf("exhaust concurrency window: %v", err)
	}
	_, err = repo.Reserve(ctx, ReserveParams{AccountID: "acct-unknown", RequestID: "req-unknown", AttemptID: "attempt-2", Allocations: allocations})
	if !errors.Is(err, ErrReservationRejected) {
		t.Fatalf("second Reserve error = %v, want ErrReservationRejected once local-safety concurrency is exhausted", err)
	}
}

func TestReserve_NoWindowsAtAllIsRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-none")
	insertAccount(t, db, "acct-none", "prov-none")

	allocations := []quota.Allocation{{Unit: quota.UnitRequests, Cost: 1, Source: quota.EstimateSourceFromRequest}}
	repo := NewQuotaReservationRepo(db, fixedQuotaClock(1000))

	_, err := repo.Reserve(ctx, ReserveParams{AccountID: "acct-none", RequestID: "req-none", AttemptID: "attempt-1", Allocations: allocations})
	if !errors.Is(err, quota.ErrNoApplicableWindow) {
		t.Fatalf("Reserve error = %v, want quota.ErrNoApplicableWindow", err)
	}
	if n := countRows(t, db, "quota_reservations", "account_id", "acct-none"); n != 0 {
		t.Fatalf("quota_reservations rows = %d, want 0", n)
	}
}

// TestReserveOnWindow_VersionGuardBites drives reserveOnWindow directly.
// This is the only place the "AND version = ?" clause is independently
// observable: with one pooled connection nothing can interleave inside
// Reserve itself (every caller serializes through the pool), so without
// this test that clause's behavior is unprovable via Reserve.
func TestReserveOnWindow_VersionGuardBites(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-guard")
	insertAccount(t, db, "acct-guard", "prov-guard")
	seedWindowFull(t, db, "win-guard", "acct-guard", "local_safety", "concurrency", "concurrency", "local:concurrency", nil, float64Ptr(10), 0, 3)

	conn, err := db.Conn().Conn(ctx)
	if err != nil {
		t.Fatalf("acquire conn: %v", err)
	}
	defer func() { _ = conn.Close() }()

	t.Run("current version and sufficient headroom affects 1 row", func(t *testing.T) {
		affected, err := reserveOnWindow(ctx, conn, "win-guard", 3, 2, 1000)
		if err != nil {
			t.Fatalf("reserveOnWindow: %v", err)
		}
		if !affected {
			t.Fatalf("affected = false, want true")
		}
		reserved, version := readWindowReservedVersionOnConn(ctx, t, conn, "win-guard")
		if reserved != 2 || version != 4 {
			t.Fatalf("after success: (reserved=%v version=%v), want (2,4)", reserved, version)
		}
	})

	t.Run("stale expectedVersion affects 0 rows and leaves reserved unchanged", func(t *testing.T) {
		// Window is now at version 4, reserved 2. Use a stale version (3).
		affected, err := reserveOnWindow(ctx, conn, "win-guard", 3, 1, 2000)
		if err != nil {
			t.Fatalf("reserveOnWindow: %v", err)
		}
		if affected {
			t.Fatalf("affected = true with a stale expectedVersion, want false")
		}
		reserved, version := readWindowReservedVersionOnConn(ctx, t, conn, "win-guard")
		if reserved != 2 || version != 4 {
			t.Fatalf("after stale-version attempt: (reserved=%v version=%v), want (2,4) unchanged", reserved, version)
		}
	})

	t.Run("current version but cost exceeding headroom affects 0 rows", func(t *testing.T) {
		// headroom = 10 - 2 = 8; request cost 100 exceeds it.
		affected, err := reserveOnWindow(ctx, conn, "win-guard", 4, 100, 3000)
		if err != nil {
			t.Fatalf("reserveOnWindow: %v", err)
		}
		if affected {
			t.Fatalf("affected = true with cost exceeding headroom, want false")
		}
		reserved, version := readWindowReservedVersionOnConn(ctx, t, conn, "win-guard")
		if reserved != 2 || version != 4 {
			t.Fatalf("after over-cost attempt: (reserved=%v version=%v), want (2,4) unchanged", reserved, version)
		}
	})
}

// TestReserve_ConcurrentCallersNeverOvercommit proves the OUTCOME (no
// overcommit, no partial debit) under real concurrent callers.
// SetMaxOpenConns(1) serializes them at the connection-pool level, so
// this does not exercise interleaved SQL execution — that is
// TestReserveOnWindow_VersionGuardBites's job — but it does prove that
// repeated real Reserve calls against a shared, capacity-limited window
// never let more attempts through than the window can hold.
func TestReserve_ConcurrentCallersNeverOvercommit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), reserveTestTimeout)
	defer cancel()

	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-concurrent")
	insertAccount(t, db, "acct-concurrent", "prov-concurrent")
	seedWindowFull(t, db, "win-concurrent", "acct-concurrent", "local_safety", "requests", "estimated_consumption", "rolling:3600s", nil, float64Ptr(3), 0, 1)

	repo := NewQuotaReservationRepo(db, fixedQuotaClock(1000))
	allocations := []quota.Allocation{{Unit: quota.UnitRequests, Cost: 1, Source: quota.EstimateSourceFromRequest}}

	const callers = 10
	var wg sync.WaitGroup
	results := make([]error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := repo.Reserve(ctx, ReserveParams{
				AccountID:   "acct-concurrent",
				RequestID:   fmt.Sprintf("req-concurrent-%d", i),
				AttemptID:   fmt.Sprintf("attempt-%d", i),
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
		case errors.Is(err, ErrReservationRejected):
			rejected++
		default:
			t.Fatalf("unexpected Reserve error: %v", err)
		}
	}
	if succeeded != 3 {
		t.Fatalf("succeeded = %d, want exactly 3", succeeded)
	}
	if rejected != 7 {
		t.Fatalf("rejected = %d, want exactly 7", rejected)
	}

	reserved, _ := readWindowReservedVersion(t, db, "win-concurrent")
	if reserved != 3 {
		t.Fatalf("final reserved = %v, want exactly 3 (never exceeds capacity)", reserved)
	}
	if n := countRows(t, db, "quota_reservations", "account_id", "acct-concurrent"); n != 3 {
		t.Fatalf("quota_reservations rows = %d, want exactly 3", n)
	}
}
