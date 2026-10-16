-- Drop the inert `priority` ordering column.
--
-- No producer in this repository ever writes a non-default value: the job
-- model has no priority field and Submit/SubmitIdempotent insert without it,
-- so every row keeps DEFAULT 0 and the claim's "priority DESC" term was dead
-- ordering (plus a misleading index column). Claim ordering is FIFO by
-- queued_at.
ALTER TABLE render_jobs DROP COLUMN priority;

-- The old idx_render_jobs_claim (state, priority DESC, queued_at ASC) is
-- dropped automatically with its column; recreate the claim index over the
-- columns the claim query actually orders by.
CREATE INDEX idx_render_jobs_claim
    ON render_jobs (state, queued_at);
