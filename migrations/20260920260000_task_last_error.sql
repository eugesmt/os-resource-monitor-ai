-- +goose Up
ALTER TABLE tasks ADD COLUMN last_error TEXT NOT NULL DEFAULT '';

-- +goose Down
-- Intentionally irreversible: SQLite cannot drop a column without rebuilding the table.
