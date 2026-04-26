-- 0003_clips.sql
-- Table for storing clip metadata, replacing clip_index.json
-- SQLite-compatible version

CREATE TABLE IF NOT EXISTS clips (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    filename TEXT NOT NULL,
    folder_id TEXT,
    folder_path TEXT,
    group_name TEXT,
    media_type TEXT DEFAULT 'clip',
    drive_link TEXT,
    download_link TEXT,
    tags TEXT, -- JSON array of tags
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_clips_name ON clips (name);
CREATE INDEX IF NOT EXISTS idx_clips_group ON clips (group_name);
CREATE INDEX IF NOT EXISTS idx_clips_media_type ON clips (media_type);

-- Table for artlist-specific stock assets if needed (for now we use clips table)
-- but we might want a specific table for stock indexing checkpoints
CREATE TABLE IF NOT EXISTS indexing_checkpoints (
    id TEXT PRIMARY KEY,
    path TEXT NOT NULL,
    last_indexed_at TEXT NOT NULL,
    metadata TEXT -- Extra info like folder_id for Drive
);
