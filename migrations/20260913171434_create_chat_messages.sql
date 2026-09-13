-- +goose Up
CREATE TABLE chat_messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    role TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
    content TEXT NOT NULL CHECK (length(trim(content)) > 0)
);

-- +goose Down
SELECT intentionally_irreversible; -- intentionally irreversible: persistent chat history must not be dropped
