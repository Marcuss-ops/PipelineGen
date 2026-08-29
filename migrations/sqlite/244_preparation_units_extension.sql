-- 244_preparation_units_extension.sql
-- Per-job DAG edges for the Preparation Fabric.
-- Computation identity remains global in preparation_units; workflow topology
-- is stored per job here.
-- database: primary

CREATE TABLE IF NOT EXISTS preparation_dependencies (
    job_id             TEXT NOT NULL,
    unit_id            TEXT NOT NULL,
    depends_on_unit_id TEXT NOT NULL,

    dependency_kind TEXT NOT NULL DEFAULT 'HARD'
        CHECK (dependency_kind IN ('HARD', 'SOFT')),

    created_at TEXT NOT NULL,

    PRIMARY KEY (job_id, unit_id, depends_on_unit_id)
);

CREATE INDEX IF NOT EXISTS idx_preparation_dependencies_downstream
    ON preparation_dependencies(job_id, depends_on_unit_id);

CREATE INDEX IF NOT EXISTS idx_preparation_dependencies_upstream
    ON preparation_dependencies(job_id, unit_id);
