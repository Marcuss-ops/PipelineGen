-- database: primary
-- Migration 238: extend resource_observations for canonical run resource
-- telemetry — attempt linkage plus swap, disk utilization/iowait/queue
-- depth, decoder, per-component thermal and throttling columns.
-- Existing rows keep '' / NULL for the new columns; a missing measurement
-- stays NULL (never a fake zero).

ALTER TABLE resource_observations ADD COLUMN attempt_id TEXT NOT NULL DEFAULT '';
ALTER TABLE resource_observations ADD COLUMN swap_in_bytes INTEGER;
ALTER TABLE resource_observations ADD COLUMN swap_out_bytes INTEGER;
ALTER TABLE resource_observations ADD COLUMN disk_util_pct REAL;
ALTER TABLE resource_observations ADD COLUMN io_wait_pct REAL;
ALTER TABLE resource_observations ADD COLUMN disk_queue_depth REAL;
ALTER TABLE resource_observations ADD COLUMN decoder_avg_pct REAL;
ALTER TABLE resource_observations ADD COLUMN cpu_temp_peak_c REAL;
ALTER TABLE resource_observations ADD COLUMN gpu_temp_peak_c REAL;
ALTER TABLE resource_observations ADD COLUMN throttled INTEGER;

CREATE INDEX IF NOT EXISTS idx_resource_observations_attempt
    ON resource_observations(attempt_id, observed_at);
