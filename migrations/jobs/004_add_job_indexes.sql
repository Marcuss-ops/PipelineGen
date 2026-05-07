-- 004_add_job_indexes.sql
-- Additional performance indexes for jobs and job_events tables

-- jobs additional indexes
CREATE INDEX IF NOT EXISTS idx_jobs_status_updated
ON jobs(status, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_jobs_worker
ON jobs(worker_id)
WHERE worker_id != '';

CREATE INDEX IF NOT EXISTS idx_jobs_retry
ON jobs(retry_count, max_retries);

CREATE INDEX IF NOT EXISTS idx_jobs_updated_at
ON jobs(updated_at DESC);

-- job_events additional indexes
CREATE INDEX IF NOT EXISTS idx_job_events_type
ON job_events(type);

CREATE INDEX IF NOT EXISTS idx_job_events_created_at
ON job_events(created_at DESC);

CREATE INDEX IF NOT EXISTS idx_job_events_composite
ON job_events(job_id, type, created_at DESC);
