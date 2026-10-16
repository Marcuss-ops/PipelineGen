-- render_jobs.not_before: deferred scheduling for render jobs.
--
-- A job submitted with a future not_before is stored as pending but is NOT
-- claimable until the time is due (the claim query filters on it), so a
-- producer can pre-load a backlog without overrunning the GPU workers. NULL
-- (the column default, and every pre-existing row) means immediately
-- claimable, so the change is backward compatible.
ALTER TABLE render_jobs ADD COLUMN not_before TIMESTAMPTZ;

-- Supports the claim predicate (state + due time) without a sequential scan
-- of the pending backlog.
CREATE INDEX render_jobs_claim_due_idx ON render_jobs(state, not_before, queued_at);
