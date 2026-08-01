-- Retire the third design-system theme without editing the already-applied
-- owner_settings migration. Existing high-contrast preferences resolve to
-- the dark theme before the table CHECK is narrowed.

-- +goose Up
-- +goose StatementBegin
UPDATE owner_settings SET theme = 'venom-dark' WHERE theme = 'venom-hc';

CREATE TABLE owner_settings_v15 (
    id                                        INTEGER PRIMARY KEY CHECK (id = 1),
    theme                                     TEXT NOT NULL CHECK (theme IN ('venom-dark', 'venom-light')),
    density                                   TEXT NOT NULL CHECK (density IN ('comfortable', 'compact')),
    updated_at                                INTEGER NOT NULL,
    enrichment_enabled                       INTEGER NOT NULL DEFAULT 0,
    accent                                    TEXT NOT NULL DEFAULT 'mono' CHECK (accent IN ('mono', 'blue', 'violet', 'amber', 'emerald', 'rose')),
    radius_px                                 INTEGER NOT NULL DEFAULT 6 CHECK (radius_px BETWEEN 0 AND 16),
    spacing_scale                             REAL NOT NULL DEFAULT 1.0 CHECK (spacing_scale BETWEEN 0.75 AND 1.25),
    quota_staleness_seconds                   INTEGER NOT NULL DEFAULT 900 CHECK (quota_staleness_seconds > 0),
    probe_max_in_flight_per_provider          INTEGER NOT NULL DEFAULT 1 CHECK (probe_max_in_flight_per_provider >= 1),
    probe_expensive_enabled                   INTEGER NOT NULL DEFAULT 0 CHECK (probe_expensive_enabled IN (0, 1)),
    probe_per_account_window_seconds          INTEGER NOT NULL DEFAULT 86400 CHECK (probe_per_account_window_seconds > 0)
);

INSERT INTO owner_settings_v15 (
    id, theme, density, updated_at, enrichment_enabled, accent, radius_px,
    spacing_scale, quota_staleness_seconds, probe_max_in_flight_per_provider,
    probe_expensive_enabled, probe_per_account_window_seconds
)
SELECT
    id, theme, density, updated_at, enrichment_enabled, accent, radius_px,
    spacing_scale, quota_staleness_seconds, probe_max_in_flight_per_provider,
    probe_expensive_enabled, probe_per_account_window_seconds
FROM owner_settings;

DROP TABLE owner_settings;
ALTER TABLE owner_settings_v15 RENAME TO owner_settings;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE TABLE owner_settings_v14 (
    id                                        INTEGER PRIMARY KEY CHECK (id = 1),
    theme                                     TEXT NOT NULL CHECK (theme IN ('venom-dark', 'venom-light', 'venom-hc')),
    density                                   TEXT NOT NULL CHECK (density IN ('comfortable', 'compact')),
    updated_at                                INTEGER NOT NULL,
    enrichment_enabled                       INTEGER NOT NULL DEFAULT 0,
    accent                                    TEXT NOT NULL DEFAULT 'mono' CHECK (accent IN ('mono', 'blue', 'violet', 'amber', 'emerald', 'rose')),
    radius_px                                 INTEGER NOT NULL DEFAULT 6 CHECK (radius_px BETWEEN 0 AND 16),
    spacing_scale                             REAL NOT NULL DEFAULT 1.0 CHECK (spacing_scale BETWEEN 0.75 AND 1.25),
    quota_staleness_seconds                   INTEGER NOT NULL DEFAULT 900 CHECK (quota_staleness_seconds > 0),
    probe_max_in_flight_per_provider          INTEGER NOT NULL DEFAULT 1 CHECK (probe_max_in_flight_per_provider >= 1),
    probe_expensive_enabled                   INTEGER NOT NULL DEFAULT 0 CHECK (probe_expensive_enabled IN (0, 1)),
    probe_per_account_window_seconds          INTEGER NOT NULL DEFAULT 86400 CHECK (probe_per_account_window_seconds > 0)
);

INSERT INTO owner_settings_v14 (
    id, theme, density, updated_at, enrichment_enabled, accent, radius_px,
    spacing_scale, quota_staleness_seconds, probe_max_in_flight_per_provider,
    probe_expensive_enabled, probe_per_account_window_seconds
)
SELECT
    id, theme, density, updated_at, enrichment_enabled, accent, radius_px,
    spacing_scale, quota_staleness_seconds, probe_max_in_flight_per_provider,
    probe_expensive_enabled, probe_per_account_window_seconds
FROM owner_settings;

DROP TABLE owner_settings;
ALTER TABLE owner_settings_v14 RENAME TO owner_settings;
-- +goose StatementEnd
