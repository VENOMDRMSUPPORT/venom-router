-- +goose Up
-- +goose StatementBegin
CREATE TABLE owner_settings (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    theme      TEXT NOT NULL CHECK (theme IN ('venom-dark', 'venom-light', 'venom-hc')),
    density    TEXT NOT NULL CHECK (density IN ('comfortable', 'compact')),
    updated_at INTEGER NOT NULL
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE owner_settings;
-- +goose StatementEnd
