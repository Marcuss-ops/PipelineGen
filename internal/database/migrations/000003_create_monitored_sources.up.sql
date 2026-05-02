CREATE TABLE IF NOT EXISTS monitored_sources (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL,
    external_id TEXT DEFAULT '',
    external_url TEXT NOT NULL,
    title TEXT DEFAULT '',
    channel_id TEXT DEFAULT '',
    channel_url TEXT DEFAULT '',
    keyword TEXT DEFAULT '',
    group_name TEXT DEFAULT '',
    category TEXT DEFAULT '',
    status TEXT NOT NULL DEFAULT 'discovered',
    last_seen_at TEXT,
    last_checked_at TEXT,
    processed_count INTEGER NOT NULL DEFAULT 0,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_monitored_sources_source_external_url
ON monitored_sources(source, external_url);

CREATE INDEX IF NOT EXISTS idx_monitored_sources_keyword
ON monitored_sources(keyword);

CREATE INDEX IF NOT EXISTS idx_monitored_sources_status
ON monitored_sources(status);
