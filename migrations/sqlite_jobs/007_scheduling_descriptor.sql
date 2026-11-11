-- database: jobs
-- Typed scheduling projection for the hot claim path. Job type and priority
-- remain their canonical jobs columns; this migration adds only the explicit
-- resource lane and availability fields needed to avoid payload hydration just
-- to decide which worker pool may claim a row.
ALTER TABLE jobs ADD COLUMN resource_class TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN execution_lane TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN available_at TEXT;

CREATE INDEX IF NOT EXISTS idx_jobs_scheduling_lane
    ON jobs(status, type, execution_lane, available_at, priority DESC, created_at ASC, id ASC);
