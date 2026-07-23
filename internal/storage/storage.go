// Package storage owns the single SQLite database handle and is the
// structural entry point for all SQL in the codebase: later units add
// concrete typed repositories on top of the DB type this package
// exposes, rather than opening their own connection or issuing ad-hoc
// SQL elsewhere. This unit establishes only that base — no tables, no
// migrations, no queries beyond what this package's own tests use to
// verify the pragmas took effect.
package storage

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// driverName is the database/sql driver modernc.org/sqlite registers
// itself under.
const driverName = "sqlite"

// dbFileName is the single SQLite database file's name within <dataDir>.
const dbFileName = "venom.db"

// pragmas are applied via DSN query parameters (each connection the
// driver opens runs them itself), not a one-time Exec after Open, so
// they hold even if the connection pool ever replaces the underlying
// connection. Exactly these four, verbatim from 01 §5 / the task card —
// do not add, omit, or "improve" this set here.
var pragmas = []string{
	"journal_mode(WAL)",
	"busy_timeout(5000)",
	"foreign_keys(ON)",
	"synchronous(NORMAL)",
}

// DB is the typed-repository base: the single *sql.DB handle everything
// in internal/storage is built on. Concrete repositories (added in
// later units) take a *DB and issue their queries through Conn();
// nothing outside internal/storage is meant to reach into it directly.
type DB struct {
	sqlDB *sql.DB
	path  string
}

// Open opens the single database file at <dataDir>/venom.db (dataDir is
// resolved by the caller via platform.DataDir/platform.EnsureDataDir —
// this package never hardcodes a path), applies the four required
// pragmas, and enforces single-writer discipline.
//
// Single-writer discipline: SetMaxOpenConns(1) on the returned *sql.DB.
// SQLite already has exactly one physical writer regardless of caller
// behavior; pairing that with a database/sql pool that only ever hands
// out one connection means every statement — reads included — serializes
// through that one connection, so there is no separate reader connection
// that could contend with a writer for SQLite's own single-writer lock.
// This is the standard, documented approach for using SQLite (any
// driver, modernc.org/sqlite included) through database/sql.
//
// File permissions: the file is pre-created (if it does not already
// exist) with mode 0600 before the driver opens it, matching the
// owner-only intent internal/platform already applies to the data
// directory. On Windows this permission bit is not meaningful to the
// filesystem — per-user isolation comes from %LOCALAPPDATA% already
// being a per-user path, the same precedent as P0-FND-003; no custom
// ACL work is added here.
func Open(dataDir string) (*DB, error) {
	path := filepath.Join(dataDir, dbFileName)

	if err := ensureOwnerOnlyFile(path); err != nil {
		return nil, fmt.Errorf("storage: create %q: %w", path, err)
	}

	// No "file:" scheme prefix: modernc.org/sqlite treats a dsn that
	// doesn't start with "file:" as a plain OS filename once the "?..."
	// query suffix is stripped (the query itself is still parsed
	// separately for _pragma). This sidesteps the Windows
	// "file:///C:/..." URI-escaping quirk entirely, since the path never
	// has to be expressed as a URI.
	dsn := path + "?" + pragmaQuery()

	sqlDB, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("storage: open %q: %w", path, err)
	}

	sqlDB.SetMaxOpenConns(1)

	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("storage: open %q: %w", path, err)
	}

	return &DB{sqlDB: sqlDB, path: path}, nil
}

func ensureOwnerOnlyFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	return f.Close()
}

func pragmaQuery() string {
	v := url.Values{}
	for _, p := range pragmas {
		v.Add("_pragma", p)
	}
	return v.Encode()
}

// Close closes the underlying database handle.
func (d *DB) Close() error {
	return d.sqlDB.Close()
}

// Path returns the resolved path to the database file this DB was
// opened against.
func (d *DB) Path() string {
	return d.path
}

// Conn exposes the underlying *sql.DB. It is the single intended entry
// point for SQL in this codebase: concrete repositories (added in later
// units) call this rather than opening their own connection.
func (d *DB) Conn() *sql.DB {
	return d.sqlDB
}
