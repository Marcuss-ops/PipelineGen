-- database: primary
-- Migration 227: separate queue completion wait from Chronon render time.
--
-- render_ms / encode_ms are worker-reported Chronon/media durations.
-- completion_wait_ms is PipelineGen's wall time observing the queue.
-- polling_sleep_ms measures the time deliberately spent between status polls,
-- making the latency introduced by the production 2s cadence explicit.

ALTER TABLE render_attempt_analytics ADD COLUMN completion_wait_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE render_attempt_analytics ADD COLUMN polling_sleep_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE render_attempt_analytics ADD COLUMN polling_interval_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE render_attempt_analytics ADD COLUMN poll_count INTEGER NOT NULL DEFAULT 0;
