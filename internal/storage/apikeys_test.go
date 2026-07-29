package storage

import (
	"context"
	"testing"
	"time"
)

func intp(v int) *int { return &v }

// TestAPIKeyRepo_RoundTrip proves the typed repository's full lifecycle:
// create, an equality lookup-by-hash that finds it (and preserves the nullable
// rpm_limit), an unknown-hash lookup that finds nothing, revoke that marks the
// key revoked, and a deterministically ordered list.
func TestAPIKeyRepo_RoundTrip(t *testing.T) {
	db := migratedQuotaDB(t)
	repo := NewAPIKeyRepo(db)
	ctx := context.Background()

	created := time.Unix(1_700_000_000, 0).UTC()
	if err := repo.Create(ctx, CreateAPIKeyParams{
		ID: "k-1", Label: "prod", KeyHash: hex64("aa"), KeyPrefix: "vk_live_aa", RPMLimit: intp(120), CreatedAt: created,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, ok, err := repo.LookupByHash(ctx, hex64("aa"))
	if err != nil {
		t.Fatalf("LookupByHash: %v", err)
	}
	if !ok {
		t.Fatalf("LookupByHash did not find the just-created key")
	}
	if got.ID != "k-1" || got.Label != "prod" || got.KeyPrefix != "vk_live_aa" {
		t.Fatalf("looked-up key = %+v, want id=k-1 label=prod prefix=vk_live_aa", got)
	}
	if got.RPMLimit == nil || *got.RPMLimit != 120 {
		t.Fatalf("rpm_limit round-trip = %v, want 120", got.RPMLimit)
	}
	if got.Revoked() {
		t.Fatalf("a fresh key must not be revoked")
	}

	// Revoke → the key reads as revoked but is still found (revocation is a
	// state, not a deletion; unit 2's auth is what refuses a revoked key).
	if err := repo.Revoke(ctx, "k-1", created.Add(time.Hour)); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	got, ok, err = repo.LookupByHash(ctx, hex64("aa"))
	if err != nil || !ok {
		t.Fatalf("LookupByHash after revoke: ok=%v err=%v", ok, err)
	}
	if !got.Revoked() {
		t.Fatalf("key must read as revoked after Revoke")
	}
	// Revoke is idempotent: a second call is a no-op, not an error.
	if err := repo.Revoke(ctx, "k-1", created.Add(2*time.Hour)); err != nil {
		t.Fatalf("second Revoke must be a no-op: %v", err)
	}
}

// TestAPIKeyRepo_UnknownHashNeverMatches proves lookup is an EQUALITY match on
// the full hash: neither a wholly-unknown hash nor a PREFIX of a stored hash
// authenticates. The prefix case is the load-bearing one — it is what a
// `LIKE hash || '%'` regression would wrongly match.
func TestAPIKeyRepo_UnknownHashNeverMatches(t *testing.T) {
	db := migratedQuotaDB(t)
	repo := NewAPIKeyRepo(db)
	ctx := context.Background()

	full := hex64("beef")
	if err := repo.Create(ctx, CreateAPIKeyParams{
		ID: "k-1", Label: "prod", KeyHash: full, KeyPrefix: "vk_live_be", CreatedAt: time.Unix(1, 0),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, ok, err := repo.LookupByHash(ctx, hex64("ffff")); err != nil || ok {
		t.Fatalf("a wholly-unknown hash matched (ok=%v, err=%v)", ok, err)
	}
	if _, ok, err := repo.LookupByHash(ctx, "beef"); err != nil || ok {
		t.Fatalf("a PREFIX of a stored hash matched — lookup must be equality, not a prefix/LIKE (ok=%v, err=%v)", ok, err)
	}
}

// TestAPIKeyRepo_ListDeterministicOrder proves List orders by created_at ASC,
// then id ASC as a stable tie-break, regardless of insertion order.
func TestAPIKeyRepo_ListDeterministicOrder(t *testing.T) {
	db := migratedQuotaDB(t)
	repo := NewAPIKeyRepo(db)
	ctx := context.Background()

	// Insert out of order; two share a created_at to exercise the id tie-break.
	must := func(id string, at int64) {
		if err := repo.Create(ctx, CreateAPIKeyParams{
			ID: id, Label: id, KeyHash: hex64(id), KeyPrefix: "vk_live_" + id, CreatedAt: time.Unix(at, 0),
		}); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
	}
	must("c", 200)
	must("a", 100)
	must("b", 100) // same instant as "a" → id tie-break puts "a" before "b"

	keys, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var order []string
	for _, k := range keys {
		order = append(order, k.ID)
	}
	want := []string{"a", "b", "c"}
	if len(order) != len(want) {
		t.Fatalf("List order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("List order = %v, want %v (created_at ASC, id ASC)", order, want)
		}
	}
}

// TestAPIKeyRepo_TouchLastUsed proves the last-used bookkeeping touch persists.
func TestAPIKeyRepo_TouchLastUsed(t *testing.T) {
	db := migratedQuotaDB(t)
	repo := NewAPIKeyRepo(db)
	ctx := context.Background()

	if err := repo.Create(ctx, CreateAPIKeyParams{
		ID: "k-1", Label: "prod", KeyHash: hex64("cc"), KeyPrefix: "vk_live_cc", CreatedAt: time.Unix(1, 0),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got, _, _ := repo.LookupByHash(ctx, hex64("cc")); got.LastUsedAt != nil {
		t.Fatalf("last_used_at must start NULL, got %v", got.LastUsedAt)
	}

	used := time.Unix(1_700_000_500, 0).UTC()
	if err := repo.TouchLastUsed(ctx, "k-1", used); err != nil {
		t.Fatalf("TouchLastUsed: %v", err)
	}
	got, _, _ := repo.LookupByHash(ctx, hex64("cc"))
	if got.LastUsedAt == nil || !got.LastUsedAt.Equal(used) {
		t.Fatalf("last_used_at = %v, want %v", got.LastUsedAt, used)
	}
}
