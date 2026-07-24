package storage

import (
	"context"
	"testing"
)

func TestOwnerAuthRepo_ExistsFalseBeforeCreate(t *testing.T) {
	db := migratedOwnerAuthDB(t)
	repo := NewOwnerAuthRepo(db)

	exists, err := repo.Exists(context.Background())
	if err != nil {
		t.Fatalf("Exists: unexpected error: %v", err)
	}
	if exists {
		t.Fatalf("Exists() = true before any Create, want false")
	}
}

func TestOwnerAuthRepo_CreateThenExistsAndGet(t *testing.T) {
	db := migratedOwnerAuthDB(t)
	repo := NewOwnerAuthRepo(db)
	ctx := context.Background()

	row := OwnerAuthRow{
		PasswordHash: []byte("hash-bytes"),
		Salt:         []byte("salt-bytes"),
		KDFTime:      3,
		KDFMemKiB:    65536,
		KDFThreads:   4,
		KDFKeyLen:    32,
	}
	if err := repo.Create(ctx, row); err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}

	exists, err := repo.Exists(ctx)
	if err != nil {
		t.Fatalf("Exists: unexpected error: %v", err)
	}
	if !exists {
		t.Fatalf("Exists() = false after Create, want true")
	}

	got, ok, err := repo.Get(ctx)
	if err != nil {
		t.Fatalf("Get: unexpected error: %v", err)
	}
	if !ok {
		t.Fatalf("Get() ok = false after Create, want true")
	}
	if string(got.PasswordHash) != "hash-bytes" || string(got.Salt) != "salt-bytes" {
		t.Fatalf("Get() = %+v, want the stored hash/salt bytes back exactly", got)
	}
	if got.KDFTime != 3 || got.KDFMemKiB != 65536 || got.KDFThreads != 4 || got.KDFKeyLen != 32 {
		t.Fatalf("Get() KDF params = %+v, want the exact stored params", got)
	}
}

func TestOwnerAuthRepo_SecondCreateRejected(t *testing.T) {
	db := migratedOwnerAuthDB(t)
	repo := NewOwnerAuthRepo(db)
	ctx := context.Background()

	first := OwnerAuthRow{PasswordHash: []byte("h1"), Salt: []byte("s1"), KDFTime: 3, KDFMemKiB: 65536, KDFThreads: 4, KDFKeyLen: 32}
	if err := repo.Create(ctx, first); err != nil {
		t.Fatalf("first Create: unexpected error: %v", err)
	}

	second := OwnerAuthRow{PasswordHash: []byte("h2"), Salt: []byte("s2"), KDFTime: 3, KDFMemKiB: 65536, KDFThreads: 4, KDFKeyLen: 32}
	if err := repo.Create(ctx, second); err != ErrOwnerAuthAlreadySet {
		t.Fatalf("second Create error = %v, want ErrOwnerAuthAlreadySet", err)
	}

	// The second Create must not have overwritten the first row.
	got, ok, err := repo.Get(ctx)
	if err != nil || !ok {
		t.Fatalf("Get after rejected second Create: ok=%v err=%v", ok, err)
	}
	if string(got.PasswordHash) != "h1" {
		t.Fatalf("PasswordHash = %q after rejected second Create, want unchanged %q", got.PasswordHash, "h1")
	}
}

func TestOwnerAuthRepo_GetOkFalseBeforeCreate(t *testing.T) {
	db := migratedOwnerAuthDB(t)
	repo := NewOwnerAuthRepo(db)

	_, ok, err := repo.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("Get() ok = true before any Create, want false")
	}
}
