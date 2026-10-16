-- render_rendered_state: introduce the "rendered" state and Google Drive
-- publication columns.
--
-- A job enters "rendered" when its Chronon render finished and the artifact is
-- durably stored in the object store, but the external publication (Google
-- Drive) failed. Workers re-claiming such a job skip rendering and only retry
-- the publication, so a flaky upload never wastes GPU rendering.

-- render_jobs gains the 'rendered' state.
ALTER TABLE render_jobs DROP CONSTRAINT render_jobs_state_check;
ALTER TABLE render_jobs ADD CONSTRAINT render_jobs_state_check
    CHECK (state IN ('pending', 'running', 'retry_wait', 'completed', 'failed', 'cancelled', 'rendered'));

-- render_attempts gains the 'rendered' attempt status: the render completed
-- but the publication failed, so the attempt is neither failed nor completed.
ALTER TABLE render_attempts DROP CONSTRAINT render_attempts_status_check;
ALTER TABLE render_attempts ADD CONSTRAINT render_attempts_status_check
    CHECK (status IN ('running', 'completed', 'failed', 'lease_expired', 'rendered'));

-- render_artifacts records the Google Drive publication, populated only after
-- the upload succeeds.
ALTER TABLE render_artifacts ADD COLUMN IF NOT EXISTS drive_file_id TEXT;
ALTER TABLE render_artifacts ADD COLUMN IF NOT EXISTS drive_link TEXT;
