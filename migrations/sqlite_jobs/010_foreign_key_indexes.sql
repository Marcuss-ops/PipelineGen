-- database: jobs
-- DB-07 (2026-11-27): FK child columns in the jobs plane. job_payloads is
-- 1:1 with jobs but had no index on job_id; job_registry_metrics references
-- job_steps(step_id) without one.

CREATE INDEX IF NOT EXISTS idx_fk_job_payloads_job_id ON job_payloads(job_id);
CREATE INDEX IF NOT EXISTS idx_fk_job_registry_metrics_step_id ON job_registry_metrics(step_id);
