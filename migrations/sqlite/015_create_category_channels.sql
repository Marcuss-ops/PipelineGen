-- 015_create_category_channels.sql
-- Per-category YouTube channel subscriptions
-- Each Drive folder category can have multiple channels to monitor.
-- Channels are checked during Channel Monitor cycles, and their videos
-- are automatically classified into the assigned category.

CREATE TABLE IF NOT EXISTS category_channels (
    id TEXT PRIMARY KEY,
    category TEXT NOT NULL,           -- e.g. "boxe", "rap", "comedy"
    channel_url TEXT NOT NULL,         -- YouTube channel URL
    channel_name TEXT NOT NULL DEFAULT '',
    keywords TEXT NOT NULL DEFAULT '[]',  -- JSON array of title-level filter keywords
    min_views INTEGER NOT NULL DEFAULT 0,
    max_clip_duration INTEGER NOT NULL DEFAULT 60,
    drive_folder_id TEXT NOT NULL DEFAULT '',  -- Optional specific Drive folder ID

    -- Semantic matching via transcript analysis (added June 2026)
    semantic_keywords TEXT NOT NULL DEFAULT '[]',  -- JSON array of themes for transcript-level matching
    min_semantic_score INTEGER NOT NULL DEFAULT 60,  -- Minimum Ollama score (0-100) to accept match
    playlist_end INTEGER NOT NULL DEFAULT -1,  -- Per-channel override: 0=all videos, -1=use global default, >0=count

    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_category_channels_category ON category_channels(category);
CREATE INDEX IF NOT EXISTS idx_category_channels_url ON category_channels(channel_url);
