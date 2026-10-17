-- +goose Up
ALTER TABLE tasks ADD COLUMN response_type TEXT NOT NULL DEFAULT ''
    CHECK (response_type IN ('', 'diagnosis', 'conversation_recall', 'metric_comparison', 'general_answer'));

-- +goose Down
-- Intentionally irreversible: SQLite cannot drop a column without rebuilding the table.
