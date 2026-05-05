-- 001_create_harvester_jobs.sql
-- Harvester jobs table for tracking YouTube channel harvesting

CREATE TABLE IF NOT EXISTS harvester_jobs (
    id TEXT PRIMARY KEY,
    channel_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    video_id TEXT DEFAULT '',
    title TEXT DEFAULT '',
    duration INTEGER DEFAULT 0,
    view_count INTEGER DEFAULT 0,
    published_at TEXT,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_harvester_jobs_channel ON harvester_jobs(channel_id);
CREATE INDEX IF NOT EXISTS idx_harvester_jobs_status ON harvester_jobs(status);
CREATE INDEX IF NOT EXISTS idx_harvester_jobs_video ON harvester_jobs(video_id);
CREATE INDEX IF NOT EXISTS idx_harvester_jobs_created ON harvester_jobs(created_at DESC);
