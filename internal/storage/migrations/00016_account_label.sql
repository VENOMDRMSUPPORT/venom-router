-- Optional owner-supplied human-readable label for an account (P6 off-tracker).
-- A null label falls back to a "#account NN" display in the dashboard.

-- +goose Up
ALTER TABLE accounts ADD COLUMN label TEXT;

-- +goose Down
-- modernc.org/sqlite (SQLite >= 3.35) supports DROP COLUMN, and `label` is a
-- plain column in no index or constraint, so the down is a faithful inverse of
-- the up -- matching the DROP COLUMN convention used by 00007/00009/00013/00014.
-- A hand-written table reconstruction here would have to re-declare every
-- accounts DEFAULT, CHECK, and UNIQUE from 00003 and silently drifts if any is
-- missed (it did: a lost DEFAULT 'connecting' and UNIQUE(id, provider_id)).
ALTER TABLE accounts DROP COLUMN label;
