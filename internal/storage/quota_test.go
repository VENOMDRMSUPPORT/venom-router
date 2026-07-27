package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

func fixedQuotaClock(unix int64) func() time.Time {
	return func() time.Time { return time.Unix(unix, 0) }
}

// TestEnsureLocalSafetyWindows_CreatesMandatoryWindows proves a
// zero-window account gets exactly the two mandatory local-safety
// windows (02 §3), with reserved=0, version=1, NULL remaining, and
// source='local_safety'.
func TestEnsureLocalSafetyWindows_CreatesMandatoryWindows(t *testing.T) {
	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-ls-create")
	insertAccount(t, db, "acct-ls-create", "prov-ls-create")

	specs, err := quota.DefaultLocalSafetyPolicy().MandatoryWindows()
	if err != nil {
		t.Fatalf("MandatoryWindows(): %v", err)
	}

	repo := NewQuotaWindowRepo(db, nil, fixedQuotaClock(1000))
	if err := repo.EnsureLocalSafetyWindows(context.Background(), "acct-ls-create", specs); err != nil {
		t.Fatalf("EnsureLocalSafetyWindows: %v, want success", err)
	}

	rows, err := db.Conn().Query(
		`SELECT source, reserved, version, remaining FROM quota_windows WHERE account_id = ? ORDER BY window_type`,
		"acct-ls-create",
	)
	if err != nil {
		t.Fatalf("query quota_windows: %v", err)
	}
	defer func() { _ = rows.Close() }()

	count := 0
	for rows.Next() {
		var source string
		var reserved float64
		var version int64
		var remaining *float64
		if err := rows.Scan(&source, &reserved, &version, &remaining); err != nil {
			t.Fatalf("scan: %v", err)
		}
		count++
		if source != "local_safety" {
			t.Fatalf("source = %q, want local_safety", source)
		}
		if reserved != 0 {
			t.Fatalf("reserved = %v, want 0", reserved)
		}
		if version != 1 {
			t.Fatalf("version = %v, want 1", version)
		}
		if remaining != nil {
			t.Fatalf("remaining = %v, want NULL (unknown)", *remaining)
		}
	}
	if count != 2 {
		t.Fatalf("row count = %d, want exactly 2", count)
	}
}

