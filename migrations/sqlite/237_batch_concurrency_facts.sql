-- database: primary
-- Migration 237: durable batch concurrency facts.
-- Only lifecycle facts are stored; concurrency aggregates are derived at read-time.

CREATE TABLE IF NOT EXISTS benchmark_batches (
    batch_id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL DEFAULT '',
    worker_slot_count INTEGER NOT NULL CHECK (worker_slot_count > 0),
    started_at TEXT NOT NULL,
    completed_at TEXT,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS benchmark_batch_jobs (
    batch_id TEXT NOT NULL,
    job_id TEXT NOT NULL,
    run_id TEXT NOT NULL DEFAULT '',
    worker_slot INTEGER,
    queued_at TEXT,
    started_at TEXT NOT NULL,
    completed_at TEXT,
    status TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    PRIMARY KEY (batch_id, job_id),
    FOREIGN KEY (batch_id) REFERENCES benchmark_batches(batch_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_benchmark_batches_started
    ON benchmark_batches(started_at);
CREATE INDEX IF NOT EXISTS idx_benchmark_batch_jobs_batch_started
    ON benchmark_batch_jobs(batch_id, started_at);
CREATE INDEX IF NOT EXISTS idx_benchmark_batch_jobs_job
    ON benchmark_batch_jobs(job_id, started_at);
