-- Migration 127: composite index for parent aggregator poller.
-- The voiceover parent aggregator queries:
--   SELECT ... FROM jobs WHERE type = ? AND status IN (...)
--   AND json_extract(result_json,'$.parent_state') IN (...)
--
-- An index on (type, status) lets SQLite narrow the scan to
-- voiceover.generate jobs in RUNNING/FINALIZING/SUCCEEDED status
-- before applying the json_extract filter, instead of scanning
-- ALL voiceover.generate rows ever created.
--
-- See voiceover.md §10.5 for the canonical query shape.

CREATE INDEX IF NOT EXISTS idx_jobs_type_status
    ON jobs(type, status);
