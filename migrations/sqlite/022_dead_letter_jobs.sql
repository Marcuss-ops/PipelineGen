-- Dead-letter table for jobs that exhausted all retries. Kept separate
-- from the main jobs table so the dashboard stays clean and operators
-- can query "what failed recently" without filtering on status.
CREATE TABLE IF NOT EXISTS dead_letter_jobs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id TEXT NOT NULL,
    job_type TEXT NOT NULL,
    correlation_id TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL,
    payload_json TEXT,
    retry_count INTEGER NOT NULL DEFAULT 0,
    failed_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_dlq_job_id ON dead_letter_jobs(job_id);
CREATE INDEX IF NOT EXISTS idx_dlq_correlation ON dead_letter_jobs(correlation_id);
CREATE INDEX IF NOT EXISTS idx_dlq_failed_at ON dead_letter_jobs(failed_at);
