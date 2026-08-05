-- +goose Up
-- +goose StatementBegin
CREATE TABLE benchmark_runs (
    id                TEXT PRIMARY KEY,
    model_id          TEXT NOT NULL REFERENCES models(id) ON DELETE CASCADE,
    account_id        TEXT NOT NULL,
    provider_id       TEXT NOT NULL,
    provider_model_id TEXT NOT NULL,
    requests          INTEGER NOT NULL,
    successes         INTEGER NOT NULL,
    ttft_ms           INTEGER,          -- median across successful requests; NULL if none succeeded
    tokens_per_sec    REAL,             -- median across successful requests; NULL if none succeeded
    rating            REAL,             -- the derived 0..1 score; NULL when the success gate failed
    started_at        INTEGER NOT NULL,
    finished_at       INTEGER NOT NULL,
    created_at        INTEGER NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_benchmark_runs_model ON benchmark_runs(model_id, finished_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX idx_benchmark_runs_model;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE benchmark_runs;
-- +goose StatementEnd
