-- +goose Up
CREATE TABLE metric_samples (
    snapshot_id TEXT PRIMARY KEY,
    captured_at TEXT NOT NULL,
    hostname TEXT NOT NULL DEFAULT '',
    cpu_percent REAL,
    mem_percent REAL,
    load_1 REAL,
    load_5 REAL,
    load_15 REAL,
    network_bytes_recv REAL,
    network_bytes_sent REAL,
    network_recv_per_sec REAL,
    network_send_per_sec REAL,
    disk_read_bytes REAL,
    disk_write_bytes REAL,
    disk_read_per_sec REAL,
    disk_write_per_sec REAL,
    missing_data_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(missing_data_json))
);

CREATE INDEX metric_samples_captured_at_idx
    ON metric_samples (captured_at, snapshot_id);

CREATE TABLE metric_rollups (
    bucket_start TEXT PRIMARY KEY,
    bucket_end TEXT NOT NULL,
    generated_at TEXT NOT NULL,
    sample_count INTEGER NOT NULL CHECK (sample_count >= 0),
    covered_from TEXT,
    covered_to TEXT,

    cpu_sum REAL,
    cpu_count INTEGER NOT NULL DEFAULT 0 CHECK (cpu_count >= 0),
    cpu_min REAL,
    cpu_max REAL,
    memory_sum REAL,
    memory_count INTEGER NOT NULL DEFAULT 0 CHECK (memory_count >= 0),
    memory_min REAL,
    memory_max REAL,
    load1_sum REAL,
    load1_count INTEGER NOT NULL DEFAULT 0 CHECK (load1_count >= 0),
    load1_min REAL,
    load1_max REAL,
    load5_sum REAL,
    load5_count INTEGER NOT NULL DEFAULT 0 CHECK (load5_count >= 0),
    load5_min REAL,
    load5_max REAL,
    load15_sum REAL,
    load15_count INTEGER NOT NULL DEFAULT 0 CHECK (load15_count >= 0),
    load15_min REAL,
    load15_max REAL,
    network_recv_rate_sum REAL,
    network_recv_rate_count INTEGER NOT NULL DEFAULT 0 CHECK (network_recv_rate_count >= 0),
    network_recv_rate_min REAL,
    network_recv_rate_max REAL,
    network_send_rate_sum REAL,
    network_send_rate_count INTEGER NOT NULL DEFAULT 0 CHECK (network_send_rate_count >= 0),
    network_send_rate_min REAL,
    network_send_rate_max REAL,
    disk_read_rate_sum REAL,
    disk_read_rate_count INTEGER NOT NULL DEFAULT 0 CHECK (disk_read_rate_count >= 0),
    disk_read_rate_min REAL,
    disk_read_rate_max REAL,
    disk_write_rate_sum REAL,
    disk_write_rate_count INTEGER NOT NULL DEFAULT 0 CHECK (disk_write_rate_count >= 0),
    disk_write_rate_min REAL,
    disk_write_rate_max REAL,

    network_recv_delta_bytes REAL,
    network_send_delta_bytes REAL,
    disk_read_delta_bytes REAL,
    disk_write_delta_bytes REAL,
    missing_data_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(missing_data_json))
);

CREATE INDEX metric_rollups_bucket_end_idx
    ON metric_rollups (bucket_end);

-- +goose Down
DROP INDEX metric_rollups_bucket_end_idx;
DROP TABLE metric_rollups;
DROP INDEX metric_samples_captured_at_idx;
DROP TABLE metric_samples;
