-- 066: Convert job status from lowercase domain values to UPPERCASE final domain values
-- States: QUEUED, LEASED, RUNNING, RETRY_WAIT, SUCCEEDED, FAILED, CANCELLED

UPDATE jobs SET status = 'QUEUED'      WHERE status IN ('queued', 'PENDING');
UPDATE jobs SET status = 'LEASED'      WHERE status IN ('leased', 'LEASED');
UPDATE jobs SET status = 'RUNNING'     WHERE status IN ('running', 'RUNNING');
UPDATE jobs SET status = 'RETRY_WAIT'  WHERE status IN ('retry_wait', 'RETRY_WAIT');
UPDATE jobs SET status = 'SUCCEEDED'   WHERE status IN ('completed', 'SUCCEEDED');
UPDATE jobs SET status = 'FAILED'      WHERE status IN ('failed', 'FAILED');
UPDATE jobs SET status = 'CANCELLED'   WHERE status IN ('cancelled', 'CANCELLED');
