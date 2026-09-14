-- +goose Up
PRAGMA foreign_keys = ON;

CREATE TABLE conversations (
    id INTEGER PRIMARY KEY,
    created_at TEXT NOT NULL
);
CREATE TABLE dialogue_branches (
    id INTEGER PRIMARY KEY,
    conversation_id INTEGER NOT NULL REFERENCES conversations(id),
    name TEXT,
    created_at TEXT NOT NULL,
    UNIQUE(id, conversation_id)
);
CREATE TABLE dialogue_checkpoints (
    id INTEGER PRIMARY KEY,
    conversation_id INTEGER NOT NULL,
    branch_id INTEGER NOT NULL,
    message_id INTEGER NOT NULL REFERENCES chat_messages(id),
    label TEXT,
    created_at TEXT NOT NULL,
    UNIQUE(id, conversation_id, branch_id),
    UNIQUE(branch_id, message_id),
    FOREIGN KEY (branch_id, conversation_id) REFERENCES dialogue_branches(id, conversation_id)
);
CREATE TABLE dialogue_branch_origins (
    child_branch_id INTEGER PRIMARY KEY,
    conversation_id INTEGER NOT NULL,
    parent_branch_id INTEGER NOT NULL,
    checkpoint_id INTEGER NOT NULL,
    FOREIGN KEY (child_branch_id, conversation_id) REFERENCES dialogue_branches(id, conversation_id),
    FOREIGN KEY (parent_branch_id, conversation_id) REFERENCES dialogue_branches(id, conversation_id),
    FOREIGN KEY (checkpoint_id, conversation_id, parent_branch_id) REFERENCES dialogue_checkpoints(id, conversation_id, branch_id),
    CHECK (child_branch_id <> parent_branch_id)
);
CREATE TABLE conversation_state (
    conversation_id INTEGER PRIMARY KEY REFERENCES conversations(id),
    active_branch_id INTEGER NOT NULL,
    version INTEGER NOT NULL CHECK (version > 0),
    FOREIGN KEY (active_branch_id, conversation_id) REFERENCES dialogue_branches(id, conversation_id)
);
CREATE TABLE chat_message_branches (
    message_id INTEGER PRIMARY KEY REFERENCES chat_messages(id) ON DELETE CASCADE,
    conversation_id INTEGER NOT NULL,
    branch_id INTEGER NOT NULL,
    FOREIGN KEY (branch_id, conversation_id) REFERENCES dialogue_branches(id, conversation_id)
);
CREATE INDEX idx_message_branches_branch_message ON chat_message_branches(branch_id, message_id);
CREATE INDEX idx_branch_origins_parent ON dialogue_branch_origins(parent_branch_id, checkpoint_id);
CREATE INDEX idx_branches_conversation ON dialogue_branches(conversation_id, id);

CREATE TABLE dialogue_facts (
    id INTEGER PRIMARY KEY,
    conversation_id INTEGER NOT NULL,
    branch_id INTEGER NOT NULL,
    fact_key TEXT NOT NULL,
    category TEXT NOT NULL,
    value_text TEXT,
    state TEXT NOT NULL CHECK (state IN ('active', 'invalidated', 'conflicted')),
    source_message_id INTEGER REFERENCES chat_messages(id),
    version INTEGER NOT NULL CHECK (version > 0),
    updated_at TEXT NOT NULL,
    expires_at TEXT,
    inherited_from_fact_id INTEGER,
    UNIQUE(branch_id, fact_key),
    FOREIGN KEY (branch_id, conversation_id) REFERENCES dialogue_branches(id, conversation_id)
);
CREATE TABLE dialogue_fact_revisions (
    id INTEGER PRIMARY KEY,
    fact_id INTEGER NOT NULL REFERENCES dialogue_facts(id),
    operation TEXT NOT NULL,
    old_value_text TEXT,
    new_value_text TEXT,
    source_message_id INTEGER NOT NULL REFERENCES chat_messages(id),
    created_at TEXT NOT NULL
);
CREATE TABLE ai_fact_extraction_usage (
    id INTEGER PRIMARY KEY,
    conversation_id INTEGER NOT NULL,
    branch_id INTEGER NOT NULL,
    user_message_id INTEGER REFERENCES chat_messages(id),
    started_at TEXT NOT NULL,
    finished_at TEXT NOT NULL,
    model TEXT NOT NULL,
    outcome TEXT NOT NULL,
    proposed_mutations INTEGER NOT NULL CHECK (proposed_mutations >= 0),
    input_tokens INTEGER,
    output_tokens INTEGER,
    total_tokens INTEGER,
    estimated_input_tokens INTEGER NOT NULL CHECK (estimated_input_tokens >= 0),
    estimated_cost_usd REAL,
    applied INTEGER NOT NULL CHECK (applied IN (0, 1)),
    FOREIGN KEY (branch_id, conversation_id) REFERENCES dialogue_branches(id, conversation_id)
);

INSERT INTO conversations(id, created_at) VALUES (1, datetime('now'));
INSERT INTO dialogue_branches(id, conversation_id, name, created_at) VALUES (1, 1, 'root', datetime('now'));
INSERT INTO conversation_state(conversation_id, active_branch_id, version) VALUES (1, 1, 1);
INSERT INTO chat_message_branches(message_id, conversation_id, branch_id)
    SELECT id, 1, 1 FROM chat_messages;

ALTER TABLE ai_call_usage ADD COLUMN context_strategy TEXT;
ALTER TABLE ai_call_usage ADD COLUMN branch_id INTEGER;
ALTER TABLE ai_call_usage ADD COLUMN provider_message_count INTEGER;
ALTER TABLE ai_call_usage ADD COLUMN history_message_count INTEGER;
ALTER TABLE ai_call_usage ADD COLUMN service_context_tokens INTEGER;
ALTER TABLE ai_call_usage ADD COLUMN facts_count INTEGER;
ALTER TABLE ai_call_usage ADD COLUMN facts_tokens INTEGER;
ALTER TABLE ai_call_usage ADD COLUMN branch_metadata_tokens INTEGER;
ALTER TABLE ai_call_usage ADD COLUMN auxiliary_input_tokens INTEGER;
ALTER TABLE ai_call_usage ADD COLUMN auxiliary_output_tokens INTEGER;
ALTER TABLE ai_call_usage ADD COLUMN auxiliary_total_tokens INTEGER;
ALTER TABLE ai_call_usage ADD COLUMN auxiliary_cost_usd REAL;
ALTER TABLE ai_call_usage ADD COLUMN aggregate_input_tokens INTEGER;
ALTER TABLE ai_call_usage ADD COLUMN aggregate_output_tokens INTEGER;
ALTER TABLE ai_call_usage ADD COLUMN aggregate_total_tokens INTEGER;
ALTER TABLE ai_call_usage ADD COLUMN aggregate_cost_usd REAL;

-- +goose Down
SELECT intentionally_irreversible;
