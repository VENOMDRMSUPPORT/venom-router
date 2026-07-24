package storage

import (
	"context"
	"testing"
	"time"
)

func TestOwnerSessionRepo_CreateInsertsOneRow(t *testing.T) {
	db := migratedOwnerAuthDB(t)
	repo := NewOwnerSessionRepo(db)

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	row := OwnerSessionRow{
		TokenHash:         []byte("token-hash-bytes"),
		CreatedAt:         now,
		LastSeenAt:        now,
		IdleExpiresAt:     now.Add(30 * time.Minute),
		AbsoluteExpiresAt: now.Add(12 * time.Hour),
	}
	if err := repo.Create(context.Background(), row); err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}

	assertRowCount(t, db, "owner_sessions", 1)

	var (
		tokenHash                                               []byte
		createdAt, lastSeenAt, idleExpiresAt, absoluteExpiresAt string
		revokedAt, reverifyFreshUntil                           *string
	)
	err := db.Conn().QueryRow(
		`SELECT token_hash, created_at, last_seen_at, idle_expires_at, absolute_expires_at, revoked_at, reverify_fresh_until
		 FROM owner_sessions LIMIT 1`,
	).Scan(&tokenHash, &createdAt, &lastSeenAt, &idleExpiresAt, &absoluteExpiresAt, &revokedAt, &reverifyFreshUntil)
	if err != nil {
		t.Fatalf("query inserted row: %v", err)
	}

	if string(tokenHash) != "token-hash-bytes" {
		t.Fatalf("token_hash = %q, want %q", tokenHash, "token-hash-bytes")
	}
	wantCreatedAt := formatTimestamp(now)
	if createdAt != wantCreatedAt {
		t.Fatalf("created_at = %q, want %q", createdAt, wantCreatedAt)
	}
	wantIdle := formatTimestamp(now.Add(30 * time.Minute))
	if idleExpiresAt != wantIdle {
		t.Fatalf("idle_expires_at = %q, want %q", idleExpiresAt, wantIdle)
	}
	wantAbsolute := formatTimestamp(now.Add(12 * time.Hour))
	if absoluteExpiresAt != wantAbsolute {
		t.Fatalf("absolute_expires_at = %q, want %q", absoluteExpiresAt, wantAbsolute)
	}
	if revokedAt != nil {
		t.Fatalf("revoked_at = %v, want NULL for a freshly created session", *revokedAt)
	}
	if reverifyFreshUntil != nil {
		t.Fatalf("reverify_fresh_until = %v, want NULL for a freshly created session", *reverifyFreshUntil)
	}
}

func TestOwnerSessionRepo_TokenHashMustBeUnique(t *testing.T) {
	db := migratedOwnerAuthDB(t)
	repo := NewOwnerSessionRepo(db)
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	row := OwnerSessionRow{
		TokenHash:         []byte("duplicate-hash"),
		CreatedAt:         now,
		LastSeenAt:        now,
		IdleExpiresAt:     now.Add(30 * time.Minute),
		AbsoluteExpiresAt: now.Add(12 * time.Hour),
	}
	if err := repo.Create(ctx, row); err != nil {
		t.Fatalf("first Create: unexpected error: %v", err)
	}
	if err := repo.Create(ctx, row); err == nil {
		t.Fatalf("second Create with a duplicate token_hash succeeded, want rejection (token_hash is UNIQUE)")
	}
}

func TestOwnerSessionRepo_GetByTokenHash_AbsentReturnsNotOK(t *testing.T) {
	db := migratedOwnerAuthDB(t)
	repo := NewOwnerSessionRepo(db)

	_, ok, err := repo.GetByTokenHash(context.Background(), []byte("no-such-hash"))
	if err != nil {
		t.Fatalf("GetByTokenHash: unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("GetByTokenHash(absent) ok = true, want false")
	}
}

func TestOwnerSessionRepo_GetByTokenHash_FindsCreatedRowNotRevoked(t *testing.T) {
	db := migratedOwnerAuthDB(t)
	repo := NewOwnerSessionRepo(db)
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	tokenHash := []byte("lookup-hash")
	if err := repo.Create(ctx, OwnerSessionRow{
		TokenHash:         tokenHash,
		CreatedAt:         now,
		LastSeenAt:        now,
		IdleExpiresAt:     now.Add(30 * time.Minute),
		AbsoluteExpiresAt: now.Add(12 * time.Hour),
	}); err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}

	got, ok, err := repo.GetByTokenHash(ctx, tokenHash)
	if err != nil {
		t.Fatalf("GetByTokenHash: unexpected error: %v", err)
	}
	if !ok {
		t.Fatalf("GetByTokenHash(existing) ok = false, want true")
	}
	if got.RevokedAt != nil {
		t.Fatalf("RevokedAt = %v for a freshly created session, want nil", got.RevokedAt)
	}
	if !got.IdleExpiresAt.Equal(now.Add(30 * time.Minute)) {
		t.Fatalf("IdleExpiresAt = %v, want %v", got.IdleExpiresAt, now.Add(30*time.Minute))
	}
	if !got.AbsoluteExpiresAt.Equal(now.Add(12 * time.Hour)) {
		t.Fatalf("AbsoluteExpiresAt = %v, want %v", got.AbsoluteExpiresAt, now.Add(12*time.Hour))
	}
}

