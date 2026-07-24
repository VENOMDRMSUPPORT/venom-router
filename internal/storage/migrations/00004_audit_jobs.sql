-- +goose Up
-- +goose StatementBegin
CREATE TABLE audit_events (
    id          INTEGER PRIMARY KEY,
    action      TEXT NOT NULL,
    entity_type TEXT,
    entity_id   TEXT,
    result      TEXT NOT NULL,
    reason_code TEXT,
    at          INTEGER NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER audit_events_no_update
BEFORE UPDATE ON audit_events
BEGIN
    SELECT RAISE(ABORT, 'audit_events is append-only');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER audit_events_no_delete
BEFORE DELETE ON audit_events
BEGIN
    SELECT RAISE(ABORT, 'audit_events is append-only');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE jobs (
    id              TEXT PRIMARY KEY,
    kind            TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'running', 'completed', 'failed', 'expired')),
    started_at      INTEGER,
    finished_at     INTEGER,
    result_ref      TEXT,
    error           TEXT,
    created_at      INTEGER NOT NULL,
    retention_until INTEGER
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE jobs;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER audit_events_no_delete;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER audit_events_no_update;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE audit_events;
-- +goose StatementEnd
