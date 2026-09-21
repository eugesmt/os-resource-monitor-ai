-- +goose Up
CREATE TABLE assistant_invariants (
    id TEXT PRIMARY KEY,
    category TEXT NOT NULL,
    statement TEXT NOT NULL,
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1))
);

-- +goose Down
DROP TABLE assistant_invariants;
