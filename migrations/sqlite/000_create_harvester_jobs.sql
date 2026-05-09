-- 000_create_harvester_jobs.sql
-- Missing table harvester_jobs required for performance indexes and tests

CREATE TABLE IF NOT EXISTS harvester_jobs (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL,
    external_id TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    duration INTEGER DEFAULT 0,
    view_count INTEGER DEFAULT 0,
    published_at TEXT,
    error TEXT,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_harvester_jobs_source ON harvester_jobs(source);
CREATE INDEX IF NOT EXISTS idx_harvester_jobs_status ON harvester_jobs(status);
