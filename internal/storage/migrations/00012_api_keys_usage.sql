-- M7 (P5-DB-001): Venom API keys + usage records (02 §schema, 09 §3.11, 05 §7).
--
-- Secret hygiene is STRUCTURAL, not merely a convention:
--   * venom_api_keys stores ONLY a verifier hash (key_hash = hex(sha256(raw)),
--     64 lowercase hex chars) plus a NON-SECRET display fragment (key_prefix).
--     There is deliberately NO column that can hold a raw key, a plaintext, an
--     encrypted key envelope, a ciphertext, or a bearer token — a Venom key is
--     VERIFIED, never recovered (01 §8, 09 §3.11: "hash-only ... shown once").
--   * usage_records (written later by P5-PAPI-002; created now so that writer
--     needs no follow-up migration) carries only correlation ids, a typed
--     terminal status, and nullable numeric metrics. It has NO column for a
--     prompt, a response, token content, a raw provider error, or an
--     Authorization header (05 §7). tokens_in / tokens_out are integer COUNTS
--     (the X-Venom-Tokens-In/Out metrics, 01 §6c), never token text.
--
-- Nullable-numeric rule (02 §schema): a NULL numeric means UNKNOWN, never 0.
-- rpm_limit NULL = "no explicit per-key limit configured" (the configured
-- default applies at auth time — never "unlimited"); every metric column is
-- nullable so an un-measured dimension is NULL, not a misleading 0.
--
-- usage_records.api_key_id is ON DELETE SET NULL, never CASCADE: revoking or
-- deleting a key must never erase the billing/usage history attributed to it.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE venom_api_keys (
    id           TEXT PRIMARY KEY,
    label        TEXT NOT NULL CHECK (length(trim(label)) > 0),
    key_hash     TEXT NOT NULL UNIQUE CHECK (length(key_hash) = 64),
    key_prefix   TEXT NOT NULL,
    rpm_limit    INTEGER CHECK (rpm_limit IS NULL OR rpm_limit > 0),
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER,
    revoked_at   INTEGER
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE usage_records (
    id                TEXT PRIMARY KEY,
    request_id        TEXT NOT NULL,
    api_key_id        TEXT REFERENCES venom_api_keys(id) ON DELETE SET NULL,
    tier              TEXT NOT NULL CHECK (tier IN ('lite', 'pro', 'max')),
    provider_id       TEXT,
    account_id        TEXT,
    provider_model_id TEXT,
    funding           TEXT,
    status            TEXT NOT NULL,
    latency_ms        INTEGER,
    tokens_in         INTEGER,
    tokens_out        INTEGER,
    fallback_attempts INTEGER,
    created_at        INTEGER NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_usage_records_api_key ON usage_records(api_key_id, created_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_usage_records_created ON usage_records(created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX idx_usage_records_created;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX idx_usage_records_api_key;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE usage_records;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE venom_api_keys;
-- +goose StatementEnd
