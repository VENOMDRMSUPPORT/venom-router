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

func TestOwnerSessionRepo_Renew_AdvancesLastSeenAndIdleExpiry(t *testing.T) {
	db := migratedOwnerAuthDB(t)
	repo := NewOwnerSessionRepo(db)
	ctx := context.Background()
	created := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	tokenHash := []byte("renew-me")
	if err := repo.Create(ctx, OwnerSessionRow{
		TokenHash:         tokenHash,
		CreatedAt:         created,
		LastSeenAt:        created,
		IdleExpiresAt:     created.Add(30 * time.Minute),
		AbsoluteExpiresAt: created.Add(12 * time.Hour),
	}); err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}

	renewAt := created.Add(10 * time.Minute)
	newIdle := renewAt.Add(30 * time.Minute)
	if err := repo.Renew(ctx, tokenHash, renewAt, newIdle); err != nil {
		t.Fatalf("Renew: unexpected error: %v", err)
	}

	got, ok, err := repo.GetByTokenHash(ctx, tokenHash)
	if err != nil || !ok {
		t.Fatalf("GetByTokenHash after Renew: ok=%v err=%v", ok, err)
	}
	if !got.LastSeenAt.Equal(renewAt) {
		t.Fatalf("LastSeenAt = %v, want %v", got.LastSeenAt, renewAt)
	}
	if !got.IdleExpiresAt.Equal(newIdle) {
		t.Fatalf("IdleExpiresAt = %v, want %v", got.IdleExpiresAt, newIdle)
	}
}

func TestOwnerSessionRepo_Renew_RevokedSessionUnaffected(t *testing.T) {
	db := migratedOwnerAuthDB(t)
	repo := NewOwnerSessionRepo(db)
	ctx := context.Background()
	created := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	tokenHash := []byte("renew-revoked")
	if err := repo.Create(ctx, OwnerSessionRow{
		TokenHash:         tokenHash,
		CreatedAt:         created,
		LastSeenAt:        created,
		IdleExpiresAt:     created.Add(30 * time.Minute),
		AbsoluteExpiresAt: created.Add(12 * time.Hour),
	}); err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}
	if err := repo.Revoke(ctx, tokenHash); err != nil {
		t.Fatalf("Revoke: unexpected error: %v", err)
	}

	if err := repo.Renew(ctx, tokenHash, created.Add(time.Hour), created.Add(90*time.Minute)); err != nil {
		t.Fatalf("Renew: unexpected error: %v", err)
	}

	got, ok, err := repo.GetByTokenHash(ctx, tokenHash)
	if err != nil || !ok {
		t.Fatalf("GetByTokenHash: ok=%v err=%v", ok, err)
	}
	if got.RevokedAt == nil {
		t.Fatalf("RevokedAt = nil, want set (a revoked session must never be resurrected by Renew)")
	}
	// idle_expires_at must be unchanged from creation — Renew's WHERE
	// clause excludes revoked rows.
	if !got.IdleExpiresAt.Equal(created.Add(30 * time.Minute)) {
		t.Fatalf("IdleExpiresAt = %v, want unchanged %v", got.IdleExpiresAt, created.Add(30*time.Minute))
	}
}

func TestOwnerSessionRepo_RevokeAll_RevokesEveryActiveSession(t *testing.T) {
	db := migratedOwnerAuthDB(t)
	repo := NewOwnerSessionRepo(db)
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	hashes := [][]byte{[]byte("sess-a"), []byte("sess-b"), []byte("sess-c")}
	for _, h := range hashes {
		if err := repo.Create(ctx, OwnerSessionRow{
			TokenHash:         h,
			CreatedAt:         now,
			LastSeenAt:        now,
			IdleExpiresAt:     now.Add(30 * time.Minute),
			AbsoluteExpiresAt: now.Add(12 * time.Hour),
		}); err != nil {
			t.Fatalf("Create(%s): unexpected error: %v", h, err)
		}
	}

	// One of the three is already revoked before RevokeAll runs.
	if err := repo.Revoke(ctx, hashes[0]); err != nil {
		t.Fatalf("pre-revoke: unexpected error: %v", err)
	}
	preRevokedAt, _, err := repo.GetByTokenHash(ctx, hashes[0])
	if err != nil {
		t.Fatalf("GetByTokenHash: %v", err)
	}

	if err := repo.RevokeAll(ctx, now.Add(time.Hour)); err != nil {
		t.Fatalf("RevokeAll: unexpected error: %v", err)
	}

	for _, h := range hashes {
		got, ok, err := repo.GetByTokenHash(ctx, h)
		if err != nil || !ok {
			t.Fatalf("GetByTokenHash(%s): ok=%v err=%v", h, ok, err)
		}
		if got.RevokedAt == nil {
			t.Fatalf("session %s not revoked after RevokeAll", h)
		}
	}

	// The already-revoked session's RevokedAt must be untouched by
	// RevokeAll (its WHERE clause only matches still-active rows).
	postRevokedAt, _, err := repo.GetByTokenHash(ctx, hashes[0])
	if err != nil {
		t.Fatalf("GetByTokenHash: %v", err)
	}
	if !postRevokedAt.RevokedAt.Equal(*preRevokedAt.RevokedAt) {
		t.Fatalf("already-revoked session's RevokedAt changed by RevokeAll: before=%v after=%v", preRevokedAt.RevokedAt, postRevokedAt.RevokedAt)
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