// TestEnsureLocalSafetyWindows_IsIdempotent proves calling
// EnsureLocalSafetyWindows a second time never overwrites an existing
// row's reserved/limit_value/version/created_at, and never creates a
// duplicate.
func TestEnsureLocalSafetyWindows_IsIdempotent(t *testing.T) {
	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-ls-idem")
	insertAccount(t, db, "acct-ls-idem", "prov-ls-idem")

	specs, err := quota.DefaultLocalSafetyPolicy().MandatoryWindows()
	if err != nil {
		t.Fatalf("MandatoryWindows(): %v", err)
	}

	repo := NewQuotaWindowRepo(db, nil, fixedQuotaClock(1000))
	if err := repo.EnsureLocalSafetyWindows(context.Background(), "acct-ls-idem", specs); err != nil {
		t.Fatalf("first EnsureLocalSafetyWindows: %v", err)
	}

	// Mutate one row with raw SQL to prove the second ensure leaves it
	// completely untouched.
	if _, err := db.Conn().Exec(
		`UPDATE quota_windows SET reserved = 3, limit_value = 9, version = 7 WHERE account_id = ? AND window_type = 'concurrency'`,
		"acct-ls-idem",
	); err != nil {
		t.Fatalf("mutate row: %v", err)
	}
	var createdAtBefore int64
	if err := db.Conn().QueryRow(
		`SELECT created_at FROM quota_windows WHERE account_id = ? AND window_type = 'concurrency'`, "acct-ls-idem",
	).Scan(&createdAtBefore); err != nil {
		t.Fatalf("read created_at before: %v", err)
	}

	if err := repo.EnsureLocalSafetyWindows(context.Background(), "acct-ls-idem", specs); err != nil {
		t.Fatalf("second EnsureLocalSafetyWindows: %v, want success (no-op)", err)
	}

	var reserved, limitValue float64
	var version, createdAtAfter int64
	if err := db.Conn().QueryRow(
		`SELECT reserved, limit_value, version, created_at FROM quota_windows WHERE account_id = ? AND window_type = 'concurrency'`,
		"acct-ls-idem",
	).Scan(&reserved, &limitValue, &version, &createdAtAfter); err != nil {
		t.Fatalf("read mutated row after second ensure: %v", err)
	}
	if reserved != 3 || limitValue != 9 || version != 7 || createdAtAfter != createdAtBefore {
		t.Fatalf("row after second ensure = (reserved=%v limit_value=%v version=%v created_at=%v), want byte-for-byte unchanged (3, 9, 7, %v)",
			reserved, limitValue, version, createdAtAfter, createdAtBefore)
	}

	var count int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM quota_windows WHERE account_id = ?`, "acct-ls-idem").Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 2 {
		t.Fatalf("row count after second ensure = %d, want still exactly 2", count)
	}
}

// TestEnsureLocalSafetyWindows_CoversAccountsThatPredateQuota encodes the
// governor's spec decision: EnsureLocalSafetyWindows is an idempotent
// ENSURE, not a create-on-connect event, so it also bounds accounts
// enrolled before quota existed (the P2b-era account shape — zero
// windows).
func TestEnsureLocalSafetyWindows_CoversAccountsThatPredateQuota(t *testing.T) {
	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-ls-predate")
	insertAccount(t, db, "acct-ls-predate", "prov-ls-predate")

	var countBefore int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM quota_windows WHERE account_id = ?`, "acct-ls-predate").Scan(&countBefore); err != nil {
		t.Fatalf("count rows before: %v", err)
	}
	if countBefore != 0 {
		t.Fatalf("row count before ensure = %d, want 0 (this is the pre-existing P2b-era account shape)", countBefore)
	}

	specs, err := quota.DefaultLocalSafetyPolicy().MandatoryWindows()
	if err != nil {
		t.Fatalf("MandatoryWindows(): %v", err)
	}
	repo := NewQuotaWindowRepo(db, nil, fixedQuotaClock(1000))
	if err := repo.EnsureLocalSafetyWindows(context.Background(), "acct-ls-predate", specs); err != nil {
		t.Fatalf("EnsureLocalSafetyWindows: %v, want success", err)
	}

	var countAfter int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM quota_windows WHERE account_id = ?`, "acct-ls-predate").Scan(&countAfter); err != nil {
		t.Fatalf("count rows after: %v", err)
	}
	if countAfter != 2 {
		t.Fatalf("row count after ensure = %d, want 2 (account is now bounded)", countAfter)
	}
}

// TestEnsureLocalSafetyWindows_RejectsNonLocalSafetySpec proves the
// all-or-nothing validation: a slice whose second element is not
// local_safety is rejected and leaves ZERO rows written.
func TestEnsureLocalSafetyWindows_RejectsNonLocalSafetySpec(t *testing.T) {
	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-ls-reject")
	insertAccount(t, db, "acct-ls-reject", "prov-ls-reject")

	specs, err := quota.DefaultLocalSafetyPolicy().MandatoryWindows()
	if err != nil {
		t.Fatalf("MandatoryWindows(): %v", err)
	}
	specs[1].Source = quota.SourceProviderEvidence

	repo := NewQuotaWindowRepo(db, nil, fixedQuotaClock(1000))
	err = repo.EnsureLocalSafetyWindows(context.Background(), "acct-ls-reject", specs)
	if !errors.Is(err, ErrInvalidLocalSafetySpec) {
		t.Fatalf("EnsureLocalSafetyWindows error = %v, want ErrInvalidLocalSafetySpec", err)
	}

	var count int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM quota_windows WHERE account_id = ?`, "acct-ls-reject").Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("row count after rejected call = %d, want 0 (all-or-nothing)", count)
	}
}

