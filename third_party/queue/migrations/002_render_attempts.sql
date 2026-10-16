-- render_attempts: one row per worker attempt on a job.
--
-- Attempts are never overwritten: every claim creates a new attempt so the
-- full history (failed, lease-expired, succeeded) is preserved. render_jobs
-- keeps the current summary; this table keeps the detail.
CREATE TABLE render_attempts (
    id               TEXT PRIMARY KEY,
    job_id           TEXT NOT NULL REFERENCES render_jobs (id) ON DELETE CASCADE,
    attempt_number   INTEGER NOT NULL,
    worker_id        TEXT,
    status           TEXT NOT NULL DEFAULT 'running',
    started_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_started_at TIMESTAMPTZ,
    last_renewed_at  TIMESTAMPTZ,
    finished_at      TIMESTAMPTZ,
    error_code       TEXT,
    error_message    TEXT,
    render_ms        BIGINT,
    download_ms      BIGINT,
    upload_ms        BIGINT,
    total_ms         BIGINT,
    UNIQUE (job_id, attempt_number)
);

ALTER TABLE render_attempts
    ADD CONSTRAINT render_attempts_status_check
    CHECK (status IN ('running', 'completed', 'failed', 'lease_expired'));
