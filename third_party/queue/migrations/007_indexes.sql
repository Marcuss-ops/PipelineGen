-- Indexes for the hot queue paths: claim ordering, lease expiry scanning,
-- and per-job history/event lookups.

-- Claim: pending jobs ordered by priority (highest first) then queue time.
CREATE INDEX idx_render_jobs_claim
    ON render_jobs (state, priority DESC, queued_at ASC);

-- Lease expiry scan: running jobs whose lease has lapsed.
CREATE INDEX idx_render_jobs_lease
    ON render_jobs (state, lease_until);

CREATE INDEX idx_render_jobs_workflow
    ON render_jobs (workflow_id);

-- render_attempts (job_id, attempt_number) is already covered by the UNIQUE
-- constraint created in 002_render_attempts.sql, so no duplicate index.

CREATE INDEX idx_render_artifacts_job
    ON render_artifacts (job_id);

CREATE INDEX idx_render_events_job_time
    ON render_events (job_id, created_at);

CREATE INDEX idx_processing_metrics_job
    ON processing_metrics (job_id, metric_name);

-- Idempotent submission: at most one job per idempotency_key.
CREATE UNIQUE INDEX idx_render_jobs_idempotency
    ON render_jobs (idempotency_key)
    WHERE idempotency_key IS NOT NULL;
