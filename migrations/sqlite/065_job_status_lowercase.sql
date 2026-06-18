-- 065: Convert job status from UPPERCASE legacy models to lowercase domain values
-- The canonical domain (internal/core/domain/job) uses 7-state lowercase:
--   queued, leased, running, retry_wait, completed, failed, cancelled
-- Legacy models used UPPERCASE: PENDING, LEASED, RUNNING, SUCCEEDED, RETRY_WAIT, FAILED, CANCELLED

UPDATE jobs SET status = 'queued'      WHERE status = 'PENDING';
UPDATE jobs SET status = 'leased'      WHERE status = 'LEASED';
UPDATE jobs SET status = 'running'     WHERE status = 'RUNNING';
UPDATE jobs SET status = 'retry_wait'  WHERE status = 'RETRY_WAIT';
UPDATE jobs SET status = 'completed'   WHERE status = 'SUCCEEDED';
UPDATE jobs SET status = 'failed'      WHERE status = 'FAILED';
UPDATE jobs SET status = 'cancelled'   WHERE status = 'CANCELLED';
