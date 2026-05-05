-- 003_create_video_stats_history.sql
-- Video stats history for tracking statistics over time

CREATE TABLE IF NOT EXISTS video_stats_history (
    id TEXT PRIMARY KEY,
    clip_id TEXT NOT NULL,
    stat_type TEXT NOT NULL,
    value REAL DEFAULT 0,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_video_stats_clip ON video_stats_history(clip_id);
CREATE INDEX IF NOT EXISTS idx_video_stats_type ON video_stats_history(stat_type);
CREATE INDEX IF NOT EXISTS idx_video_stats_created ON video_stats_history(created_at DESC);
