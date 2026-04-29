-- 0005_harvester_jobs.sql
-- Migration to create harvester_jobs table for persistent cron job storage

CREATE TABLE IF NOT EXISTS harvester_jobs (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    query TEXT NOT NULL,
    channel TEXT,
    interval TEXT NOT NULL,
    enabled BOOLEAN DEFAULT 1,
    last_run_at TEXT,
    next_run_at TEXT,
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_harvester_jobs_enabled ON harvester_jobs(enabled);
CREATE INDEX IF NOT EXISTS idx_harvester_jobs_next_run ON harvester_jobs(next_run_at);
