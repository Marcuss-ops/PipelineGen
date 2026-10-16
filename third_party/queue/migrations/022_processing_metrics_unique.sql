-- processing_metrics uniqueness: one row per (job, attempt, metric_name).
-- Makes Complete idempotent: publication retries (Rendered → re-Complete)
-- upsert instead of duplicating rows. The batched insert in artifacts.go
-- uses ON CONFLICT on this constraint.
CREATE UNIQUE INDEX IF NOT EXISTS idx_processing_metrics_unique
    ON processing_metrics (job_id, attempt_id, metric_name);
