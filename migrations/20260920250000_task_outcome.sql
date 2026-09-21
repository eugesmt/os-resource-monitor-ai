-- +goose Up
ALTER TABLE tasks ADD COLUMN outcome TEXT NOT NULL DEFAULT 'open'
    CHECK (outcome IN ('open', 'completed', 'cancelled'));
UPDATE tasks SET outcome = 'completed' WHERE phase = 'done';

-- +goose Down
-- SQLite cannot safely drop a column without rebuilding the table; this
-- additive task outcome migration is intentionally irreversible.
