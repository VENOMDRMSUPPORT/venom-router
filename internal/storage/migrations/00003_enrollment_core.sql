-- +goose Up
-- +goose StatementBegin
CREATE TABLE providers (
    id                   TEXT PRIMARY KEY,
    display_name         TEXT NOT NULL,
    description          TEXT,
    auth_mode            TEXT NOT NULL
                         CHECK (auth_mode IN ('oauth2', 'api_key', 'custom_openai')),
    base_url             TEXT,
    settings_json        TEXT,
    funding_mode         TEXT NOT NULL
                         CHECK (funding_mode IN ('fixed', 'owner_policy', 'provider_evidence', 'evidence_required')),
    funding_fixed        TEXT
                         CHECK (funding_fixed IS NULL OR funding_fixed IN ('free', 'paid', 'unknown')),
    funding_locked       INTEGER NOT NULL DEFAULT 0,
    funding_non_expiring INTEGER NOT NULL DEFAULT 0,
    created_at           INTEGER NOT NULL,
    updated_at           INTEGER NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE accounts (
    id                   TEXT PRIMARY KEY,
    provider_id          TEXT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
    external_id          TEXT NOT NULL,
    display_name         TEXT,
    auth_type            TEXT NOT NULL
                         CHECK (auth_type IN ('api_key', 'oauth2')),
    connection_state     TEXT NOT NULL DEFAULT 'connecting'
                         CHECK (connection_state IN ('connecting', 'connected', 'stopped', 'disconnected')),
    health_state         TEXT NOT NULL DEFAULT 'unknown'
                         CHECK (health_state IN ('unknown', 'healthy', 'degraded', 'unavailable', 'expired')),
    reauth_in_progress   INTEGER NOT NULL DEFAULT 0,
    identity_email       TEXT,
    identity_plan        TEXT,
    last_health_check_at INTEGER,
    last_health_error    TEXT,
    created_at           INTEGER NOT NULL,
    updated_at           INTEGER NOT NULL,
    UNIQUE (provider_id, external_id),
    UNIQUE (id, provider_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE account_credentials (
    id                 TEXT PRIMARY KEY,
    account_id         TEXT NOT NULL,
    provider_id        TEXT NOT NULL,
    kind               TEXT NOT NULL
                       CHECK (kind IN ('api_key', 'oauth2', 'github_oauth', 'copilot_service')),
    state              TEXT NOT NULL DEFAULT 'active'
                       CHECK (state IN ('active', 'staged', 'retired')),
    fingerprint_sha256 TEXT NOT NULL,
    key_id             TEXT NOT NULL,
    nonce              BLOB NOT NULL,
    ciphertext         BLOB NOT NULL,
    expires_at         INTEGER,
    created_at         INTEGER NOT NULL,
    updated_at         INTEGER NOT NULL,
    retired_at         INTEGER,
    FOREIGN KEY (account_id, provider_id) REFERENCES accounts(id, provider_id) ON DELETE CASCADE
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX idx_cred_active_per_kind
    ON account_credentials(account_id, kind) WHERE state = 'active';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX idx_cred_staged_per_kind
    ON account_credentials(account_id, kind) WHERE state = 'staged';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX idx_cred_fingerprint
    ON account_credentials(provider_id, fingerprint_sha256) WHERE state != 'retired';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE account_funding_evidence (
    id            TEXT PRIMARY KEY,
    account_id    TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    funding       TEXT NOT NULL
                 CHECK (funding IN ('free', 'paid', 'unknown')),
    source        TEXT NOT NULL
                 CHECK (source IN ('provider_policy', 'provider_evidence', 'owner_policy', 'owner_override')),
    locked        INTEGER NOT NULL DEFAULT 0,
    confidence    REAL NOT NULL,
    evidence_json TEXT,
    reason        TEXT,
    observed_at   INTEGER NOT NULL,
    superseded_at INTEGER
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX idx_funding_current
    ON account_funding_evidence(account_id) WHERE superseded_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE oauth_transactions (
    state_sha256   TEXT PRIMARY KEY,
    provider_id    TEXT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
    transaction_id TEXT NOT NULL,
    key_id         TEXT NOT NULL,
    nonce          BLOB NOT NULL,
    ciphertext     BLOB NOT NULL,
    expires_at     INTEGER NOT NULL,
    consumed       INTEGER NOT NULL DEFAULT 0,
    created_at     INTEGER NOT NULL
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE oauth_transactions;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX idx_funding_current;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE account_funding_evidence;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX idx_cred_fingerprint;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX idx_cred_staged_per_kind;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX idx_cred_active_per_kind;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE account_credentials;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE accounts;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE providers;
-- +goose StatementEnd
