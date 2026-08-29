-- 249_preparation_attempts_workload_compat.sql
-- Backward-compatible extension for databases that already contain
-- preparation_attempts without workload measurements.
-- database: primary

ALTER TABLE preparation_attempts
    ADD COLUMN workload_dimension TEXT NOT NULL DEFAULT '';

ALTER TABLE preparation_attempts
    ADD COLUMN workload_amount REAL NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_preparation_attempts_workload
    ON preparation_attempts(workload_dimension, workload_amount)
    WHERE workload_dimension <> '' AND workload_amount > 0;
