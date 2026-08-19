-- database: observability
-- The run observation is the complete operational fact. These fields were
-- already carried by OperationReport and must not exist only in analytics.

ALTER TABLE run_operation_observations ADD COLUMN source_sha256 TEXT NOT NULL DEFAULT '';
ALTER TABLE run_operation_observations ADD COLUMN source_duration_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE run_operation_observations ADD COLUMN source_size_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE run_operation_observations ADD COLUMN width INTEGER NOT NULL DEFAULT 0;
ALTER TABLE run_operation_observations ADD COLUMN height INTEGER NOT NULL DEFAULT 0;
ALTER TABLE run_operation_observations ADD COLUMN fps REAL NOT NULL DEFAULT 0;
ALTER TABLE run_operation_observations ADD COLUMN input_codec TEXT NOT NULL DEFAULT '';
ALTER TABLE run_operation_observations ADD COLUMN output_codec TEXT NOT NULL DEFAULT '';
ALTER TABLE run_operation_observations ADD COLUMN cache_hit INTEGER NOT NULL DEFAULT 0;
ALTER TABLE run_operation_observations ADD COLUMN strategy TEXT NOT NULL DEFAULT '';
ALTER TABLE run_operation_observations ADD COLUMN output_size_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE run_operation_observations ADD COLUMN cpu_user_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE run_operation_observations ADD COLUMN cpu_system_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE run_operation_observations ADD COLUMN created_at TEXT;
