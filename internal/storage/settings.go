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
// that to the defaults below rather than erroring. EnrichmentEnabled is the
// P3a-CAPI-003 optional-metadata-enrichment toggle (04 §2b): off by
// default, both at this Go-layer default AND at the 00007 migration's own
// column default — the routing-critical free-safety pipeline never reads
// this field and is never affected by it.
type SettingsRow struct {
	Theme             string
	Density           string
	Accent            string
	RadiusPx          int
	SpacingScale      float64
	EnrichmentEnabled bool

	// Operational settings (P6-CAPI-001, 00014). Unlike the appearance
	// fields these change RUNTIME behaviour, so each one's default here is
	// the exact value that used to be hardcoded at its consumer — an install
	// that upgrades across 00014 keeps behaving identically until the owner
	// changes something.
	QuotaStalenessSeconds        int
	ProbeMaxInFlightPerProvider  int
	ProbeExpensiveEnabled        bool
	ProbePerAccountWindowSeconds int

	UpdatedAt time.Time // zero value when no row exists yet (Get returned defaults)
}

// StalenessWindow is QuotaStalenessSeconds as a duration — the value every
// quota.Window.State call must be given (05 §4). Exposed as a method so no
// consumer re-derives the seconds->duration conversion.
func (r SettingsRow) StalenessWindow() time.Duration {
	return time.Duration(r.QuotaStalenessSeconds) * time.Second
}

// ProbeAccountWindow is ProbePerAccountWindowSeconds as a duration.
func (r SettingsRow) ProbeAccountWindow() time.Duration {
	return time.Duration(r.ProbePerAccountWindowSeconds) * time.Second
}

// DefaultTheme and DefaultDensity are the frozen @venom/design-system
// defaults Get returns when no owner_settings row exists yet — the exact
// values a fresh DB resolves to, with no seed row to reason about.
// DefaultAccent/DefaultRadiusPx/DefaultSpacingScale extend the same frozen
// vocabulary for the customizer dimension (Design_System/src/customizer.ts
// -> DEFAULT_ACCENT/DEFAULT_RADIUS_PX/DEFAULT_SPACING_SCALE), matching the
// 00013 migration's own column defaults verbatim.
const (
	DefaultTheme        = "venom-dark"
	DefaultDensity      = "comfortable"
	DefaultAccent       = "mono"
	DefaultRadiusPx     = 6
	DefaultSpacingScale = 1.0
)

// The operational defaults (P6-CAPI-001). These are declared HERE, once, and
// mirrored verbatim by 00014's column DEFAULTs — so "what a fresh DB resolves
// to" has exactly one definition. Each equals the constant its consumer used
// to hardcode:
//
//   - DefaultQuotaStalenessSeconds == quota.DefaultStalenessWindow (15m).
//   - The three probe values == intelligence.DefaultProbeSafetyPolicy()'s
//     MaxInFlightPerProvider / ExpensiveProbesEnabled / PerAccountWindow.
//
// They are restated as plain integers rather than imported, because this
// package must not depend on internal/quota or internal/intelligence; the
// equality is asserted by test instead of by import.
const (
	DefaultQuotaStalenessSeconds        = 900   // 15m
	DefaultProbeMaxInFlightPerProvider  = 1     // 04 §2: "max 1 in-flight probe"
	DefaultProbeExpensiveEnabled        = false // 04 §2: expensive probes are opt-in
	DefaultProbePerAccountWindowSeconds = 86400 // 24h rolling window
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
		out                    SettingsRow
		theme, density, accent string
		radiusPx               int
		spacingScale           float64
		enrichmentEnabled      int
		staleness              int
		maxInFlight            int
		expensiveEnabled       int
		accountWindow          int
		updatedAt              int64
	)
	err := r.db.Conn().QueryRowContext(ctx,
		`SELECT theme, density, accent, radius_px, spacing_scale, enrichment_enabled,
			quota_staleness_seconds, probe_max_in_flight_per_provider,
			probe_expensive_enabled, probe_per_account_window_seconds, updated_at
		 FROM owner_settings WHERE id = 1`,
	).Scan(&theme, &density, &accent, &radiusPx, &spacingScale, &enrichmentEnabled,
		&staleness, &maxInFlight, &expensiveEnabled, &accountWindow, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		// A fresh DB resolves to DefaultSettingsRow() — the SAME single
		// definition 00014's column defaults mirror, so "no row yet" and "a
		// row nobody has touched" can never disagree.
		return DefaultSettingsRow(), nil
	}
	if err != nil {
		return SettingsRow{}, fmt.Errorf("storage: get owner_settings: %w", err)
	}

	out.Theme = theme
	out.Density = density
	out.Accent = accent
	out.RadiusPx = radiusPx
	out.SpacingScale = spacingScale
	out.EnrichmentEnabled = enrichmentEnabled != 0
	out.QuotaStalenessSeconds = staleness
	out.ProbeMaxInFlightPerProvider = maxInFlight
	out.ProbeExpensiveEnabled = expensiveEnabled != 0
	out.ProbePerAccountWindowSeconds = accountWindow
	out.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return out, nil
}

