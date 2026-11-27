-- database: observability
-- DB-07 (2026-11-27): FK child columns in the observability plane. These
-- tables are written on every run and were missing the indexes that make
-- parent cleanup cheap.

CREATE INDEX IF NOT EXISTS idx_fk_run_observability_attempt_id ON run_observability(attempt_id);
CREATE INDEX IF NOT EXISTS idx_fk_run_child_observations_parent_run_id ON run_child_observations(parent_run_id);
CREATE INDEX IF NOT EXISTS idx_fk_run_artifact_checkpoints_run_id ON run_artifact_checkpoints(run_id);
