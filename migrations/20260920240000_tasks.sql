-- +goose Up
CREATE TABLE tasks (
    task_id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES conversations(id),
    phase TEXT NOT NULL CHECK (phase IN ('planning', 'execution', 'validation', 'done')),
    step TEXT NOT NULL,
    expected_action_kind TEXT NOT NULL CHECK (expected_action_kind IN ('', 'agent', 'user', 'tool', 'none')),
    expected_action TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'paused')),
    version INTEGER NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE UNIQUE INDEX tasks_one_open_per_conversation
    ON tasks (conversation_id) WHERE phase <> 'done';

CREATE TABLE task_transition_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id TEXT NOT NULL REFERENCES tasks(task_id),
    action TEXT NOT NULL,
    from_phase TEXT NOT NULL,
    to_phase TEXT NOT NULL,
    from_status TEXT NOT NULL,
    to_status TEXT NOT NULL,
    step TEXT NOT NULL,
    expected_action TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- +goose Down
DROP TABLE task_transition_events;
DROP INDEX tasks_one_open_per_conversation;
DROP TABLE tasks;
