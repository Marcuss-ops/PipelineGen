-- database: jobs
-- Payloads/results belong to dedicated tables.  Keep this migration
-- compatibility-only: older jobs databases may have been created before
-- the split and need the tables/indexes, but the hot `jobs` row must never
-- be widened with payload_json/result_json columns.
CREATE TABLE IF NOT EXISTS job_payloads (
    job_id TEXT PRIMARY KEY,
    codec_id TEXT NOT NULL DEFAULT 'json',
    payload TEXT NOT NULL DEFAULT '{}',
    payload_hash TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS job_results (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id TEXT NOT NULL,
    attempt INTEGER NOT NULL DEFAULT 0,
    result_hash TEXT NOT NULL DEFAULT '',
    codec_id TEXT NOT NULL DEFAULT 'json',
    result_payload TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL,
    FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS uniq_job_results_dedup
    ON job_results(job_id, attempt, result_hash);
CREATE INDEX IF NOT EXISTS idx_job_results_job
    ON job_results(job_id, attempt DESC);
