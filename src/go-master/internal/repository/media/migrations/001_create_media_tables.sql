-- 001_create_media_tables.sql
-- Real media repository tables for multi-workspace support.

CREATE TABLE IF NOT EXISTS media_items (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL DEFAULT '',
    project_id TEXT NOT NULL DEFAULT '',
    source_id TEXT NOT NULL,
    source_kind TEXT NOT NULL,
    media_type TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    title TEXT NOT NULL DEFAULT '',
    description TEXT,
    category TEXT,
    external_id TEXT,
    external_url TEXT,
    duration_secs INTEGER DEFAULT 0,
    metadata_json TEXT,
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_media_items_workspace ON media_items(workspace_id);
CREATE INDEX IF NOT EXISTS idx_media_items_project ON media_items(project_id);
CREATE INDEX IF NOT EXISTS idx_media_items_source ON media_items(source_id, source_kind);
CREATE INDEX IF NOT EXISTS idx_media_items_status ON media_items(status);
CREATE INDEX IF NOT EXISTS idx_media_items_created ON media_items(created_at DESC);

CREATE TABLE IF NOT EXISTS media_files (
    id TEXT PRIMARY KEY,
    media_asset_id TEXT NOT NULL,
    location_kind TEXT NOT NULL,
    uri TEXT,
    local_path TEXT,
    drive_link TEXT,
    download_link TEXT,
    mime_type TEXT,
    width INTEGER DEFAULT 0,
    height INTEGER DEFAULT 0,
    duration_secs INTEGER DEFAULT 0,
    file_size_bytes INTEGER DEFAULT 0,
    file_hash TEXT,
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now')),
    FOREIGN KEY (media_asset_id) REFERENCES media_items(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_media_files_asset ON media_files(media_asset_id);
CREATE INDEX IF NOT EXISTS idx_media_files_hash ON media_files(file_hash);
CREATE INDEX IF NOT EXISTS idx_media_files_status ON media_files(status);

CREATE TABLE IF NOT EXISTS media_tags (
    media_asset_id TEXT NOT NULL,
    tag TEXT NOT NULL,
    PRIMARY KEY (media_asset_id, tag),
    FOREIGN KEY (media_asset_id) REFERENCES media_items(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_media_tags_tag ON media_tags(tag);
