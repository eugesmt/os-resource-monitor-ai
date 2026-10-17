-- +goose Up
CREATE TABLE memory_records (
    id TEXT PRIMARY KEY,
    layer TEXT NOT NULL CHECK (layer IN ('short_term', 'working', 'long_term')),
    scope TEXT NOT NULL,
    kind TEXT NOT NULL,
    content TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE INDEX memory_records_layer_scope_created_idx
    ON memory_records (layer, scope, created_at, id);

-- +goose Down
DROP INDEX memory_records_layer_scope_created_idx;
DROP TABLE memory_records;
