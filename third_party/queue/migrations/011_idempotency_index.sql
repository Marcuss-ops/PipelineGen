-- A retry with the same logical key must resolve to one canonical job.
CREATE UNIQUE INDEX IF NOT EXISTS render_jobs_idempotency_key_unique
    ON render_jobs (idempotency_key)
    WHERE idempotency_key IS NOT NULL AND idempotency_key <> '';
