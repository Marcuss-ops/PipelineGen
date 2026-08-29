-- 250_preparation_fabric_legacy_cleanup.sql
-- Preparation Fabric legacy cleanup.
-- The canonical identity is unit_fingerprint; the legacy fingerprint column
-- remains as a compatibility read surface because SQLite cannot safely drop
-- columns without rebuilding tables and existing application rows may depend
-- on it. Normalize its values instead of destructive removal.
-- database: primary

UPDATE preparation_units
SET fingerprint = unit_fingerprint
WHERE (fingerprint IS NULL OR fingerprint = '')
  AND unit_fingerprint IS NOT NULL
  AND unit_fingerprint <> '';

UPDATE preparation_attempts
SET workload_dimension = '', workload_amount = 0
WHERE workload_amount < 0;

CREATE INDEX IF NOT EXISTS idx_preparation_units_canonical_fingerprint
    ON preparation_units(unit_fingerprint);

CREATE INDEX IF NOT EXISTS idx_preparation_attempts_status_finished
    ON preparation_attempts(status, finished_at)
    WHERE status IN ('READY', 'HIT');
