package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// SettingsRow is the single owner-settings row (P2b-CAPI-005). Config
// only, never a secret — theme/density are non-secret UI enums mirroring
// the frozen @venom/design-system vocabulary (Design_System/src/themes.ts,
// Design_System/src/density.ts). A fresh DB has no row yet; Get resolves
// that to the defaults below rather than erroring.
type SettingsRow struct {
	Theme     string
	Density   string
	UpdatedAt time.Time // zero value when no row exists yet (Get returned defaults)
}

// DefaultTheme and DefaultDensity are the frozen @venom/design-system
// defaults Get returns when no owner_settings row exists yet — the exact
// values a fresh DB resolves to, with no seed row to reason about.
const (
	DefaultTheme   = "venom-dark"
	DefaultDensity = "comfortable"
)

// SettingsRepo persists the single owner_settings row (M5,
// 00005_owner_settings.sql). It is config-only storage wired directly into
// httpapi (like jobs.go) — there is NO accounts/application port for
// settings, since settings are configuration, not account-domain
// orchestration.
type SettingsRepo struct {
	db *DB
}

// NewSettingsRepo builds a repository over db's existing connection.
func NewSettingsRepo(db *DB) *SettingsRepo {
	return &SettingsRepo{db: db}
}

// Get reads the single owner_settings row. When no row exists yet (a fresh
// DB, before the first PUT), it returns SettingsRow{Theme: DefaultTheme,
// Density: DefaultDensity} with a zero UpdatedAt and a nil error — a fresh
// DB resolves to the frozen defaults, never 500. Other errors wrap-and-
// return. This repo does NOT validate the enum here: the owner_settings
// CHECK constraint is the DB-level backstop, and the httpapi handler does
// the explicit validation ahead of the write.
func (r *SettingsRepo) Get(ctx context.Context) (SettingsRow, error) {
	var (
		out            SettingsRow
		theme, density string
		updatedAt      int64
	)
	err := r.db.Conn().QueryRowContext(ctx,
		`SELECT theme, density, updated_at FROM owner_settings WHERE id = 1`,
	).Scan(&theme, &density, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SettingsRow{Theme: DefaultTheme, Density: DefaultDensity}, nil
	}
	if err != nil {
		return SettingsRow{}, fmt.Errorf("storage: get owner_settings: %w", err)
	}

	out.Theme = theme
	out.Density = density
	out.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return out, nil
}

// Put UPSERTs the single owner_settings row (id = 1), stamping updated_at
// = now. It does NOT validate the enum — that is the httpapi handler's
// job; the owner_settings CHECK constraint is the defense-in-depth
// backstop that bites if an invalid value ever reaches this layer. now is
// the caller-supplied clock (tests inject a fixed value); it is stored as
// an INTEGER epoch (.Unix()), matching the M2–M4 timestamp convention.
func (r *SettingsRepo) Put(ctx context.Context, theme, density string, now time.Time) error {
	_, err := r.db.Conn().ExecContext(ctx,
		`INSERT INTO owner_settings (id, theme, density, updated_at)
		 VALUES (1, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET theme = excluded.theme, density = excluded.density, updated_at = excluded.updated_at`,
		theme, density, now.Unix(),
	)
	if err != nil {
		return fmt.Errorf("storage: put owner_settings: %w", err)
	}
	return nil
}
