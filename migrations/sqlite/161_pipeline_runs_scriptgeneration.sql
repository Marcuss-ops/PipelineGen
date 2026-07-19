-- 161_pipeline_runs_scriptgeneration.sql
-- Expands pipeline_runs to support the durable script-generation workflow.
-- The table may already exist with only an id column (seed_fixture bootstrap).
-- This migration creates the base table and then expands it with the full
-- GenerationRun aggregate schema via ALTER TABLE ADD COLUMN.
--
-- Note: this migration is intended to run once per database. Re-running it
-- after the columns already exist will fail with "duplicate column name".
-- The migration runner records the applied migration in schema_migrations,
-- so re-runs are not expected in normal operation.

-- Ensure the base table exists (idempotent no-op when already present).
CREATE TABLE IF NOT EXISTS pipeline_runs (
    id TEXT PRIMARY KEY
);

-- Expand the table with the GenerationRun aggregate columns.
-- SQLite ALTER TABLE ADD COLUMN is used so existing ids are preserved.
ALTER TABLE pipeline_runs ADD COLUMN job_id TEXT;
ALTER TABLE pipeline_runs ADD COLUMN idempotency_key TEXT NOT NULL DEFAULT '';
ALTER TABLE pipeline_runs ADD COLUMN status TEXT NOT NULL DEFAULT 'PENDING';
ALTER TABLE pipeline_runs ADD COLUMN current_stage TEXT NOT NULL DEFAULT 'NORMALIZING';
ALTER TABLE pipeline_runs ADD COLUMN requested_payload_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE pipeline_runs ADD COLUMN result_json TEXT;
ALTER TABLE pipeline_runs ADD COLUMN error_code TEXT;
ALTER TABLE pipeline_runs ADD COLUMN error_message TEXT;
ALTER TABLE pipeline_runs ADD COLUMN failed_stage TEXT;
ALTER TABLE pipeline_runs ADD COLUMN attempt_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE pipeline_runs ADD COLUMN next_retry_at TEXT;
ALTER TABLE pipeline_runs ADD COLUMN created_at TEXT NOT NULL DEFAULT (datetime('now'));
ALTER TABLE pipeline_runs ADD COLUMN updated_at TEXT NOT NULL DEFAULT (datetime('now'));

-- Indexes for the read patterns used by the runner and GET /full.
CREATE INDEX IF NOT EXISTS idx_pipeline_runs_job_id ON pipeline_runs(job_id);
CREATE INDEX IF NOT EXISTS idx_pipeline_runs_idempotency_key ON pipeline_runs(idempotency_key);
CREATE INDEX IF NOT EXISTS idx_pipeline_runs_status ON pipeline_runs(status);
CREATE INDEX IF NOT EXISTS idx_pipeline_runs_next_retry_at ON pipeline_runs(next_retry_at);
