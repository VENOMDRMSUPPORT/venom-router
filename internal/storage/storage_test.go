package storage

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
)

func TestOpen_PragmasInEffect(t *testing.T) {
	dir := t.TempDir()

	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	cases := []struct {
		pragma string
		want   string
	}{
		{"journal_mode", "wal"},
		{"busy_timeout", "5000"},
		{"foreign_keys", "1"},
		{"synchronous", "1"}, // NORMAL
	}
	for _, c := range cases {
		got := queryPragmaString(t, db, c.pragma)
		if got != c.want {
			t.Fatalf("PRAGMA %s = %q, want %q", c.pragma, got, c.want)
		}
	}
}

func TestOpen_WALSidecarAppearsAfterWrite(t *testing.T) {
	dir := t.TempDir()

	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	// A write to the database header (not a table/schema — user_version is
	// a plain header field) is enough to exercise a WAL write; this keeps
	// the test within this unit's "no tables" boundary.
	if _, err := db.Conn().Exec("PRAGMA user_version = 1"); err != nil {
		t.Fatalf("write to trigger a WAL page write: %v", err)
	}

	walPath := db.Path() + "-wal"
	if _, err := os.Stat(walPath); err != nil {
		t.Fatalf("expected WAL sidecar file %q to exist after a write, stat error = %v", walPath, err)
	}

	if got := queryPragmaString(t, db, "journal_mode"); got != "wal" {
		t.Fatalf("PRAGMA journal_mode = %q, want %q", got, "wal")
	}
}

// TestOpen_ConcurrentWritesSerializeUnderSingleWriter proves single-writer
// discipline holds: many goroutines each run a read-modify-write
// transaction against the same header field (user_version) concurrently.
// With SetMaxOpenConns(1), database/sql only ever hands out the one
// connection to whichever transaction currently holds it, so every
// increment is correctly serialized end to end — no lost updates, no
// SQLITE_BUSY errors, no corruption.
func TestOpen_ConcurrentWritesSerializeUnderSingleWriter(t *testing.T) {
	dir := t.TempDir()

	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	if got := db.Conn().Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("MaxOpenConnections = %d, want 1 (single-writer discipline)", got)
	}

	const goroutines = 20
	const incrementsPerGoroutine = 10

	var wg sync.WaitGroup
	errs := make(chan error, goroutines*incrementsPerGoroutine)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < incrementsPerGoroutine; j++ {
				if err := incrementUserVersion(db); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent increment failed: %v", err)
	}

	got := queryPragmaString(t, db, "user_version")
	want := strconv.Itoa(goroutines * incrementsPerGoroutine)
	if got != want {
		t.Fatalf("user_version = %q, want %q (a mismatch means an update was lost — single-writer discipline did not hold)", got, want)
	}
}

func incrementUserVersion(db *DB) error {
	tx, err := db.Conn().Begin()
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has succeeded

	var current int
	if err := tx.QueryRow("PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}

	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", current+1)); err != nil {
		return fmt.Errorf("write user_version: %w", err)
	}

	return tx.Commit()
}

func queryPragmaString(t *testing.T, db *DB, name string) string {
	t.Helper()

	var v any
	if err := db.Conn().QueryRow("PRAGMA " + name).Scan(&v); err != nil {
		t.Fatalf("query PRAGMA %s: %v", name, err)
	}

	switch val := v.(type) {
	case []byte:
		return string(val)
	case string:
		return val
	case int64:
		return strconv.FormatInt(val, 10)
	default:
		return fmt.Sprintf("%v", val)
	}
}
