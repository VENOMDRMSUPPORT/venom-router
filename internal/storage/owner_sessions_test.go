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
