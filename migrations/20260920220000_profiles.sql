-- +goose Up
CREATE TABLE profiles (
    user_id TEXT PRIMARY KEY,
    style TEXT NOT NULL,
    format TEXT NOT NULL,
    constraints_json TEXT NOT NULL
);

-- +goose Down
DROP TABLE profiles;
