-- Cancellation support: a producer may cancel a job that has not reached a
-- terminal state. render_jobs already allows the 'cancelled' state (migration
-- 001); this migration lets a cancelled render attempt be closed as
-- 'cancelled' so the attempt history records why the attempt ended without a
-- GPU re-render (a cancelled job is never claimable again and lease expiry
-- never requeues it).
ALTER TABLE render_attempts DROP CONSTRAINT render_attempts_status_check;
ALTER TABLE render_attempts ADD CONSTRAINT render_attempts_status_check
    CHECK (status IN ('running', 'completed', 'failed', 'lease_expired', 'rendered', 'cancelled'));
