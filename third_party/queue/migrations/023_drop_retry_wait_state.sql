-- D4: retry_wait was never produced by any code path but remained in every
-- CHECK constraint, forcing every future state-machine edit to preserve an
-- unreachable state. Drop it. Likewise document that workflow_id,
-- source_job_id and render_jobs.error_code are dead columns kept only for
-- backward compatibility with external producers — new code must not use them.
ALTER TABLE render_jobs DROP CONSTRAINT IF EXISTS render_jobs_state_check;
ALTER TABLE render_jobs ADD CONSTRAINT render_jobs_state_check
    CHECK (state IN ('pending', 'running', 'completed', 'failed', 'cancelled', 'rendered', 'finalizing'));
