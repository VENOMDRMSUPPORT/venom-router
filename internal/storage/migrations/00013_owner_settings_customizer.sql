-- +goose Up
-- +goose StatementBegin
ALTER TABLE owner_settings ADD COLUMN accent TEXT NOT NULL DEFAULT 'mono' CHECK (accent IN ('mono', 'blue', 'violet', 'amber', 'emerald', 'rose'));
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE owner_settings ADD COLUMN radius_px INTEGER NOT NULL DEFAULT 6 CHECK (radius_px BETWEEN 0 AND 16);
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE owner_settings ADD COLUMN spacing_scale REAL NOT NULL DEFAULT 1.0 CHECK (spacing_scale BETWEEN 0.75 AND 1.25);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE owner_settings DROP COLUMN spacing_scale;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE owner_settings DROP COLUMN radius_px;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE owner_settings DROP COLUMN accent;
-- +goose StatementEnd
