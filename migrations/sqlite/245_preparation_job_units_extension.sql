-- =============================================================================
-- 245_preparation_job_units_extension.sql — Preparation Fabric (Control Plane) v2
-- =============================================================================
--
-- Context:
--   Migration 243 declared `preparation_job_units`, the per-job view over the
--   global `preparation_units` registry: which fingerprint each job depends on,
--   whether it is required, and when the job adopted the prepared result. That
--   table turns the global computation identity into a real per-job DAG.
--
--   This migration EXTENDS it with the plan's richer job-scoped metadata:
--
--     * where the unit sits in the workflow (phase, scene_id, language),
--     * the plan's spec/adoption policy (speculation_ceiling, priority,
--       critical_path_ms, queue_distance),
--     * a typed adoption_state (PENDING/ADOPTED/MISS/INVALIDATED) replacing
--       the coarse two-valued `adopted` flag for ready-at-claim accounting,
--     * checkpoint linkage (checkpoint_stage + checkpoint_unit_id) so the
--       "checkpoint first, prepared result second, computation last" ordering
--       is durable,
--     * promotion/invalidation timestamps for the claim-snapshot KPI.
--
--   The existing `adopted` INTEGER and `queue_rank` columns are left in place
--   (backward-compatible) and `adoption_state` supersedes them for new writes.
--
--   Idempotency:
--     * All CREATE INDEX use `IF NOT EXISTS` → idempotent.
--     * The ALTER TABLE ADD COLUMNs are NOT idempotent (SQLite has no
--       `ADD COLUMN IF NOT EXISTS`); the canonical guard is the
--       schema_migrations ledger, matching migration 244.
--
--   SQLite ADD COLUMN does not permit CHECK / non-constant defaults, so
--   `adoption_state` is added as plain TEXT with default 'PENDING'. The
--   canonical set (PENDING, ADOPTED, MISS, INVALIDATED) and the
--   `speculation_ceiling` 0..5 range are enforced at the application layer.
-- =============================================================================

-- database: primary
CREATE TABLE IF NOT EXISTS preparation_job_units (
    job_id TEXT NOT NULL,
    unit_id TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    required INTEGER NOT NULL DEFAULT 1,
    adopted INTEGER NOT NULL DEFAULT 0,
    queue_rank INTEGER,
    planned_at TEXT NOT NULL,
    adopted_at TEXT,
    PRIMARY KEY (job_id, unit_id)
);

-- ---------------------------------------------------------------------------
-- Workflow placement
-- ---------------------------------------------------------------------------
ALTER TABLE preparation_job_units ADD COLUMN phase     TEXT NOT NULL DEFAULT '';
ALTER TABLE preparation_job_units ADD COLUMN scene_id  TEXT NOT NULL DEFAULT '';
ALTER TABLE preparation_job_units ADD COLUMN language  TEXT NOT NULL DEFAULT '';

-- ---------------------------------------------------------------------------
-- Scheduling / speculation policy (job-scoped, NOT global)
-- ---------------------------------------------------------------------------
ALTER TABLE preparation_job_units ADD COLUMN queue_distance       INTEGER NOT NULL DEFAULT 0;
ALTER TABLE preparation_job_units ADD COLUMN speculation_ceiling   INTEGER NOT NULL DEFAULT 0;
ALTER TABLE preparation_job_units ADD COLUMN priority_score        REAL    NOT NULL DEFAULT 0;
ALTER TABLE preparation_job_units ADD COLUMN critical_path_ms      INTEGER NOT NULL DEFAULT 0;

-- ---------------------------------------------------------------------------
-- Adoption lifecycle (supersedes the coarse `adopted` flag)
-- ---------------------------------------------------------------------------
ALTER TABLE preparation_job_units ADD COLUMN adoption_state    TEXT NOT NULL DEFAULT 'PENDING';
ALTER TABLE preparation_job_units ADD COLUMN promoted_at       TEXT;
ALTER TABLE preparation_job_units ADD COLUMN invalidated_at    TEXT;

-- ---------------------------------------------------------------------------
-- Checkpoint linkage (checkpoint first → prepared result → computation)
-- ---------------------------------------------------------------------------
ALTER TABLE preparation_job_units ADD COLUMN checkpoint_stage   TEXT NOT NULL DEFAULT '';
ALTER TABLE preparation_job_units ADD COLUMN checkpoint_unit_id TEXT NOT NULL DEFAULT '';

-- ---------------------------------------------------------------------------
-- Job-scoped support indexes (idempotent)
-- ---------------------------------------------------------------------------
-- Adoption scan: per-job readiness at claim / replay
CREATE INDEX IF NOT EXISTS idx_preparation_job_units_job_state
    ON preparation_job_units(job_id, adoption_state);

-- DAG per-scene iteration during speculative execution
CREATE INDEX IF NOT EXISTS idx_preparation_job_units_scene
    ON preparation_job_units(job_id, scene_id);

-- Scheduler priority ordering inside a job
CREATE INDEX IF NOT EXISTS idx_preparation_job_units_priority
    ON preparation_job_units(job_id, priority_score DESC);