-- +goose Up
ALTER TABLE tasks ADD COLUMN goal TEXT NOT NULL DEFAULT '';

-- +goose Down
-- Intentionally irreversible: SQLite cannot drop a column without rebuilding the table.
