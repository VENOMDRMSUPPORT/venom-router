-- +goose Up
-- +goose StatementBegin
ALTER TABLE owner_settings ADD COLUMN enrichment_enabled INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE owner_settings DROP COLUMN enrichment_enabled;
-- +goose StatementEnd
