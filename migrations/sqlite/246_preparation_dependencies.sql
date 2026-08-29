-- 246_preparation_dependencies.sql
-- Durable snapshot of preparation readiness at job-claim time.
-- database: primary

CREATE TABLE IF NOT EXISTS preparation_claim_snapshots (
    job_id                 TEXT NOT NULL,
    attempt_id             TEXT NOT NULL,

    job_revision           INTEGER NOT NULL DEFAULT 0,
    claimed_at             TEXT NOT NULL,

    total_units            INTEGER NOT NULL DEFAULT 0,
    required_units         INTEGER NOT NULL DEFAULT 0,
    ready_units            INTEGER NOT NULL DEFAULT 0,
    running_units          INTEGER NOT NULL DEFAULT 0,
    missing_units          INTEGER NOT NULL DEFAULT 0,

    prepared_ratio         REAL NOT NULL DEFAULT 0,
    estimated_saved_ms     INTEGER NOT NULL DEFAULT 0,
    speculative_work_ms    INTEGER NOT NULL DEFAULT 0,
    queue_wait_ms          INTEGER NOT NULL DEFAULT 0,
    queue_position_at_plan INTEGER NOT NULL DEFAULT 0,

    metadata_json          TEXT NOT NULL DEFAULT '{}',

    PRIMARY KEY (job_id, attempt_id)
);

CREATE INDEX IF NOT EXISTS idx_preparation_claim_snapshots_claimed
    ON preparation_claim_snapshots(claimed_at);
