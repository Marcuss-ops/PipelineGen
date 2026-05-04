CREATE TABLE IF NOT EXISTS asset_index (
    asset_id TEXT PRIMARY KEY,
    asset_type TEXT NOT NULL,
    source TEXT NOT NULL,
    source_id TEXT,
    operation_key TEXT,
    group_name TEXT,
    subfolder TEXT,
    local_path TEXT,
    drive_link TEXT,
    download_link TEXT,
    file_hash TEXT,
    content_hash TEXT,
    status TEXT NOT NULL,
    metadata_json TEXT DEFAULT '{}',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_asset_index_source
ON asset_index(source, source_id);

CREATE INDEX IF NOT EXISTS idx_asset_index_hash
ON asset_index(content_hash);

CREATE INDEX IF NOT EXISTS idx_asset_index_group
ON asset_index(group_name, subfolder);

CREATE INDEX IF NOT EXISTS idx_asset_index_status
ON asset_index(status);
