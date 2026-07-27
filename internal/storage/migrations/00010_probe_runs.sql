-- +goose Up
-- +goose StatementBegin
CREATE TABLE probe_runs (
    id                     TEXT PRIMARY KEY,
    offering_operation_id  TEXT NOT NULL REFERENCES offering_operations(id) ON DELETE CASCADE,
    account_id             TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    provider_id            TEXT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
    -- operation deliberately has NO CHECK, matching the call M4 made for
    -- offering_operations.operation (00006_catalog_discovery.sql) — the
    -- operation vocabulary is owned by internal/models, not duplicated as
    -- a second source of truth here.
    operation              TEXT NOT NULL,
    probe_class            TEXT NOT NULL CHECK (probe_class IN ('standard', 'expensive')),
    -- execution mirrors intelligence.ProbeExecution's closed six-value
    -- vocabulary verbatim (04 §2) — an internal classification, so unlike
    -- operation above it DOES get a CHECK.
    execution              TEXT NOT NULL CHECK (execution IN ('pending', 'running', 'succeeded', 'inconclusive', 'retryable_failure', 'terminal_failure')),
    reservation_id         TEXT,
    started_at             INTEGER NOT NULL,
    finished_at            INTEGER
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_probe_runs_account_started
    ON probe_runs(account_id, started_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_probe_runs_provider_execution
    ON probe_runs(provider_id, execution);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_probe_runs_cooldown
    ON probe_runs(offering_operation_id, operation, started_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE probe_run_costs (
    probe_run_id TEXT NOT NULL REFERENCES probe_runs(id) ON DELETE CASCADE,
    unit         TEXT NOT NULL,
    cost         REAL NOT NULL,
    PRIMARY KEY (probe_run_id, unit)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE probe_run_costs;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX idx_probe_runs_cooldown;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX idx_probe_runs_provider_execution;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX idx_probe_runs_account_started;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE probe_runs;
-- +goose StatementEnd
