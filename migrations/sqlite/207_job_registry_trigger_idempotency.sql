-- database: primary
-- Migration 207: make job-registry lifecycle projections replay-safe.
--
-- Worker finalization and the registry recorder can observe the same
-- terminal transition. The event identity is deterministic, so a replay
-- must not turn an already-persisted lifecycle event into a failed job
-- projection.

DROP TRIGGER IF EXISTS trg_job_registry_status_changed;
CREATE TRIGGER trg_job_registry_status_changed
AFTER UPDATE OF status, started_at, completed_at, error ON jobs
WHEN OLD.status IS NOT NEW.status OR OLD.started_at IS NOT NEW.started_at
  OR OLD.completed_at IS NOT NEW.completed_at OR OLD.error IS NOT NEW.error
BEGIN
    INSERT OR IGNORE INTO job_registry_events
        (event_id, job_id, event_type, payload_json, created_at)
    VALUES
        ('job-status-' || NEW.id || '-' || NEW.revision || '-' || NEW.status,
         NEW.id,
         'JOB_STATUS_CHANGED',
         json_object('from', OLD.status, 'to', NEW.status, 'error', NEW.error),
         COALESCE(NEW.updated_at, datetime('now')));
END;
