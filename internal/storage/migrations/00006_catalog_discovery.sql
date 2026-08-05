-- +goose Up
-- +goose StatementBegin
CREATE TABLE models (
    id                     TEXT PRIMARY KEY,
    canonical_key_sha256   TEXT NOT NULL UNIQUE,
    display_name           TEXT,
    native_context_tokens  INTEGER,   -- ONLY writer: DiscoveryRepo.SetNativeContextTokens (internal/storage/discovery.go)
    native_modalities_json TEXT,
    quality_rating         REAL,
    created_at             INTEGER NOT NULL,
    updated_at             INTEGER NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE provider_model_aliases (
    provider_id       TEXT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
    provider_model_id TEXT NOT NULL,
    model_id          TEXT NOT NULL REFERENCES models(id) ON DELETE CASCADE,
    PRIMARY KEY (provider_id, provider_model_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE account_model_offerings (
    account_id        TEXT NOT NULL,
    provider_id       TEXT NOT NULL,
    provider_model_id TEXT NOT NULL,
    model_id          TEXT NOT NULL REFERENCES models(id),
    availability      TEXT NOT NULL
                      CHECK (availability IN ('available', 'withdrawn', 'unknown')),
    context_length    INTEGER,
    max_input_tokens  INTEGER,
    max_output_tokens INTEGER,
    capabilities_json TEXT,
    pricing_json      TEXT,
    lifecycle_json    TEXT,
    first_seen_at     INTEGER NOT NULL,
    last_seen_at      INTEGER NOT NULL,
    UNIQUE (account_id, provider_model_id),
    FOREIGN KEY (account_id, provider_id) REFERENCES accounts(id, provider_id) ON DELETE CASCADE
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_account_model_offerings_account
    ON account_model_offerings(account_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE offering_operations (
    id                TEXT PRIMARY KEY,
    account_id        TEXT NOT NULL,
    provider_id       TEXT NOT NULL,
    provider_model_id TEXT NOT NULL,
    operation         TEXT NOT NULL,
    created_at        INTEGER NOT NULL,
    updated_at        INTEGER NOT NULL,
    UNIQUE (account_id, provider_model_id, operation),
    FOREIGN KEY (account_id, provider_id) REFERENCES accounts(id, provider_id) ON DELETE CASCADE,
    FOREIGN KEY (account_id, provider_model_id)
        REFERENCES account_model_offerings(account_id, provider_model_id) ON DELETE CASCADE
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_offering_operations_offering
    ON offering_operations(account_id, provider_model_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE certifications (
    offering_operation_id TEXT PRIMARY KEY
                          REFERENCES offering_operations(id) ON DELETE CASCADE,
    status                TEXT NOT NULL DEFAULT 'discovered'
                          CHECK (status IN ('discovered', 'observed', 'probing', 'certified', 'suspended', 'expired')),
    capability_truth      TEXT NOT NULL DEFAULT 'unknown'
                          CHECK (capability_truth IN ('unknown', 'supported', 'unsupported')),
    version               INTEGER NOT NULL DEFAULT 1,
    certified_at          INTEGER,
    evidence_ref          TEXT,
    created_at            INTEGER NOT NULL,
    updated_at            INTEGER NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_certifications_status
    ON certifications(status);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE discovery_runs (
    id          TEXT PRIMARY KEY,
    account_id  TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    generation  INTEGER NOT NULL,
    status      TEXT NOT NULL
                CHECK (status IN ('running', 'applied', 'superseded', 'failed')),
    reason_code TEXT,
    started_at  INTEGER NOT NULL,
    finished_at INTEGER,
    UNIQUE (account_id, generation)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE discovery_runs;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX idx_certifications_status;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE certifications;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX idx_offering_operations_offering;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE offering_operations;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX idx_account_model_offerings_account;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE account_model_offerings;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE provider_model_aliases;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE models;
-- +goose StatementEnd
