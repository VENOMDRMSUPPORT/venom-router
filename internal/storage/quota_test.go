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
