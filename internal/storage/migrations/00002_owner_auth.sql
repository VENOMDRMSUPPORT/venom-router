-- +goose Up
-- +goose StatementBegin
CREATE TABLE owner_auth (
    id            INTEGER PRIMARY KEY CHECK (id = 1),
    password_hash BLOB NOT NULL,
    salt          BLOB NOT NULL,
    kdf_time      INTEGER NOT NULL CHECK (kdf_time > 0),
    kdf_mem_kib   INTEGER NOT NULL CHECK (kdf_mem_kib > 0),
    kdf_threads   INTEGER NOT NULL CHECK (kdf_threads > 0),
    kdf_key_len   INTEGER NOT NULL CHECK (kdf_key_len > 0),
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE owner_sessions (
    id                   INTEGER PRIMARY KEY,
    token_hash           BLOB NOT NULL UNIQUE,
    created_at           TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_seen_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    idle_expires_at      TEXT NOT NULL,
    absolute_expires_at  TEXT NOT NULL,
    revoked_at           TEXT NULL,
    reverify_fresh_until TEXT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE auth_events (
    id          INTEGER PRIMARY KEY,
    action      TEXT NOT NULL,
    result      TEXT NOT NULL,
    reason_code TEXT NULL,
    at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER auth_events_no_update
BEFORE UPDATE ON auth_events
BEGIN
    SELECT RAISE(ABORT, 'auth_events is append-only');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER auth_events_no_delete
BEFORE DELETE ON auth_events
BEGIN
    SELECT RAISE(ABORT, 'auth_events is append-only');
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER auth_events_no_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER auth_events_no_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE auth_events;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE owner_sessions;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE owner_auth;
-- +goose StatementEnd