// DefaultSettingsRow is the exact SettingsRow a fresh database resolves to.
// It is the ONE place the frozen defaults are assembled, so Get's no-row path,
// the httpapi handler, and the migration's column defaults all describe the
// same install.
func DefaultSettingsRow() SettingsRow {
	return SettingsRow{
		Theme:                        DefaultTheme,
		Density:                      DefaultDensity,
		Accent:                       DefaultAccent,
		RadiusPx:                     DefaultRadiusPx,
		SpacingScale:                 DefaultSpacingScale,
		EnrichmentEnabled:            false,
		QuotaStalenessSeconds:        DefaultQuotaStalenessSeconds,
		ProbeMaxInFlightPerProvider:  DefaultProbeMaxInFlightPerProvider,
		ProbeExpensiveEnabled:        DefaultProbeExpensiveEnabled,
		ProbePerAccountWindowSeconds: DefaultProbePerAccountWindowSeconds,
	}
}

// Put UPSERTs the single owner_settings row (id = 1), stamping updated_at
// = now. It does NOT validate the enums/ranges — that is the httpapi
// handler's job; the owner_settings CHECK constraints are the
// defense-in-depth backstop that bites if an invalid value ever reaches
// this layer. now is the caller-supplied clock (tests inject a fixed
// value); it is stored as an INTEGER epoch (.Unix()), matching the M2–M4
// timestamp convention.
func (r *SettingsRepo) Put(ctx context.Context, theme, density, accent string, radiusPx int, spacingScale float64, now time.Time) error {
	_, err := r.db.Conn().ExecContext(ctx,
		`INSERT INTO owner_settings (id, theme, density, accent, radius_px, spacing_scale, updated_at)
		 VALUES (1, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET theme = excluded.theme, density = excluded.density,
		     accent = excluded.accent, radius_px = excluded.radius_px, spacing_scale = excluded.spacing_scale,
		     updated_at = excluded.updated_at`,
		theme, density, accent, radiusPx, spacingScale, now.Unix(),
	)
	if err != nil {
		return fmt.Errorf("storage: put owner_settings: %w", err)
	}
	return nil
}

// PutEnrichment UPSERTs ONLY the enrichment_enabled column of the single
// owner_settings row (id = 1), stamping updated_at = now, WITHOUT
// disturbing theme/density (P3a-CAPI-003, 04 §2b). When the row does not
// yet exist, it is created with the frozen design-system defaults
// (DefaultTheme/DefaultDensity) alongside the given enabled value; when it
// already exists, the ON CONFLICT clause updates only enrichment_enabled
// and updated_at — theme/density are read back unchanged on the next Get.
// This is the routing-critical/optional split's storage-layer half: this
// method touches nothing the free-safety pipeline (internal/intelligence)
// ever reads.
func (r *SettingsRepo) PutEnrichment(ctx context.Context, enabled bool, now time.Time) error {
	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	_, err := r.db.Conn().ExecContext(ctx,
		`INSERT INTO owner_settings (id, theme, density, enrichment_enabled, updated_at)
		 VALUES (1, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET enrichment_enabled = excluded.enrichment_enabled, updated_at = excluded.updated_at`,
		DefaultTheme, DefaultDensity, enabledInt, now.Unix(),
	)
	if err != nil {
		return fmt.Errorf("storage: put owner_settings enrichment: %w", err)
	}
	return nil
}

