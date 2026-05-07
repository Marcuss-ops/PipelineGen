-- 006_create_pipeline_runs.sql
-- Pipeline runs table for tracking complete pipeline executions

CREATE TABLE IF NOT EXISTS pipeline_runs (
    id TEXT PRIMARY KEY,
    run_type TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    input_json TEXT,
    output_json TEXT,
    error_message TEXT,
    started_at TEXT DEFAULT CURRENT_TIMESTAMP,
    finished_at TEXT,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_pipeline_runs_type
ON pipeline_runs(run_type);

CREATE INDEX IF NOT EXISTS idx_pipeline_runs_status
ON pipeline_runs(status);

CREATE INDEX IF NOT EXISTS idx_pipeline_runs_created_at
ON pipeline_runs(created_at DESC);

CREATE INDEX IF NOT EXISTS idx_pipeline_runs_started_at
ON pipeline_runs(started_at DESC)
WHERE started_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_pipeline_runs_finished_at
ON pipeline_runs(finished_at DESC)
WHERE finished_at IS NOT NULL;
