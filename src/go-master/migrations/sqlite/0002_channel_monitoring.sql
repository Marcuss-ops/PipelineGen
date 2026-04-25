-- 0002_channel_monitoring.sql
-- Schema per il tracciamento dei canali e dei video tramite YouTube Data API v3
-- SQLite-compatible version

CREATE TABLE IF NOT EXISTS monitored_channels (
    channel_id TEXT PRIMARY KEY,
    title TEXT,
    uploads_playlist_id TEXT NOT NULL,
    last_checked_at TEXT,
    config TEXT DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS video_metadata (
    video_id TEXT PRIMARY KEY,
    channel_id TEXT REFERENCES monitored_channels(channel_id),
    title TEXT NOT NULL,
    description TEXT,
    published_at TEXT NOT NULL,
    duration_sec INTEGER,
    view_count INTEGER DEFAULT 0,
    like_count INTEGER DEFAULT 0,
    comment_count INTEGER DEFAULT 0,
    category_id TEXT,
    tags TEXT,
    language TEXT,
    status TEXT DEFAULT 'discovered',
    gemma_classification TEXT,
    drive_folder_id TEXT,
    drive_file_id TEXT,
    last_synced_at TEXT DEFAULT (datetime('now')),
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_video_metadata_channel_published
    ON video_metadata (channel_id, published_at DESC);

CREATE INDEX IF NOT EXISTS idx_video_metadata_status
    ON video_metadata (status);

CREATE TABLE IF NOT EXISTS video_stats_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    video_id TEXT NOT NULL REFERENCES video_metadata(video_id) ON DELETE CASCADE,
    view_count INTEGER NOT NULL,
    like_count INTEGER,
    recorded_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_video_stats_history_video_recorded
    ON video_stats_history (video_id, recorded_at DESC);
