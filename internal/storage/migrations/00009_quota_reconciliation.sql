-- +goose Up
-- +goose StatementBegin
ALTER TABLE quota_reservations ADD COLUMN reconcile_attempts INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE quota_reservations ADD COLUMN lease_owner TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE quota_reservations ADD COLUMN lease_expires_at INTEGER;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE quota_reservation_allocations ADD COLUMN actual_confidence TEXT
    CHECK (actual_confidence IS NULL OR actual_confidence IN ('high', 'low'));
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE quota_rebaseline_flags (
    account_id  TEXT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    reason_code TEXT NOT NULL,
    flagged_at  INTEGER NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_quota_reservations_lease
    ON quota_reservations(state, lease_expires_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX idx_quota_reservations_lease;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE quota_rebaseline_flags;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE quota_reservation_allocations DROP COLUMN actual_confidence;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE quota_reservations DROP COLUMN lease_expires_at;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE quota_reservations DROP COLUMN lease_owner;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE quota_reservations DROP COLUMN reconcile_attempts;
-- +goose StatementEnd
