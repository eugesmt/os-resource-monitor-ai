-- +goose NO TRANSACTION
-- +goose Up
PRAGMA foreign_keys = OFF;

CREATE TABLE tasks_rebuilt (
    task_id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES conversations(id),
    goal TEXT NOT NULL DEFAULT '',
    phase TEXT NOT NULL CHECK (phase IN ('planning', 'execution', 'validation', 'done')),
    step TEXT NOT NULL,
    expected_action_kind TEXT NOT NULL CHECK (expected_action_kind IN ('', 'agent', 'user', 'tool', 'none')),
    expected_action TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'paused')),
    outcome TEXT NOT NULL DEFAULT 'open' CHECK (outcome IN ('open', 'completed', 'cancelled')),
    last_error TEXT NOT NULL DEFAULT '',
    response_type TEXT NOT NULL DEFAULT '' CHECK (response_type IN ('', 'diagnosis', 'conversation_recall', 'metric_comparison', 'general_answer', 'invariant_conflict')),
    version INTEGER NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

INSERT INTO tasks_rebuilt (task_id, conversation_id, goal, phase, step, expected_action_kind, expected_action, status, outcome, last_error, response_type, version, created_at, updated_at)
SELECT task_id, conversation_id, goal, phase, step, expected_action_kind, expected_action, status, outcome, last_error, response_type, version, created_at, updated_at FROM tasks;

DROP TABLE tasks;
ALTER TABLE tasks_rebuilt RENAME TO tasks;
CREATE UNIQUE INDEX tasks_one_open_per_conversation ON tasks (conversation_id) WHERE phase <> 'done';

PRAGMA foreign_keys = ON;

-- +goose Down
-- Intentionally irreversible: reverting would make persisted invariant_conflict task states invalid.
