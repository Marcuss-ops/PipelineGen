-- Materialize the hourly anti-starvation score so claim queries can use an
-- ordered index instead of calculating julianday() and sorting the whole lane
-- for every worker poll. claim_next_age_millis marks the next full queued-hour boundary.
ALTER TABLE jobs ADD COLUMN claim_effective_priority INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN claim_next_age_millis INTEGER NOT NULL DEFAULT 0;

UPDATE jobs
SET claim_effective_priority = priority + COALESCE(
        CAST((julianday('now') - julianday(created_at)) * 24.0 AS INTEGER), 0),
    claim_next_age_millis = CASE
        WHEN julianday(created_at) IS NULL THEN 9223372036854775807
        ELSE CAST((julianday(created_at) - 2440587.5) * 86400000 AS INTEGER) +
            (COALESCE(CAST((julianday('now') - julianday(created_at)) * 24.0 AS INTEGER), 0) + 1) * 3600000
    END;

DROP INDEX IF EXISTS idx_jobs_scheduling_lane;
CREATE INDEX idx_jobs_claim_order
    ON jobs(status, type, claim_effective_priority DESC, created_at ASC, id ASC);
CREATE INDEX idx_jobs_claim_status_order
    ON jobs(status, claim_effective_priority DESC, created_at ASC, id ASC);
CREATE INDEX idx_jobs_claim_lane_order
    ON jobs(status, type, execution_lane, claim_effective_priority DESC, created_at ASC, id ASC);
CREATE INDEX idx_jobs_claim_aging_due
    ON jobs(status, claim_next_age_millis);
