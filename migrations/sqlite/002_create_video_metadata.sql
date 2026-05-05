-- 002_create_video_metadata.sql
-- Video metadata table for storing video file information

CREATE TABLE IF NOT EXISTS video_metadata (
    id TEXT PRIMARY KEY,
    clip_id TEXT NOT NULL,
    duration INTEGER DEFAULT 0,
    width INTEGER DEFAULT 0,
    height INTEGER DEFAULT 0,
    fps REAL DEFAULT 0,
    codec TEXT DEFAULT '',
    bitrate INTEGER DEFAULT 0,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_video_metadata_clip ON video_metadata(clip_id);
CREATE INDEX IF NOT EXISTS idx_video_metadata_created ON video_metadata(created_at DESC);