// TestQuotaWindowRepo_ListByAccount_IsDeterministicAndScoped proves
// ListByAccount is scoped to one account and returns the canonical
// (source, unit, window_type, window_key) order across repeated calls.
//
// The two mandatory windows are seeded in REVERSE of that canonical
// order deliberately (estimated_consumption, unit "requests", inserted
// before concurrency, unit "concurrency"): "concurrency" sorts before
// "requests", but it is also physically inserted first when seeded via
// MandatoryWindows()'s natural order, so SQLite's incidental rowid order
// would coincidentally match the canonical order and mask a missing
// ORDER BY. Seeding in reverse insertion order makes a dropped ORDER BY
// genuinely observable.
func TestQuotaWindowRepo_ListByAccount_IsDeterministicAndScoped(t *testing.T) {
	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-ls-list")
	insertAccount(t, db, "acct-ls-list-a", "prov-ls-list")
	insertAccount(t, db, "acct-ls-list-b", "prov-ls-list")

	specsA, err := quota.DefaultLocalSafetyPolicy().MandatoryWindows()
	if err != nil {
		t.Fatalf("MandatoryWindows(): %v", err)
	}
	if len(specsA) != 2 || specsA[0].WindowType != "concurrency" || specsA[1].WindowType != "estimated_consumption" {
		t.Fatalf("MandatoryWindows() = %+v, want [concurrency, estimated_consumption]", specsA)
	}
	specsB, err := quota.DefaultLocalSafetyPolicy().MandatoryWindows()
	if err != nil {
		t.Fatalf("MandatoryWindows(): %v", err)
	}

	repo := NewQuotaWindowRepo(db, nil, fixedQuotaClock(1000))
	// Reversed relative to MandatoryWindows()'s order and relative to the
	// canonical unit order ("concurrency" < "requests"): estimated_consumption
	// (unit=requests) is physically inserted first.
	if err := repo.EnsureLocalSafetyWindows(context.Background(), "acct-ls-list-a", []quota.WindowSpec{specsA[1]}); err != nil {
		t.Fatalf("ensure a (estimated_consumption first): %v", err)
	}
	if err := repo.EnsureLocalSafetyWindows(context.Background(), "acct-ls-list-a", []quota.WindowSpec{specsA[0]}); err != nil {
		t.Fatalf("ensure a (concurrency second): %v", err)
	}
	if err := repo.EnsureLocalSafetyWindows(context.Background(), "acct-ls-list-b", specsB); err != nil {
		t.Fatalf("ensure b: %v", err)
	}

	first, err := repo.ListByAccount(context.Background(), "acct-ls-list-a")
	if err != nil {
		t.Fatalf("ListByAccount: %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("len(first) = %d, want 2 (scoped to acct-ls-list-a only)", len(first))
	}
	for _, w := range first {
		if w.AccountID != "acct-ls-list-a" {
			t.Fatalf("window AccountID = %q, want acct-ls-list-a (list must not leak the other account's rows)", w.AccountID)
		}
	}
	// Canonical order is by unit: "concurrency" < "requests", i.e. the
	// concurrency window first, even though it was physically inserted
	// second above.
	if first[0].WindowType != "concurrency" || first[1].WindowType != "estimated_consumption" {
		t.Fatalf("order = [%s, %s], want [concurrency, estimated_consumption] (canonical order, not insertion order)",
			first[0].WindowType, first[1].WindowType)
	}

	for i := 0; i < 5; i++ {
		again, err := repo.ListByAccount(context.Background(), "acct-ls-list-a")
		if err != nil {
			t.Fatalf("ListByAccount repeat %d: %v", i, err)
		}
		if len(again) != len(first) {
			t.Fatalf("repeat %d: len = %d, want %d", i, len(again), len(first))
		}
		for j := range again {
			if again[j].WindowType != first[j].WindowType || again[j].Key != first[j].Key {
				t.Fatalf("repeat %d: order[%d] = (%q,%q), want (%q,%q) (must be deterministic)",
					i, j, again[j].WindowType, again[j].Key, first[j].WindowType, first[j].Key)
			}
		}
	}
}

// rawQuotaWindowSpec is the full column set a test needs to control when
// seeding a quota_windows row directly (bypassing EnsureLocalSafetyWindows,
// which only ever writes local_safety rows). Pointer fields are nullable,
// mirroring quota.Window's own nullable-numeric fields.
type rawQuotaWindowSpec struct {
	id         string
	accountID  string
	source     string
	unit       string
	windowType string
	windowKey  string
	used       *float64
	remaining  *float64
	total      *float64
	reserved   float64
	limitValue *float64
	resetAt    *int64
	confidence float64
	freshness  string
	observedAt int64
}

func insertRawQuotaWindow(t *testing.T, db *DB, spec rawQuotaWindowSpec) {
	t.Helper()
	confidence := spec.confidence
	if confidence == 0 {
		confidence = 1.0
	}
	freshness := spec.freshness
	if freshness == "" {
		freshness = "fresh"
	}
	if _, err := db.Conn().Exec(
		`INSERT INTO quota_windows
		    (id, account_id, source, unit, window_type, window_key, duration_seconds,
		     used, remaining, total, reserved, limit_value, reset_at, version, confidence,
		     freshness_state, observed_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?)`,
		spec.id, spec.accountID, spec.source, spec.unit, spec.windowType, spec.windowKey,
		spec.used, spec.remaining, spec.total, spec.reserved, spec.limitValue, spec.resetAt,
		confidence, freshness, spec.observedAt, spec.observedAt, spec.observedAt,
	); err != nil {
		t.Fatalf("insert raw quota window %s: %v", spec.id, err)
	}
}

// TestListByAccounts_BatchesAndScopes proves ONE ListByAccounts call
// returns every requested account's windows, correctly keyed, with no
// cross-account leakage: a third account with zero windows is simply
// absent from the returned map.
func TestListByAccounts_BatchesAndScopes(t *testing.T) {
	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-batch")
	insertAccount(t, db, "acct-batch-a", "prov-batch")
	insertAccount(t, db, "acct-batch-b", "prov-batch")
	insertAccount(t, db, "acct-batch-c", "prov-batch")
	// acct-batch-other is NEVER passed to ListByAccounts below — its window
	// exists purely to prove the account filter is real: a mutation that
	// drops/bypasses the WHERE account_id IN (...) clause (e.g. "OR 1=1")
	// would leak this window into the result set even though the DB
	// otherwise contains nothing outside the requested three accounts.
	insertAccount(t, db, "acct-batch-other", "prov-batch")

	insertRawQuotaWindow(t, db, rawQuotaWindowSpec{
		id: "w-a-1", accountID: "acct-batch-a", source: "provider_evidence", unit: "requests",
		windowType: "rolling", windowKey: "provider:daily", reserved: 0, observedAt: 1000,
	})
	insertRawQuotaWindow(t, db, rawQuotaWindowSpec{
		id: "w-b-1", accountID: "acct-batch-b", source: "local_safety", unit: "concurrency",
		windowType: "concurrency", windowKey: "local:concurrency", reserved: 0, observedAt: 1000,
	})
	insertRawQuotaWindow(t, db, rawQuotaWindowSpec{
		id: "w-other-1", accountID: "acct-batch-other", source: "provider_evidence", unit: "requests",
		windowType: "rolling", windowKey: "provider:daily", reserved: 0, observedAt: 1000,
	})
	// acct-batch-c intentionally has zero windows.

	repo := NewQuotaWindowRepo(db, nil, fixedQuotaClock(2000))
	got, err := repo.ListByAccounts(context.Background(), []string{"acct-batch-a", "acct-batch-b", "acct-batch-c"})
	if err != nil {
		t.Fatalf("ListByAccounts: %v", err)
	}

	if len(got["acct-batch-a"]) != 1 || got["acct-batch-a"][0].AccountID != "acct-batch-a" {
		t.Fatalf("acct-batch-a windows = %+v, want exactly 1 window scoped to acct-batch-a", got["acct-batch-a"])
	}
	if len(got["acct-batch-b"]) != 1 || got["acct-batch-b"][0].AccountID != "acct-batch-b" {
		t.Fatalf("acct-batch-b windows = %+v, want exactly 1 window scoped to acct-batch-b", got["acct-batch-b"])
	}
	if windows, ok := got["acct-batch-c"]; ok && len(windows) != 0 {
		t.Fatalf("acct-batch-c windows = %+v, want absent or empty (no windows leaked in)", windows)
	}
	if windows, ok := got["acct-batch-other"]; ok && len(windows) != 0 {
		t.Fatalf("acct-batch-other windows = %+v, want absent (it was never requested; the account filter must be real)", windows)
	}
	if len(got) != 2 {
		t.Fatalf("map has %d keys = %+v, want exactly 2 (only requested accounts with windows)", len(got), got)
	}
}

// TestListByAccounts_EmptyInput proves an empty accountIDs slice returns an
// empty map and nil error, with no query issued.
func TestListByAccounts_EmptyInput(t *testing.T) {
	db := migratedCatalogRepoDB(t)
	repo := NewQuotaWindowRepo(db, nil, fixedQuotaClock(1000))

	got, err := repo.ListByAccounts(context.Background(), []string{})
	if err != nil {
		t.Fatalf("ListByAccounts(empty): %v, want nil error", err)
	}
	if got == nil {
		t.Fatalf("ListByAccounts(empty) = nil map, want an empty non-nil map")
	}
	if len(got) != 0 {
		t.Fatalf("ListByAccounts(empty) = %+v, want empty map", got)
	}
}

// TestListByAccounts_OrdersCanonicallyWithinAnAccount seeds one account's
// windows in REVERSE of the canonical (source, unit, window_type,
// window_key) order and proves ListByAccounts still returns them in
// canonical order — SQLite's incidental insertion-order has masked a
// missing ORDER BY before in this project (see
// TestQuotaWindowRepo_ListByAccount_IsDeterministicAndScoped's own doc
// comment), so this test seeds deliberately out of order rather than
// relying on natural insertion order to coincide with canonical order.
func TestListByAccounts_OrdersCanonicallyWithinAnAccount(t *testing.T) {
	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-order")
	insertAccount(t, db, "acct-order", "prov-order")

	// Canonical order by (source, unit, window_type, window_key):
	// local_safety < owner_override < provider_evidence (lexical), and
	// within local_safety, "concurrency" < "requests" by unit. Seed in the
	// reverse of that.
	insertRawQuotaWindow(t, db, rawQuotaWindowSpec{
		id: "w-order-3", accountID: "acct-order", source: "provider_evidence", unit: "tokens",
		windowType: "rolling", windowKey: "provider:z", reserved: 0, observedAt: 1000,
	})
	insertRawQuotaWindow(t, db, rawQuotaWindowSpec{
		id: "w-order-2", accountID: "acct-order", source: "owner_override", unit: "requests",
		windowType: "rolling", windowKey: "owner:override", reserved: 0, observedAt: 1000,
	})
	insertRawQuotaWindow(t, db, rawQuotaWindowSpec{
		id: "w-order-1a", accountID: "acct-order", source: "local_safety", unit: "requests",
		windowType: "estimated_consumption", windowKey: "local:requests", reserved: 0, observedAt: 1000,
	})
	insertRawQuotaWindow(t, db, rawQuotaWindowSpec{
		id: "w-order-1b", accountID: "acct-order", source: "local_safety", unit: "concurrency",
		windowType: "concurrency", windowKey: "local:concurrency", reserved: 0, observedAt: 1000,
	})

	repo := NewQuotaWindowRepo(db, nil, fixedQuotaClock(2000))
	got, err := repo.ListByAccounts(context.Background(), []string{"acct-order"})
	if err != nil {
		t.Fatalf("ListByAccounts: %v", err)
	}
	windows := got["acct-order"]
	if len(windows) != 4 {
		t.Fatalf("len(windows) = %d, want 4", len(windows))
	}
	wantOrder := []string{"w-order-1b", "w-order-1a", "w-order-2", "w-order-3"}
	for i, w := range windows {
		if w.ID != wantOrder[i] {
			gotIDs := make([]string, len(windows))
			for j, ww := range windows {
				gotIDs[j] = ww.ID
			}
			t.Fatalf("order = %v, want %v (canonical source,unit,window_type,window_key order)", gotIDs, wantOrder)
		}
	}
}

// TestListByAccounts_PreservesUnknowns proves a window with NULL
// used/remaining/total comes back with nil pointers, not zeros — the
// "unknown is never zero" invariant (02 §3) at the storage layer.
func TestListByAccounts_PreservesUnknowns(t *testing.T) {
	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-unknown")
	insertAccount(t, db, "acct-unknown", "prov-unknown")

	insertRawQuotaWindow(t, db, rawQuotaWindowSpec{
		id: "w-unknown-1", accountID: "acct-unknown", source: "provider_evidence", unit: "requests",
		windowType: "rolling", windowKey: "provider:daily",
		used: nil, remaining: nil, total: nil, reserved: 0, freshness: "unknown", observedAt: 1000,
	})

	repo := NewQuotaWindowRepo(db, nil, fixedQuotaClock(2000))
	got, err := repo.ListByAccounts(context.Background(), []string{"acct-unknown"})
	if err != nil {
		t.Fatalf("ListByAccounts: %v", err)
	}
	windows := got["acct-unknown"]
	if len(windows) != 1 {
		t.Fatalf("len(windows) = %d, want 1", len(windows))
	}
	w := windows[0]
	if w.Used != nil {
		t.Fatalf("Used = %v, want nil (unknown, never 0)", *w.Used)
	}
	if w.Remaining != nil {
		t.Fatalf("Remaining = %v, want nil (unknown, never 0)", *w.Remaining)
	}
	if w.Total != nil {
		t.Fatalf("Total = %v, want nil (unknown, never 0)", *w.Total)
	}
}
