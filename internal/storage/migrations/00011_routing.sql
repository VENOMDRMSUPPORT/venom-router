-- M6 (P4-DB-001): route records + routing state (05 §7, 02 §3).
--
-- Privacy is structural (05 §7): route_decisions and route_attempts carry
-- ONLY correlation ids, typed reason codes, scores, clamp flags, and
-- normalized statuses — there are NO columns for prompts, responses, token
-- contents, raw provider errors, or authorization material, so storing
-- them is impossible. Session stickiness is deliberately absent: it is an
-- in-memory LRU only (05 §2 Step 7), never persisted.
--
-- CHECK policy mirrors 00010's precedent: vocabularies owned by this
-- migration's own routing domain (tier, breaker scope, deficit
-- funding_class — all spec-closed in 05 §1/§3/§8.1) get CHECKs;
-- circuit_breakers.state does NOT — its vocabulary is owned by the breaker
-- unit (P4-ROUTE-014), not duplicated as a second source of truth here
-- (the same call M4 made for offering_operations.operation).

-- +goose Up
-- +goose StatementBegin
CREATE TABLE route_decisions (
    id                       TEXT PRIMARY KEY,
    request_id               TEXT NOT NULL,
    tier                     TEXT NOT NULL CHECK (tier IN ('lite', 'pro', 'max')),
    workload_profile_bucket  TEXT NOT NULL,
    -- candidate-set summary and exclusion reason codes: JSON, secret-free
    -- (counts, typed reason codes, group keys — never provider payloads).
    candidate_summary        TEXT NOT NULL,
    exclusion_reasons        TEXT NOT NULL,
    chosen_provider_id       TEXT,
    chosen_provider_model_id TEXT,
    chosen_funding           TEXT,
    scores                   TEXT,
    requested_thinking       TEXT,
    applied_thinking         TEXT,
    tier_clamped             INTEGER NOT NULL DEFAULT 0,
    certified_clamped        INTEGER NOT NULL DEFAULT 0,
    created_at               INTEGER NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_route_decisions_tier_created
    ON route_decisions(tier, created_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_route_decisions_request
    ON route_decisions(request_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE route_attempts (
    id                    TEXT PRIMARY KEY,
    route_decision_id     TEXT NOT NULL REFERENCES route_decisions(id) ON DELETE CASCADE,
    attempt_number        INTEGER NOT NULL,
    provider_id           TEXT NOT NULL,
    account_id            TEXT NOT NULL,
    offering_operation_id TEXT NOT NULL,
    latency_ms            INTEGER,
    status                TEXT NOT NULL,
    thinking_clamped      INTEGER NOT NULL DEFAULT 0,
    reservation_id        TEXT,
    started_at            INTEGER NOT NULL,
    finished_at           INTEGER
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_route_attempts_decision
    ON route_attempts(route_decision_id, attempt_number);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_route_attempts_account_started
    ON route_attempts(account_id, started_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE circuit_breakers (
    -- scope is the spec-closed breaker-scope vocabulary of 05 §3.
    scope                TEXT NOT NULL CHECK (scope IN ('account', 'offering', 'provider')),
    scope_id             TEXT NOT NULL,
    state                TEXT NOT NULL,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    opened_at            INTEGER,
    backoff_multiplier   INTEGER NOT NULL DEFAULT 1,
    next_probe_at        INTEGER,
    PRIMARY KEY (scope, scope_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE deficit_cells (
    tier                    TEXT NOT NULL CHECK (tier IN ('lite', 'pro', 'max')),
    workload_profile_bucket TEXT NOT NULL,
    -- funding_class is spec-closed to free|paid (05 §8.1): deficit cells
    -- exist only for classified funding, never unknown.
    funding_class           TEXT NOT NULL CHECK (funding_class IN ('free', 'paid')),
    deficit                 REAL NOT NULL DEFAULT 0,
    updated_at              INTEGER NOT NULL,
    PRIMARY KEY (tier, workload_profile_bucket, funding_class)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE deficit_cells;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE circuit_breakers;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX idx_route_attempts_account_started;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX idx_route_attempts_decision;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE route_attempts;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX idx_route_decisions_request;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX idx_route_decisions_tier_created;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE route_decisions;
-- +goose StatementEnd
