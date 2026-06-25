-- 093_create_clip_folders.sql
--
-- Creates the clip_folders table. This table was previously defined in the standalone
-- clips.db database but was missing from the consolidated media.db.sqlite schema, leading
-- to "no such table: clip_folders" errors during manual or scheduled Drive folder syncing.

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
    created_at TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT '',
    search_key TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_clip_folders_search_key ON clip_folders(search_key);
