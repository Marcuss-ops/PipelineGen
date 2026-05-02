CREATE TABLE IF NOT EXISTS clip_folders (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL DEFAULT 'youtube',
    source_url TEXT DEFAULT '',
    video_id TEXT DEFAULT '',
    folder_id TEXT DEFAULT '',
    folder_path TEXT DEFAULT '',
    local_folder_path TEXT DEFAULT '',
    group_name TEXT DEFAULT '',
    manifest_txt_path TEXT DEFAULT '',
    manifest_json_path TEXT DEFAULT '',
    clip_count INTEGER DEFAULT 0,
    processed_count INTEGER DEFAULT 0,
    failed_count INTEGER DEFAULT 0,
    last_error TEXT DEFAULT '',
    metadata TEXT DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
