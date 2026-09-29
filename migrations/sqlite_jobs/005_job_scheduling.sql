-- database: jobs
-- Deferred job scheduling. A job submitted with a future start time is
-- persisted with status='SCHEDULED' and exactly one row in job_schedules;
-- the canonical scheduler promotes it to 'QUEUED' when it becomes due AND
-- the admission policy (daily quota + concurrency cap) grants it a slot.
-- A SCHEDULED row is never claimable because the claim query only selects
-- QUEUED rows, so scheduling cannot race the worker pool.
CREATE TABLE IF NOT EXISTS job_schedules (
    job_id TEXT PRIMARY KEY,
    run_at TEXT NOT NULL,
    created_at TEXT NOT NULL,
    FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_job_schedules_due ON job_schedules(run_at, job_id);

-- Per-UTC-day promotion counter backing the scheduler's daily quota. It is
-- the single owner of "how many scheduled jobs were promoted today"; a day
-- with no row has promoted 0.
CREATE TABLE IF NOT EXISTS job_scheduler_counters (
    day TEXT PRIMARY KEY,
    promoted INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL
);

-- Per-stage status projection for a job. One row per (job_id, stage); the
-- canonical stages are owned by the scheduling package (script_llm,
-- voiceover, overlay, final_created, final_sent). Status and progress are
-- overwritten in place so a stage re-report cannot accumulate duplicates.
CREATE TABLE IF NOT EXISTS job_stage_status (
    job_id TEXT NOT NULL,
    stage TEXT NOT NULL,
    status TEXT NOT NULL,
    progress INTEGER NOT NULL DEFAULT 0,
    detail TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL,
    PRIMARY KEY (job_id, stage),
    FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_job_stage_status_job ON job_stage_status(job_id, stage);
