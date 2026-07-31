-- database: primary
-- 184_create_process_phase_metrics.sql
-- Durable per-phase process timings shared by stock, clips, scripts,
-- translation, voiceover and document workflows.
--
-- The table intentionally keeps the common metric surface narrow. Process-
-- specific counters are represented by items_* and bytes_*; higher-level
-- process detail can be added in a future additive migration if needed.
CREATE TABLE IF NOT EXISTS process_phase_metrics (
    id INTEGER PRIMARY KEY AUTOINCREMENT,

    process_type TEXT NOT NULL,
    job_id TEXT NOT NULL,
    parent_job_id TEXT NOT NULL DEFAULT '',

    phase TEXT NOT NULL,
    language TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT '',

    started_at TEXT NOT NULL,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    queue_wait_ms INTEGER NOT NULL DEFAULT 0,

    status TEXT NOT NULL,
    error_code TEXT NOT NULL DEFAULT '',

    items_in INTEGER NOT NULL DEFAULT 0,
    items_out INTEGER NOT NULL DEFAULT 0,

    bytes_in INTEGER NOT NULL DEFAULT 0,
    bytes_out INTEGER NOT NULL DEFAULT 0,

    retry_count INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_process_phase_metrics_job
    ON process_phase_metrics(job_id, started_at DESC);

CREATE INDEX IF NOT EXISTS idx_process_phase_metrics_type_phase
    ON process_phase_metrics(process_type, phase, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_process_phase_metrics_parent_job
    ON process_phase_metrics(parent_job_id, started_at DESC)
    WHERE parent_job_id != '';
