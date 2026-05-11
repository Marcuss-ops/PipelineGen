CREATE TABLE schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
CREATE TABLE clips (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    filename TEXT,
    folder_id TEXT,
    folder_path TEXT,
    group_name TEXT,
    media_type TEXT,
    drive_link TEXT,
    download_link TEXT,
    tags TEXT,
    source TEXT,
    category TEXT,
    external_url TEXT,
    duration INTEGER,
    metadata TEXT,
    file_hash TEXT,
    local_path TEXT,
    created_at DATETIME,
    updated_at DATETIME
, duration_seconds REAL, width INTEGER DEFAULT 0, height INTEGER DEFAULT 0, fps REAL DEFAULT 0, codec TEXT DEFAULT '', size_bytes INTEGER DEFAULT 0, processing_stage TEXT, error_message TEXT, retry_count INTEGER DEFAULT 0, last_attempt_at TEXT, processed_at TEXT, search_terms TEXT NOT NULL DEFAULT '[]', drive_file_id TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '', search_text TEXT DEFAULT '', embedding_json TEXT DEFAULT '[]', scene_type TEXT DEFAULT '', usable_for_json TEXT DEFAULT '[]', avoid_for_json TEXT DEFAULT '[]', quality_score REAL DEFAULT 0.0, reuse_count INTEGER DEFAULT 0, last_used_at TEXT DEFAULT '', last_indexed_at TEXT DEFAULT '', thumb_url TEXT DEFAULT '', parent_folder_id TEXT DEFAULT '', depth INTEGER DEFAULT 0, is_folder INTEGER DEFAULT 0);
CREATE TABLE clip_folders (
    id TEXT PRIMARY KEY,
    source TEXT,
    source_url TEXT,
    video_id TEXT,
    folder_id TEXT,
    folder_path TEXT,
    local_folder_path TEXT,
    group_name TEXT,
    manifest_txt_path TEXT,
    manifest_json_path TEXT,
    clip_count INTEGER DEFAULT 0,
    processed_count INTEGER DEFAULT 0,
    failed_count INTEGER DEFAULT 0,
    skipped_count INTEGER DEFAULT 0,
    last_error TEXT,
    metadata TEXT,
    created_at DATETIME,
    updated_at DATETIME
, search_key TEXT);
CREATE INDEX idx_clips_folder_id ON clips(folder_id);
CREATE INDEX idx_clips_source ON clips(source);
CREATE INDEX idx_clips_group_name ON clips(group_name);
CREATE INDEX idx_clip_folders_source ON clip_folders(source);
CREATE INDEX idx_clip_folders_video_id ON clip_folders(video_id);
CREATE INDEX idx_clips_folder_path ON clips(folder_path);
CREATE INDEX idx_clips_media_type ON clips(media_type);
CREATE INDEX idx_clips_file_hash ON clips(file_hash);
CREATE INDEX idx_clips_created_at ON clips(created_at DESC);
CREATE INDEX idx_clip_folders_folder_id ON clip_folders(folder_id);
CREATE INDEX idx_clip_folders_folder_path ON clip_folders(folder_path);
CREATE INDEX idx_clip_folders_group_name ON clip_folders(group_name);
CREATE INDEX idx_clip_folders_created_at ON clip_folders(created_at DESC);
CREATE TABLE sqlite_sequence(name,seq);
CREATE INDEX idx_clip_folders_search_key ON clip_folders(search_key);
CREATE INDEX idx_clips_updated_at ON clips(updated_at DESC);
CREATE INDEX idx_clips_drive_link ON clips(drive_link) WHERE drive_link != '';
CREATE INDEX idx_clips_duration ON clips(duration);
CREATE UNIQUE INDEX ux_clips_file_hash
ON clips(file_hash)
WHERE file_hash IS NOT NULL AND file_hash != '';
CREATE INDEX idx_clip_folders_updated_at ON clip_folders(updated_at DESC);
CREATE INDEX idx_clip_folders_clip_count ON clip_folders(clip_count);
CREATE INDEX idx_clips_resolution
ON clips(width, height)
WHERE width > 0 AND height > 0;
CREATE INDEX idx_clips_size
ON clips(size_bytes DESC)
WHERE size_bytes > 0;
CREATE INDEX idx_clips_fps
ON clips(fps)
WHERE fps > 0;
CREATE INDEX idx_clips_processing_stage
ON clips(processing_stage)
WHERE processing_stage IS NOT NULL;
CREATE INDEX idx_clips_retry_count
ON clips(retry_count DESC)
WHERE retry_count > 0;
CREATE INDEX idx_clips_processed_at
ON clips(processed_at DESC)
WHERE processed_at IS NOT NULL;
CREATE VIRTUAL TABLE clips_fts USING fts5(id UNINDEXED, name, tags, folder_path, group_name, category, content='clips', content_rowid='rowid')
/* clips_fts(id,name,tags,folder_path,group_name,category) */;
CREATE TABLE IF NOT EXISTS 'clips_fts_data'(id INTEGER PRIMARY KEY, block BLOB);
CREATE TABLE IF NOT EXISTS 'clips_fts_idx'(segid, term, pgno, PRIMARY KEY(segid, term)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS 'clips_fts_docsize'(id INTEGER PRIMARY KEY, sz BLOB);
CREATE TABLE IF NOT EXISTS 'clips_fts_config'(k PRIMARY KEY, v) WITHOUT ROWID;
CREATE INDEX idx_clips_search_terms ON clips(search_terms);
CREATE INDEX idx_clips_search_text ON clips(search_text);
CREATE INDEX idx_clips_category ON clips(category);
CREATE INDEX idx_clips_parent_folder_id ON clips(parent_folder_id);
CREATE INDEX idx_clips_is_folder ON clips(is_folder);
CREATE INDEX idx_clips_parent_folder_sort ON clips(parent_folder_id, is_folder DESC, name ASC);