// SettingsUpdate is one PUT /settings write (P6-CAPI-001).
//
// The appearance quintet is REQUIRED and always written — that contract is
// unchanged from P2b-CAPI-005. Every other field is a POINTER meaning
// "leave unchanged when nil": the operational knobs and the enrichment toggle
// are independent switches, and a client that sends only `{theme: ...}` must
// not silently reset the owner's probe caps to the defaults.
type SettingsUpdate struct {
	Theme        string
	Density      string
	Accent       string
	RadiusPx     int
	SpacingScale float64

	EnrichmentEnabled            *bool
	QuotaStalenessSeconds        *int
	ProbeMaxInFlightPerProvider  *int
	ProbeExpensiveEnabled        *bool
	ProbePerAccountWindowSeconds *int
}

// PutSettings UPSERTs the single owner_settings row in ONE statement, so
// appearance and operational changes from a single request can never land
// half-applied.
//
// The nil-means-unchanged semantics are expressed with COALESCE against the
// EXISTING row on the conflict branch (never against `excluded`, whose values
// for an absent field are just this function's insert-time defaults). On the
// insert branch there is no existing row to preserve, so an absent field takes
// the frozen default — the same value Get would have reported for it anyway.
//
// It does NOT validate: the httpapi handler is the primary gate and the 00014
// CHECK constraints are the defense-in-depth backstop, exactly as Put's own
// doc comment describes for the appearance columns.
func (r *SettingsRepo) PutSettings(ctx context.Context, u SettingsUpdate, now time.Time) error {
	enrichment := nullableBoolArg(u.EnrichmentEnabled)
	staleness := nullableIntArg(u.QuotaStalenessSeconds)
	maxInFlight := nullableIntArg(u.ProbeMaxInFlightPerProvider)
	expensive := nullableBoolArg(u.ProbeExpensiveEnabled)
	accountWindow := nullableIntArg(u.ProbePerAccountWindowSeconds)

	_, err := r.db.Conn().ExecContext(ctx,
		`INSERT INTO owner_settings (
			id, theme, density, accent, radius_px, spacing_scale, enrichment_enabled,
			quota_staleness_seconds, probe_max_in_flight_per_provider,
			probe_expensive_enabled, probe_per_account_window_seconds, updated_at
		 ) VALUES (
			1, ?, ?, ?, ?, ?, COALESCE(?, 0),
			COALESCE(?, ?), COALESCE(?, ?), COALESCE(?, 0), COALESCE(?, ?), ?
		 )
		 ON CONFLICT(id) DO UPDATE SET
			theme = excluded.theme,
			density = excluded.density,
			accent = excluded.accent,
			radius_px = excluded.radius_px,
			spacing_scale = excluded.spacing_scale,
			enrichment_enabled = COALESCE(?, owner_settings.enrichment_enabled),
			quota_staleness_seconds = COALESCE(?, owner_settings.quota_staleness_seconds),
			probe_max_in_flight_per_provider = COALESCE(?, owner_settings.probe_max_in_flight_per_provider),
			probe_expensive_enabled = COALESCE(?, owner_settings.probe_expensive_enabled),
			probe_per_account_window_seconds = COALESCE(?, owner_settings.probe_per_account_window_seconds),
			updated_at = excluded.updated_at`,
		u.Theme, u.Density, u.Accent, u.RadiusPx, u.SpacingScale, enrichment,
		staleness, DefaultQuotaStalenessSeconds,
		maxInFlight, DefaultProbeMaxInFlightPerProvider,
		expensive,
		accountWindow, DefaultProbePerAccountWindowSeconds,
		now.Unix(),
		enrichment, staleness, maxInFlight, expensive, accountWindow,
	)
	if err != nil {
		return fmt.Errorf("storage: put owner_settings: %w", err)
	}
	return nil
}

// nullableIntArg maps a nil *int to a SQL NULL, which is what makes the
// COALESCE above resolve to "keep what is already there".
func nullableIntArg(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}

// nullableBoolArg maps a nil *bool to a SQL NULL and a non-nil one to the
// 0/1 the INTEGER column stores.
func nullableBoolArg(v *bool) any {
	if v == nil {
		return nil
	}
	if *v {
		return 1
	}
	return 0
}
