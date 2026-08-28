-- database: observability
-- Migration 238: canonical Job–Attempt–Run resource reports.
-- The report envelope and raw samples are the SSOT; aggregates are derived by readers.

CREATE TABLE IF NOT EXISTS run_resource_reports (
    run_id             TEXT PRIMARY KEY,
    job_id             TEXT NOT NULL,
    attempt_id         TEXT NOT NULL UNIQUE,
    schema_version     INTEGER NOT NULL,
    started_at         TEXT,
    finished_at        TEXT,
    sample_count       INTEGER NOT NULL DEFAULT 0,
    report_json        TEXT NOT NULL,
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS run_resource_samples (
    sample_id          TEXT PRIMARY KEY,
    run_id             TEXT NOT NULL,
    job_id             TEXT NOT NULL,
    attempt_id         TEXT NOT NULL,
    observed_at        TEXT NOT NULL,
    sample_json        TEXT NOT NULL,
    created_at         TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_run_resource_reports_job
    ON run_resource_reports(job_id, updated_at);
CREATE INDEX IF NOT EXISTS idx_run_resource_samples_run
    ON run_resource_samples(run_id, observed_at);
CREATE INDEX IF NOT EXISTS idx_run_resource_samples_job
    ON run_resource_samples(job_id, observed_at);
