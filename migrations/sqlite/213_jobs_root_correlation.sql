-- database: primary
-- Migration 213: canonical job correlation columns (parent/root lineage).
--
-- parent_job_id and root_job_id are written canonically at enqueue time by
-- the broker (internal/application/jobs Service.Enqueue). This migration
-- closes the schema gap between the already-live job-registry projection
-- (which reads/writes these columns) and the migration set, and backfills
-- historical rows so the control-plane verifier never sees an empty
-- root_job_id.

ALTER TABLE jobs ADD COLUMN parent_job_id TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN root_job_id TEXT NOT NULL DEFAULT '';

-- Backfill historical rows (pre-enqueue-writer). A root job (no parent) is
-- its own root; a child inherits its parent's root transitively. The depth
-- guard bounds the walk against a malformed parent cycle; an unresolved row
-- (orphan parent) falls back to its own id via COALESCE so root_job_id is
-- never left empty.
WITH RECURSIVE lineage(id, root_id, depth) AS (
    SELECT id, id, 0 FROM jobs WHERE parent_job_id = ''
    UNION ALL
    SELECT j.id, l.root_id, l.depth + 1
    FROM jobs j JOIN lineage l ON j.parent_job_id = l.id
    WHERE l.depth < 64
)
UPDATE jobs
SET root_job_id = COALESCE((SELECT root_id FROM lineage WHERE lineage.id = jobs.id), jobs.id)
WHERE root_job_id = '';
