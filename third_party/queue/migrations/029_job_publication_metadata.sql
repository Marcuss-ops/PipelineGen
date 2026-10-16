-- Add the optional worker publication intent and daemon admission timing to
-- the durable job row. Defaults keep existing jobs store-only with no delay.
ALTER TABLE render_jobs
    ADD COLUMN publication_policy TEXT NOT NULL DEFAULT '',
    ADD COLUMN daemon_admission_wait_ms DOUBLE PRECISION NOT NULL DEFAULT 0;

ALTER TABLE render_jobs
    ADD CONSTRAINT render_jobs_daemon_admission_wait_nonnegative
    CHECK (daemon_admission_wait_ms >= 0 AND daemon_admission_wait_ms < 'Infinity'::double precision);
