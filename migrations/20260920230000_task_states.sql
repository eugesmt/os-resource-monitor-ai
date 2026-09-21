-- +goose Up
CREATE TABLE task_states (
    task_id TEXT PRIMARY KEY REFERENCES conversations(id),
    phase TEXT NOT NULL CHECK (phase IN ('planning', 'execution', 'validation', 'done')),
    step TEXT NOT NULL,
    expected_action TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'paused')),
    version INTEGER NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- +goose Down
DROP TABLE task_states;
