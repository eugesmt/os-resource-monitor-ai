-- +goose Up
CREATE TABLE conversations (
    id TEXT PRIMARY KEY,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE conversation_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    conversation_id TEXT NOT NULL REFERENCES conversations(id),
    turn_id TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
    content TEXT NOT NULL CHECK (length(content) > 0),
    snapshot_id TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (conversation_id, turn_id, role)
);

INSERT INTO conversations (id) VALUES ('default');

-- +goose Down
CREATE TEMP TABLE conversation_event_log_rollback_guard (
    reason TEXT CHECK (reason <> 'conversation event log is append-only and irreversible')
);
INSERT INTO conversation_event_log_rollback_guard (reason)
VALUES ('conversation event log is append-only and irreversible');
