-- +goose Up
CREATE TABLE ai_call_usage (
    assistant_message_id INTEGER PRIMARY KEY REFERENCES chat_messages(id) ON DELETE CASCADE,
    user_message_id INTEGER NOT NULL UNIQUE REFERENCES chat_messages(id) ON DELETE CASCADE,
    model TEXT NOT NULL CHECK (length(trim(model)) > 0),
    current_request_tokens INTEGER NOT NULL CHECK (current_request_tokens >= 0),
    history_tokens INTEGER NOT NULL CHECK (history_tokens >= 0),
    system_prompt_tokens INTEGER NOT NULL CHECK (system_prompt_tokens >= 0),
    estimated_input_tokens INTEGER NOT NULL CHECK (estimated_input_tokens >= 0),
    input_tokens INTEGER CHECK (input_tokens IS NULL OR input_tokens >= 0),
    output_tokens INTEGER CHECK (output_tokens IS NULL OR output_tokens >= 0),
    total_tokens INTEGER CHECK (total_tokens IS NULL OR total_tokens >= 0),
    estimated_cost_usd REAL CHECK (estimated_cost_usd IS NULL OR estimated_cost_usd >= 0),
    component_token_count TEXT NOT NULL CHECK (component_token_count = 'estimated'),
    usage_available INTEGER NOT NULL CHECK (usage_available IN (0, 1)),
    usage_inconsistent INTEGER NOT NULL CHECK (usage_inconsistent IN (0, 1)),
    context_status TEXT NOT NULL CHECK (context_status IN ('unknown', 'within_limit', 'approaching_limit')),
    context_window_tokens INTEGER CHECK (context_window_tokens IS NULL OR context_window_tokens > 0),
    history_truncated INTEGER NOT NULL CHECK (history_truncated IN (0, 1)),
    CHECK (user_message_id < assistant_message_id)
);

-- +goose Down
SELECT intentionally_irreversible; -- intentionally irreversible: usage is part of persistent chat history
