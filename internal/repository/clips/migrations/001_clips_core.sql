-- 001_initial_schema.sql
-- Consolidated schema for clips table

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
    drive_file_id TEXT NOT NULL DEFAULT '',
    download_link TEXT NOT NULL DEFAULT '',
    tags TEXT NOT NULL DEFAULT '[]',
    source TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    external_url TEXT NOT NULL DEFAULT '',
    duration INTEGER NOT NULL DEFAULT 0,
    metadata TEXT NOT NULL DEFAULT '{}',
    file_hash TEXT NOT NULL DEFAULT '',
    local_path TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    thumb_url TEXT NOT NULL DEFAULT '',
    search_terms TEXT NOT NULL DEFAULT '[]',
    parent_folder_id TEXT DEFAULT '',
    depth INTEGER DEFAULT 0,
    is_folder INTEGER DEFAULT 0,
    duration_seconds REAL,
    width INTEGER DEFAULT 0,
    height INTEGER DEFAULT 0,
    fps REAL DEFAULT 0,
    codec TEXT DEFAULT '',
    size_bytes INTEGER DEFAULT 0,
    processing_stage TEXT,
    error_message TEXT,
    retry_count INTEGER DEFAULT 0,
    last_attempt_at TEXT,
    processed_at TEXT,
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
CREATE INDEX IF NOT EXISTS idx_clips_updated_at ON clips(updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_clips_status ON clips(status);
CREATE INDEX IF NOT EXISTS idx_clips_drive_link ON clips(drive_link) WHERE drive_link != '';
CREATE INDEX IF NOT EXISTS idx_clips_drive_file_id ON clips(drive_file_id) WHERE drive_file_id != '';
CREATE INDEX IF NOT EXISTS idx_clips_duration ON clips(duration);
CREATE INDEX IF NOT EXISTS idx_clips_resolution ON clips(width, height) WHERE width > 0 AND height > 0;
CREATE INDEX IF NOT EXISTS idx_clips_size ON clips(size_bytes DESC) WHERE size_bytes > 0;
CREATE INDEX IF NOT EXISTS idx_clips_fps ON clips(fps) WHERE fps > 0;
CREATE INDEX IF NOT EXISTS idx_clips_processing_stage ON clips(processing_stage) WHERE processing_stage IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_clips_retry_count ON clips(retry_count DESC) WHERE retry_count > 0;
CREATE INDEX IF NOT EXISTS idx_clips_processed_at ON clips(processed_at DESC) WHERE processed_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_clips_search_terms ON clips(search_terms);
CREATE INDEX IF NOT EXISTS idx_clips_parent_folder_id ON clips(parent_folder_id);
CREATE INDEX IF NOT EXISTS idx_clips_is_folder ON clips(is_folder);
CREATE INDEX IF NOT EXISTS idx_clips_parent_folder_sort ON clips(parent_folder_id, is_folder DESC, name ASC);

CREATE UNIQUE INDEX IF NOT EXISTS ux_clips_file_hash ON clips(file_hash) WHERE file_hash IS NOT NULL AND file_hash != '';

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
    status TEXT NOT NULL DEFAULT '',
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
CREATE INDEX IF NOT EXISTS idx_clip_folders_updated_at ON clip_folders(updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_clip_folders_status ON clip_folders(status) WHERE status != '';
CREATE INDEX IF NOT EXISTS idx_clip_folders_clip_count ON clip_folders(clip_count);
