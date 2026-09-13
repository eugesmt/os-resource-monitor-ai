-- +goose Up
CREATE TABLE conversation_summary (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    content TEXT NOT NULL CHECK (length(trim(content)) > 0),
    last_message_id INTEGER NOT NULL REFERENCES chat_messages(id),
    updated_at TEXT NOT NULL,
    source_message_count INTEGER NOT NULL CHECK (source_message_count > 0),
    source_tokens_estimate INTEGER NOT NULL CHECK (source_tokens_estimate >= 0),
    summary_tokens_estimate INTEGER NOT NULL CHECK (summary_tokens_estimate >= 0),
    recent_messages_limit INTEGER NOT NULL CHECK (recent_messages_limit > 0),
    version INTEGER NOT NULL CHECK (version > 0)
);
CREATE TABLE ai_summary_usage (
    id INTEGER PRIMARY KEY,
    started_at TEXT NOT NULL,
    finished_at TEXT NOT NULL,
    previous_last_message_id INTEGER NOT NULL,
    through_message_id INTEGER NOT NULL,
    message_count INTEGER NOT NULL CHECK (message_count > 0),
    model TEXT NOT NULL,
    outcome TEXT NOT NULL,
    previous_summary_tokens INTEGER NOT NULL CHECK (previous_summary_tokens >= 0),
    new_messages_tokens INTEGER NOT NULL CHECK (new_messages_tokens >= 0),
    estimated_input_tokens INTEGER NOT NULL CHECK (estimated_input_tokens >= 0),
    input_tokens INTEGER,
    output_tokens INTEGER,
    total_tokens INTEGER,
    result_summary_tokens INTEGER NOT NULL CHECK (result_summary_tokens >= 0),
    summary_saved INTEGER NOT NULL CHECK (summary_saved IN (0, 1))
);
ALTER TABLE ai_call_usage ADD COLUMN summary_tokens INTEGER;
ALTER TABLE ai_call_usage ADD COLUMN recent_history_tokens INTEGER;
ALTER TABLE ai_call_usage ADD COLUMN uncompressed_history_tokens INTEGER;
ALTER TABLE ai_call_usage ADD COLUMN estimated_uncompressed_input_tokens INTEGER;
ALTER TABLE ai_call_usage ADD COLUMN estimated_tokens_saved INTEGER;
ALTER TABLE ai_call_usage ADD COLUMN history_compacted INTEGER;

-- +goose Down
SELECT intentionally_irreversible;
