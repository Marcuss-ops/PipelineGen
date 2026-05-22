CREATE TABLE IF NOT EXISTS clip_folders (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL DEFAULT '',
    source_url TEXT NOT NULL DEFAULT '',
    video_id TEXT NOT NULL DEFAULT '',
    folder_id TEXT NOT NULL DEFAULT '',
    folder_path TEXT NOT NULL DEFAULT '',
    local_folder_path TEXT NOT NULL DEFAULT '',
    group_name TEXT NOT NULL DEFAULT '',
    manifest_txt_path TEXT NOT NULL DEFAULT '',
    manifest_json_path TEXT NOT NULL DEFAULT '',
    clip_count INTEGER NOT NULL DEFAULT 0,
    processed_count INTEGER NOT NULL DEFAULT 0,
    failed_count INTEGER NOT NULL DEFAULT 0,
    skipped_count INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    metadata TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_clip_folders_source ON clip_folders(source);
CREATE INDEX IF NOT EXISTS idx_clip_folders_video_id ON clip_folders(video_id);
CREATE INDEX IF NOT EXISTS idx_clip_folders_folder_id ON clip_folders(folder_id);
CREATE INDEX IF NOT EXISTS idx_clip_folders_folder_path ON clip_folders(folder_path);
CREATE INDEX IF NOT EXISTS idx_clip_folders_group_name ON clip_folders(group_name);
CREATE INDEX IF NOT EXISTS idx_clip_folders_created_at ON clip_folders(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_clip_folders_updated_at ON clip_folders(updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_clip_folders_clip_count ON clip_folders(clip_count);

CREATE TABLE IF NOT EXISTS segment_embeddings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    script_key TEXT NOT NULL,
    source_hash TEXT NOT NULL DEFAULT '',
    topic TEXT NOT NULL DEFAULT '',
    language TEXT NOT NULL DEFAULT '',
    template TEXT NOT NULL DEFAULT '',
    duration INTEGER NOT NULL DEFAULT 0,
    segment_index INTEGER NOT NULL,
    raw_subject TEXT NOT NULL DEFAULT '',
    canonical_subject TEXT NOT NULL DEFAULT '',
    raw_keywords_json TEXT NOT NULL DEFAULT '[]',
    canonical_keywords_json TEXT NOT NULL DEFAULT '[]',
    raw_entities_json TEXT NOT NULL DEFAULT '[]',
    canonical_entities_json TEXT NOT NULL DEFAULT '[]',
    segment_json TEXT NOT NULL DEFAULT '{}',
    embedding_json TEXT NOT NULL DEFAULT '[]',
    best_source TEXT NOT NULL DEFAULT '',
    best_path TEXT NOT NULL DEFAULT '',
    best_link TEXT NOT NULL DEFAULT '',
    best_score INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_segment_embeddings_script_key ON segment_embeddings(script_key);
CREATE UNIQUE INDEX IF NOT EXISTS idx_segment_embeddings_key_segment ON segment_embeddings(script_key, segment_index);