func TestOwnerSessionRepo_Revoke_SetsRevokedAt(t *testing.T) {
	db := migratedOwnerAuthDB(t)
	repo := NewOwnerSessionRepo(db)
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	tokenHash := []byte("revoke-me")
	if err := repo.Create(ctx, OwnerSessionRow{
		TokenHash:         tokenHash,
		CreatedAt:         now,
		LastSeenAt:        now,
		IdleExpiresAt:     now.Add(30 * time.Minute),
		AbsoluteExpiresAt: now.Add(12 * time.Hour),
	}); err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}

	before := time.Now()
	if err := repo.Revoke(ctx, tokenHash); err != nil {
		t.Fatalf("Revoke: unexpected error: %v", err)
	}
	after := time.Now()

	got, ok, err := repo.GetByTokenHash(ctx, tokenHash)
	if err != nil || !ok {
		t.Fatalf("GetByTokenHash after Revoke: ok=%v err=%v", ok, err)
	}
	if got.RevokedAt == nil {
		t.Fatalf("RevokedAt = nil after Revoke, want a timestamp")
	}
	// Truncate to the millisecond precision the TEXT column stores.
	if got.RevokedAt.Before(before.Truncate(time.Millisecond).Add(-time.Millisecond)) || got.RevokedAt.After(after.Add(time.Second)) {
		t.Fatalf("RevokedAt = %v, want between %v and %v", got.RevokedAt, before, after)
	}
}

func TestOwnerSessionRepo_Revoke_IdempotentAndNeverResurrects(t *testing.T) {
	db := migratedOwnerAuthDB(t)
	repo := NewOwnerSessionRepo(db)
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	tokenHash := []byte("revoke-twice")
	if err := repo.Create(ctx, OwnerSessionRow{
		TokenHash:         tokenHash,
		CreatedAt:         now,
		LastSeenAt:        now,
		IdleExpiresAt:     now.Add(30 * time.Minute),
		AbsoluteExpiresAt: now.Add(12 * time.Hour),
	}); err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}

	if err := repo.Revoke(ctx, tokenHash); err != nil {
		t.Fatalf("first Revoke: unexpected error: %v", err)
	}
	first, _, err := repo.GetByTokenHash(ctx, tokenHash)
	if err != nil {
		t.Fatalf("GetByTokenHash after first Revoke: %v", err)
	}
	firstRevokedAt := *first.RevokedAt

	// A second Revoke on an already-revoked row must not error and must
	// not change the recorded RevokedAt (the WHERE clause only matches
	// still-active rows).
	if err := repo.Revoke(ctx, tokenHash); err != nil {
		t.Fatalf("second Revoke (already revoked): unexpected error: %v", err)
	}
	second, ok, err := repo.GetByTokenHash(ctx, tokenHash)
	if err != nil || !ok {
		t.Fatalf("GetByTokenHash after second Revoke: ok=%v err=%v", ok, err)
	}
	if second.RevokedAt == nil || !second.RevokedAt.Equal(firstRevokedAt) {
		t.Fatalf("RevokedAt changed on a second Revoke: first=%v second=%v, want unchanged", firstRevokedAt, second.RevokedAt)
	}

	// Revoking a tokenHash that never existed is also a no-op, not an error.
	if err := repo.Revoke(ctx, []byte("never-existed")); err != nil {
		t.Fatalf("Revoke(never-existed): unexpected error: %v", err)
	}
}
