-- Clip metadata schema for artlist.db.sqlite and clips.db.sqlite
-- Compatible with mattn/go-sqlite3, no FTS5 (use LIKE for search)

CREATE TABLE IF NOT EXISTS clips (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    clip_id TEXT NOT NULL UNIQUE,
    path TEXT NOT NULL,
    duration REAL NOT NULL DEFAULT 0.0,
    tags TEXT DEFAULT '[]',
    search_text TEXT,
    tags_struct TEXT,
    category TEXT,
    scene_type TEXT,
    usable_for TEXT,
    avoid_for TEXT,
    embedding TEXT,
    quality_score REAL DEFAULT 0.0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_clips_search_text ON clips(search_text);
CREATE INDEX IF NOT EXISTS idx_clips_category ON clips(category);
CREATE INDEX IF NOT EXISTS idx_clips_scene_type ON clips(scene_type);
CREATE INDEX IF NOT EXISTS idx_clips_duration ON clips(duration);