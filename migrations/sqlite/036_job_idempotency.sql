-- 036_job_idempotency.sql
--
-- Enforce idempotency on job enqueue across remote-worker retries by adding
-- a conditional UNIQUE INDEX on (type, correlation_id), mirroring the
-- existing idx_jobs_active_key pattern. The X-Request-ID middleware sets
-- the correlation_id, so two enqueues with the same client request
-- surface as the same job_id instead of producing duplicate work
-- (especially costly for video/image generation).
--
-- Idempotent: safe to run multiple times.
--
-- Two phases:
-- 1. Resolve pre-existing duplicates. For each (type, correlation_id)
--    group with a non-empty correlation_id, keep the row with the
--    latest created_at and clear correlation_id on the rest. Without
--    this, the index creation in phase 2 would fail on production DBs
--    where the same client retried before the index existed.
-- 2. Create the conditional UNIQUE INDEX. Empty correlation_id is
--    excluded so jobs without a correlation_id stay unique-by-id
--    only — clients that don't send X-Request-ID keep working.

-- Phase 1: deduplicate existing rows. Window-function based; the inner
-- SELECT is wrapped so SQLite accepts the UPDATE-from-SELECT (SQLite
-- rejects UPDATE that targets a table directly named in its FROM list).
-- created_at is RFC3339 text and sorts lexicographically as chronology,
-- so no datetime() wrapping is needed.
UPDATE jobs
SET correlation_id = ''
WHERE id IN (
    SELECT id FROM (
        SELECT id,
               ROW_NUMBER() OVER (
                   PARTITION BY type, correlation_id
                   ORDER BY created_at DESC, id DESC
               ) AS rn
        FROM jobs
        WHERE correlation_id != ''
    )
    WHERE rn > 1
);

-- Phase 2: conditional UNIQUE INDEX. Mirrors idx_jobs_active_key
-- (WHERE active_key != '') — the correlation_id column is
-- NOT NULL DEFAULT '' so the IS NOT NULL guard would be redundant;
-- using the shorter form keeps the two indexes consistent and easier
-- to read at a glance.
CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_type_correlation
    ON jobs(type, correlation_id)
    WHERE correlation_id != '';
