-- render_jobs: current state of each render job in the queue.
--
-- This table holds the *current* state of a job; render_events holds its
-- history. There is exactly one row per job, regardless of how many attempts
-- it goes through.
CREATE TABLE render_jobs (
    id                     TEXT PRIMARY KEY,
    workflow_id            TEXT,
    source_job_id          TEXT,
    job_type               TEXT NOT NULL,
    state                  TEXT NOT NULL DEFAULT 'pending',
    priority               INTEGER NOT NULL DEFAULT 0,
    overlay_schema_version INTEGER,
    overlay_spec           JSONB NOT NULL DEFAULT '{}'::jsonb,
    input_manifest         JSONB,
    attempt_count          INTEGER NOT NULL DEFAULT 0,
    max_attempts           INTEGER NOT NULL DEFAULT 3,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    queued_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at             TIMESTAMPTZ,
    completed_at           TIMESTAMPTZ,
    failed_at              TIMESTAMPTZ,
    current_worker_id      TEXT,
    lease_until            TIMESTAMPTZ,
    error_code             TEXT,
    error_message          TEXT,
    artifact_id            TEXT,
    idempotency_key        TEXT
);

ALTER TABLE render_jobs
    ADD CONSTRAINT render_jobs_state_check
    CHECK (state IN ('pending', 'running', 'retry_wait', 'completed', 'failed', 'cancelled'));
