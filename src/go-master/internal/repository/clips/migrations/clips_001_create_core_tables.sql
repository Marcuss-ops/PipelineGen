-- clips_001_create_core_tables.sql
-- Schema iniziale per clips.db.sqlite

-- Tabella principale clip
CREATE TABLE IF NOT EXISTS clips (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL DEFAULT '',
    filename TEXT NOT NULL DEFAULT '',
    folder_id TEXT NOT NULL DEFAULT '',
    folder_path TEXT NOT NULL DEFAULT '',
    group_name TEXT NOT NULL DEFAULT '',
    media_type TEXT NOT NULL DEFAULT '',
    drive_link TEXT NOT NULL DEFAULT '',
    download_link TEXT NOT NULL DEFAULT '',
    tags TEXT NOT NULL DEFAULT '[]',
    source TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    external_url TEXT NOT NULL DEFAULT '',
    duration INTEGER NOT NULL DEFAULT 0,
    metadata TEXT NOT NULL DEFAULT '{}',
    file_hash TEXT NOT NULL DEFAULT '',
    local_path TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- Indici per clips
CREATE INDEX IF NOT EXISTS idx_clips_folder_id ON clips(folder_id);
CREATE INDEX IF NOT EXISTS idx_clips_folder_path ON clips(folder_path);
CREATE INDEX IF NOT EXISTS idx_clips_group_name ON clips(group_name);
CREATE INDEX IF NOT EXISTS idx_clips_source ON clips(source);
CREATE INDEX IF NOT EXISTS idx_clips_media_type ON clips(media_type);
CREATE INDEX IF NOT EXISTS idx_clips_file_hash ON clips(file_hash);
CREATE INDEX IF NOT EXISTS idx_clips_created_at ON clips(created_at DESC);

-- Tabella cartelle clip
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

-- Indici per clip_folders
CREATE INDEX IF NOT EXISTS idx_clip_folders_source ON clip_folders(source);
CREATE INDEX IF NOT EXISTS idx_clip_folders_video_id ON clip_folders(video_id);
CREATE INDEX IF NOT EXISTS idx_clip_folders_folder_id ON clip_folders(folder_id);
CREATE INDEX IF NOT EXISTS idx_clip_folders_folder_path ON clip_folders(folder_path);
CREATE INDEX IF NOT EXISTS idx_clip_folders_group_name ON clip_folders(group_name);
CREATE INDEX IF NOT EXISTS idx_clip_folders_created_at ON clip_folders(created_at DESC);
