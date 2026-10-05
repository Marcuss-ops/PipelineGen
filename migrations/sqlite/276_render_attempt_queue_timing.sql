-- database: primary
-- Migration 276: preserve observed producer and RenderingGen queue lifecycle
-- timestamps for every per-item attempt, including failed submit/wait paths.
-- NULL means that the owner did not report the event; timestamps are never
-- synthesized from unrelated wall durations.
ALTER TABLE render_attempt_analytics ADD COLUMN submit_started_at TEXT;
ALTER TABLE render_attempt_analytics ADD COLUMN submit_accepted_at TEXT;
ALTER TABLE render_attempt_analytics ADD COLUMN wait_started_at TEXT;
ALTER TABLE render_attempt_analytics ADD COLUMN wait_finished_at TEXT;
ALTER TABLE render_attempt_analytics ADD COLUMN queue_queued_at TEXT;
ALTER TABLE render_attempt_analytics ADD COLUMN queue_started_at TEXT;
ALTER TABLE render_attempt_analytics ADD COLUMN queue_completed_at TEXT;
ALTER TABLE render_attempt_analytics ADD COLUMN artifact_available_at TEXT;
ALTER TABLE render_attempt_analytics ADD COLUMN outcome TEXT NOT NULL DEFAULT '';
