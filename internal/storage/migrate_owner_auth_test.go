package storage

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// ownerAuthVersion is the goose version of the M1 owner-auth migration
// (00002_owner_auth.sql).
const ownerAuthVersion = 2

func TestMigrateOwnerAuth_UpDownUp(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	if _, err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() (up) error = %v", err)
	}
	assertTableExists(t, db, "owner_auth", true)
	assertTableExists(t, db, "owner_sessions", true)
	assertTableExists(t, db, "auth_events", true)

	// Roll back every migration applied on top of M1, then M1 itself, so
	// this proves M1's own down path no matter how many later migrations
	// exist (ownerAuthVersion is M1's goose version).
	for currentVersion(t, db) >= ownerAuthVersion {
		if _, err := DownOne(ctx, db); err != nil {
			t.Fatalf("DownOne() error = %v", err)
		}
	}
	assertTableExists(t, db, "owner_auth", false)
	assertTableExists(t, db, "owner_sessions", false)
	assertTableExists(t, db, "auth_events", false)
	assertBaselineTableExists(t, db, true)

	if _, err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() (re-up) error = %v", err)
	}
	assertTableExists(t, db, "owner_auth", true)
	assertTableExists(t, db, "owner_sessions", true)
	assertTableExists(t, db, "auth_events", true)
}

func TestOwnerAuth_SingleRowConstraint(t *testing.T) {
	db := migratedOwnerAuthDB(t)

	if err := insertOwnerAuthRow(db, 1); err != nil {
		t.Fatalf("insert first owner_auth row (id=1): %v", err)
	}

	if err := insertOwnerAuthRow(db, 1); err == nil {
		t.Fatalf("insert duplicate owner_auth row (id=1) succeeded, want rejection")
	}

	if err := insertOwnerAuthRow(db, 2); err == nil {
		t.Fatalf("insert second owner_auth row (id=2) succeeded, want rejection")
	}
}

func TestAuthEvents_AppendOnly(t *testing.T) {
	db := migratedOwnerAuthDB(t)

	res, err := db.Conn().Exec(
		`INSERT INTO auth_events (action, result, reason_code) VALUES ('login', 'success', NULL)`,
	)
	if err != nil {
		t.Fatalf("insert auth_events row: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}

	if _, err := db.Conn().Exec(
		`UPDATE auth_events SET result = 'failure' WHERE id = ?`, id,
	); err == nil {
		t.Fatalf("UPDATE auth_events succeeded, want RAISE(ABORT) rejection")
	}

	if _, err := db.Conn().Exec(
		`DELETE FROM auth_events WHERE id = ?`, id,
	); err == nil {
		t.Fatalf("DELETE auth_events succeeded, want RAISE(ABORT) rejection")
	}

	var result string
	if err := db.Conn().QueryRow(
		`SELECT result FROM auth_events WHERE id = ?`, id,
	).Scan(&result); err != nil {
		t.Fatalf("query surviving auth_events row: %v", err)
	}
	if result != "success" {
		t.Fatalf("auth_events row result = %q, want %q (rejected UPDATE must not have applied)", result, "success")
	}
}

func migratedOwnerAuthDB(t *testing.T) *DB {
	t.Helper()

	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := Migrate(context.Background(), db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return db
}

func insertOwnerAuthRow(db *DB, id int) error {
	_, err := db.Conn().Exec(
		`INSERT INTO owner_auth (id, password_hash, salt, kdf_time, kdf_mem_kib, kdf_threads, kdf_key_len)
		 VALUES (?, ?, ?, 3, 65536, 4, 32)`,
		id, []byte("hash"), []byte("salt"),
	)
	return err
}

func assertTableExists(t *testing.T, db *DB, name string, want bool) {
	t.Helper()

	var got string
	err := db.Conn().QueryRow(
		"SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", name,
	).Scan(&got)

	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("query sqlite_master for %s: %v", name, err)
	}
	if exists != want {
		t.Fatalf("table %s exists = %v, want %v", name, exists, want)
	}
}
