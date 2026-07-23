-- +goose Up
-- +goose StatementBegin
CREATE TABLE venom_schema_baseline (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    established_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO venom_schema_baseline (id) VALUES (1);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE venom_schema_baseline;
-- +goose StatementEnd
