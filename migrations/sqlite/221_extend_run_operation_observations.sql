-- database: observability
-- Canonical operation lifecycle for fan-out/concurrency analysis.
-- Performance tables and Prometheus must project these facts; they must not
-- create a second timing record.

ALTER TABLE run_stage_observations ADD COLUMN started_at TEXT;
ALTER TABLE run_stage_observations ADD COLUMN finished_at TEXT;

ALTER TABLE run_operation_observations ADD COLUMN queue_wait_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE run_operation_observations ADD COLUMN worker_id TEXT;
ALTER TABLE run_operation_observations ADD COLUMN queued_at TEXT;
ALTER TABLE run_operation_observations ADD COLUMN started_at TEXT;
ALTER TABLE run_operation_observations ADD COLUMN finished_at TEXT;
ALTER TABLE run_operation_observations ADD COLUMN metadata_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE run_operation_observations ADD COLUMN output_duration_ms INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_run_operation_observations_lifecycle
    ON run_operation_observations(run_id, started_at, finished_at);
CREATE INDEX IF NOT EXISTS idx_run_operation_observations_worker
    ON run_operation_observations(run_id, worker_id, started_at);
