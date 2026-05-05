-- 001_create_monitored_sources.sql
-- Monitored sources table for tracking YouTube channels and other sources

CREATE TABLE IF NOT EXISTS monitored_sources (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL DEFAULT '',
    external_id TEXT NOT NULL DEFAULT '',
    external_url TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    channel_id TEXT NOT NULL DEFAULT '',
    channel_url TEXT NOT NULL DEFAULT '',
    keyword TEXT NOT NULL DEFAULT '',
    group_name TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'active',
    last_seen_at TEXT,
    last_checked_at TEXT,
    processed_count INTEGER NOT NULL DEFAULT 0,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_monitored_sources_source ON monitored_sources(source);
CREATE INDEX IF NOT EXISTS idx_monitored_sources_external ON monitored_sources(external_id);
CREATE INDEX IF NOT EXISTS idx_monitored_sources_status ON monitored_sources(status);
CREATE INDEX IF NOT EXISTS idx_monitored_sources_group ON monitored_sources(group_name);
CREATE INDEX IF NOT EXISTS idx_monitored_sources_created ON monitored_sources(created_at DESC);
