-- +goose Up
-- +goose StatementBegin
CREATE TABLE quota_windows (
    id               TEXT PRIMARY KEY,
    account_id       TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    source           TEXT NOT NULL
                     CHECK (source IN ('provider_evidence', 'local_safety', 'owner_override')),
    unit             TEXT NOT NULL,
    window_type      TEXT NOT NULL,
    window_key       TEXT NOT NULL CHECK (window_key <> ''),
    duration_seconds INTEGER,
    used             REAL,
    remaining        REAL,
    total            REAL,
    reserved         REAL NOT NULL DEFAULT 0,
    limit_value      REAL,
    reset_at         INTEGER,
    version          INTEGER NOT NULL DEFAULT 1,
    confidence       REAL NOT NULL,
    freshness_state  TEXT NOT NULL
                     CHECK (freshness_state IN ('fresh', 'stale', 'unknown')),
    observed_at      INTEGER NOT NULL,
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL,
    UNIQUE (account_id, source, unit, window_type, window_key)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_quota_windows_account
    ON quota_windows(account_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE quota_reservations (
    id            TEXT PRIMARY KEY,
    account_id    TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    request_id    TEXT NOT NULL,
    attempt_id    TEXT NOT NULL,
    state         TEXT NOT NULL
                  CHECK (state IN ('reserved', 'reconciliation_pending', 'settled',
                                    'released', 'unknown_consumption')),
    dispatched_at INTEGER,          -- stamped before the provider call; NULL = never dispatched (janitor branch discriminator)
    expires_at    INTEGER NOT NULL, -- processing deadline, never a terminal state
    created_at    INTEGER NOT NULL,
    settled_at    INTEGER,
    UNIQUE (request_id, attempt_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_quota_reservations_deadline
    ON quota_reservations(state, expires_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE quota_reservation_allocations (
    reservation_id  TEXT NOT NULL REFERENCES quota_reservations(id) ON DELETE CASCADE,
    window_id       TEXT NOT NULL REFERENCES quota_windows(id),
    unit            TEXT NOT NULL,
    estimated_cost  REAL NOT NULL,
    estimate_source TEXT NOT NULL
                    CHECK (estimate_source IN ('from_request', 'provider_conversion', 'policy_default')),
    actual_cost     REAL,
    state           TEXT NOT NULL
                    CHECK (state IN ('reserved', 'settled', 'released', 'unknown_consumption')),
    PRIMARY KEY (reservation_id, window_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE cooldowns (
    id                    TEXT PRIMARY KEY,
    scope                 TEXT NOT NULL CHECK (scope IN ('account', 'offering', 'provider')),
    account_id            TEXT REFERENCES accounts(id) ON DELETE CASCADE,
    offering_operation_id TEXT REFERENCES offering_operations(id) ON DELETE CASCADE,
    provider_id           TEXT REFERENCES providers(id) ON DELETE CASCADE,
    reason_code           TEXT NOT NULL,
    until                 INTEGER NOT NULL,
    source                TEXT NOT NULL CHECK (source IN ('retry_after', 'default_backoff')),
    created_at            INTEGER NOT NULL,
    updated_at            INTEGER NOT NULL,
    CHECK (
        (scope = 'account'  AND account_id IS NOT NULL AND offering_operation_id IS NULL     AND provider_id IS NULL)
        OR (scope = 'offering' AND account_id IS NULL     AND offering_operation_id IS NOT NULL AND provider_id IS NULL)
        OR (scope = 'provider' AND account_id IS NULL     AND offering_operation_id IS NULL     AND provider_id IS NOT NULL)
    )
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX idx_cooldowns_account
    ON cooldowns(account_id) WHERE account_id IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX idx_cooldowns_offering
    ON cooldowns(offering_operation_id) WHERE offering_operation_id IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX idx_cooldowns_provider
    ON cooldowns(provider_id) WHERE provider_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX idx_cooldowns_provider;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX idx_cooldowns_offering;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX idx_cooldowns_account;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE cooldowns;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE quota_reservation_allocations;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX idx_quota_reservations_deadline;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE quota_reservations;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX idx_quota_windows_account;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE quota_windows;
-- +goose StatementEnd
