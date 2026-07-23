package storage

import (
	"embed"
	"io/fs"
)

// embeddedMigrations embeds the schema-version baseline/system
// migrations. Domain-table migrations (accounts, providers, quota, etc.)
// are out of scope for this unit — M1+ adds those; only the baseline
// lives here.
//
//go:embed migrations/*.sql
var embeddedMigrations embed.FS

// migrationsRootFS re-roots embeddedMigrations at the migrations
// directory itself. goose.NewProvider globs "*.sql" at the given fs.FS's
// root rather than recursing into subdirectories, so the embedded
// migrations must be presented with "migrations/" stripped — the same
// fs.Sub re-rooting goose's own documentation and tests use.
func migrationsRootFS() (fs.FS, error) {
	return fs.Sub(embeddedMigrations, "migrations")
}
