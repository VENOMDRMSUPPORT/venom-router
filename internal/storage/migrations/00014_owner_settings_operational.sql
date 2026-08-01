-- M8 (P6-CAPI-001): owner-configurable OPERATIONAL settings (09 §2, 05 §4, 04 §2).
--
-- Every column's DEFAULT is exactly the value that is hardcoded in Go today,
-- so an install that upgrades across this migration keeps its current
-- behaviour byte for byte and only changes when the owner changes it:
--   quota_staleness_seconds          = quota.DefaultStalenessWindow (15m = 900s)
--   probe_max_in_flight_per_provider = DefaultProbeSafetyPolicy().MaxInFlightPerProvider (1)
--   probe_expensive_enabled          = DefaultProbeSafetyPolicy().ExpensiveProbesEnabled (false)
--   probe_per_account_window_seconds = DefaultProbeSafetyPolicy().PerAccountWindow (24h = 86400s)
--
-- CHECK policy mirrors 00013's: these vocabularies/ranges are owned by this
-- settings surface itself, so each gets a DB-level CHECK as the
-- defense-in-depth backstop behind the httpapi validation.
--
-- probe_max_in_flight_per_provider is >= 1, NOT >= 0: intelligence.NewProbeGuard
-- already rejects a policy below 1, and a stored 0 would mean every probe
-- refuses itself forever with no way to tell that from a real concurrency
-- refusal.
--
-- The listen binds (01 §6b) are deliberately NOT here. They are resolved at
-- boot from default -> env -> flag, so a DB copy could not take effect without
-- a restart and would be a second source of truth. GET /settings reports them
-- read-only under `effective_config`.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE owner_settings ADD COLUMN quota_staleness_seconds INTEGER NOT NULL DEFAULT 900 CHECK (quota_staleness_seconds > 0);
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE owner_settings ADD COLUMN probe_max_in_flight_per_provider INTEGER NOT NULL DEFAULT 1 CHECK (probe_max_in_flight_per_provider >= 1);
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE owner_settings ADD COLUMN probe_expensive_enabled INTEGER NOT NULL DEFAULT 0 CHECK (probe_expensive_enabled IN (0, 1));
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE owner_settings ADD COLUMN probe_per_account_window_seconds INTEGER NOT NULL DEFAULT 86400 CHECK (probe_per_account_window_seconds > 0);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE owner_settings DROP COLUMN probe_per_account_window_seconds;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE owner_settings DROP COLUMN probe_expensive_enabled;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE owner_settings DROP COLUMN probe_max_in_flight_per_provider;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE owner_settings DROP COLUMN quota_staleness_seconds;
-- +goose StatementEnd
