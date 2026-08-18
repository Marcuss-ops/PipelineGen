-- database: primary
-- Migration 217: per-operation media performance granularity.
--
-- One row per media operation (probe, normalize, watermark, transitions,
-- audio_mix, assemble_copy, render_scene, ...) recorded live by the
-- ObservedExecutor — the single measurement point in the execution layer.
-- The stage-level history lives in performance_runs/performance_steps
-- (projected at job completion); THIS table answers "what did each
-- operation cost" across thousands of runs:
--
--   SELECT operation, COUNT(*), AVG(elapsed_ms), AVG(output_size_bytes),
--          SUM(cache_hit) FROM performance_operations GROUP BY operation;
--
-- run_id/job_id/step_id are correlation slots resolved from the execution
-- context at record time ('' when the operation ran outside a tracked run).
-- There is deliberately NO foreign key to performance_runs: operations are
-- recorded live during execution, before the run row is projected.
--
-- RTF (Real-Time Factor) = elapsed_ms / source_duration_ms is derived in
-- the projection, never stored.

CREATE TABLE IF NOT EXISTS performance_operations (
    operation_id TEXT PRIMARY KEY,

    run_id TEXT NOT NULL,
    job_id TEXT NOT NULL,
    step_id TEXT NOT NULL,

    operation TEXT NOT NULL,

    source_sha256 TEXT NOT NULL DEFAULT '',
    source_duration_ms INTEGER NOT NULL DEFAULT 0,
    source_size_bytes INTEGER NOT NULL DEFAULT 0,

    width INTEGER NOT NULL DEFAULT 0,
    height INTEGER NOT NULL DEFAULT 0,
    fps REAL NOT NULL DEFAULT 0,

    input_codec TEXT NOT NULL DEFAULT '',
    output_codec TEXT NOT NULL DEFAULT '',

    elapsed_ms INTEGER NOT NULL DEFAULT 0,

    cpu_user_ms INTEGER NOT NULL DEFAULT 0,
    cpu_system_ms INTEGER NOT NULL DEFAULT 0,

    output_size_bytes INTEGER NOT NULL DEFAULT 0,

    cache_hit INTEGER NOT NULL DEFAULT 0,

    strategy TEXT NOT NULL DEFAULT '',

    metadata_json TEXT NOT NULL DEFAULT '{}',

    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_performance_operations_operation ON performance_operations(operation, created_at);
CREATE INDEX IF NOT EXISTS idx_performance_operations_run ON performance_operations(run_id, created_at);