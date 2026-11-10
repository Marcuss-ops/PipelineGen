-- Lease fencing token.
--
-- Every successful claim increments this value and returns it with the job.
-- Terminal reports (complete/fail/rendered) echo the generation they were
-- given, so a report from an expired attempt cannot finalize or requeue the
-- attempt that replaced it. Existing rows start at 0 and receive generation 1
-- on their next claim.
ALTER TABLE render_jobs
    ADD COLUMN IF NOT EXISTS lease_generation BIGINT NOT NULL DEFAULT 0;
