-- Parent finalization uses the normal render_jobs row as its ownership gate.
ALTER TABLE render_jobs DROP CONSTRAINT render_jobs_state_check;
ALTER TABLE render_jobs ADD CONSTRAINT render_jobs_state_check
    CHECK (state IN ('pending', 'running', 'retry_wait', 'completed', 'failed', 'cancelled', 'rendered', 'finalizing'));
