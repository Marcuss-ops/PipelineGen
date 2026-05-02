-- Media sources table
CREATE TABLE IF NOT EXISTS media_sources (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    kind TEXT NOT NULL CHECK(kind IN ('artlist', 'stock', 'youtube', 'drive', 'manual')),
    name TEXT NOT NULL,
    external_id TEXT,
    external_url TEXT,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY(workspace_id) REFERENCES workspaces(id)
);

-- Media items table (canonical clip/logical media)
CREATE TABLE IF NOT EXISTS media_items (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    project_id TEXT,
    source_id TEXT,
    source_kind TEXT NOT NULL CHECK(source_kind IN ('artlist', 'stock', 'youtube', 'drive', 'manual')),
    media_type TEXT NOT NULL CHECK(media_type IN ('video', 'audio', 'image', 'youtube_clip', 'stock_clip', 'artlist_clip')),
    status TEXT NOT NULL CHECK(status IN ('discovered', 'downloaded', 'normalized', 'uploaded', 'ready', 'failed', 'archived')),
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    external_id TEXT,
    external_url TEXT,
    duration_seconds INTEGER DEFAULT 0,
    file_hash TEXT,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY(workspace_id) REFERENCES workspaces(id),
    FOREIGN KEY(project_id) REFERENCES projects(id),
    FOREIGN KEY(source_id) REFERENCES media_sources(id)
);

-- Media files table (physical locations)
CREATE TABLE IF NOT EXISTS media_files (
    id TEXT PRIMARY KEY,
    media_item_id TEXT NOT NULL,
    location_kind TEXT NOT NULL CHECK(location_kind IN ('url', 'local', 'drive', 's3', 'other')),
    uri TEXT NOT NULL,
    mime_type TEXT,
    width INTEGER,
    height INTEGER,
    duration_seconds INTEGER DEFAULT 0,
    file_size_bytes INTEGER DEFAULT 0,
    file_hash TEXT,
    status TEXT NOT NULL CHECK(status IN ('discovered', 'downloaded', 'normalized', 'uploaded', 'ready', 'failed', 'archived')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY(media_item_id) REFERENCES media_items(id)
);

-- Media tags table
CREATE TABLE IF NOT EXISTS media_tags (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    name TEXT NOT NULL,
    normalized_name TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE(workspace_id, normalized_name),
    FOREIGN KEY(workspace_id) REFERENCES workspaces(id)
);

-- Media item tags junction table
CREATE TABLE IF NOT EXISTS media_item_tags (
    media_item_id TEXT NOT NULL,
    tag_id TEXT NOT NULL,
    PRIMARY KEY(media_item_id, tag_id),
    FOREIGN KEY(media_item_id) REFERENCES media_items(id),
    FOREIGN KEY(tag_id) REFERENCES media_tags(id)
);

-- Media usage table
CREATE TABLE IF NOT EXISTS media_usage (
    id TEXT PRIMARY KEY,
    media_item_id TEXT NOT NULL,
    project_id TEXT,
    script_id TEXT,
    usage_kind TEXT NOT NULL CHECK(usage_kind IN ('script_asset', 'timeline_clip', 'thumbnail', 'background')),
    used_at TEXT NOT NULL,
    FOREIGN KEY(media_item_id) REFERENCES media_items(id)
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_media_sources_workspace_id ON media_sources(workspace_id);
CREATE INDEX IF NOT EXISTS idx_media_sources_kind ON media_sources(kind);

CREATE INDEX IF NOT EXISTS idx_media_items_workspace_id ON media_items(workspace_id);
CREATE INDEX IF NOT EXISTS idx_media_items_project_id ON media_items(project_id);
CREATE INDEX IF NOT EXISTS idx_media_items_source_id ON media_items(source_id);
CREATE INDEX IF NOT EXISTS idx_media_items_source_kind ON media_items(source_kind);
CREATE INDEX IF NOT EXISTS idx_media_items_media_type ON media_items(media_type);
CREATE INDEX IF NOT EXISTS idx_media_items_status ON media_items(status);
CREATE INDEX IF NOT EXISTS idx_media_items_external_id ON media_items(external_id);

CREATE INDEX IF NOT EXISTS idx_media_files_media_item_id ON media_files(media_item_id);
CREATE INDEX IF NOT EXISTS idx_media_files_location_kind ON media_files(location_kind);

CREATE INDEX IF NOT EXISTS idx_media_tags_workspace_id ON media_tags(workspace_id);
CREATE INDEX IF NOT EXISTS idx_media_tags_normalized_name ON media_tags(normalized_name);

CREATE INDEX IF NOT EXISTS idx_media_usage_media_item_id ON media_usage(media_item_id);
CREATE INDEX IF NOT EXISTS idx_media_usage_project_id ON media_usage(project_id);
CREATE INDEX IF NOT EXISTS idx_media_usage_script_id ON media_usage(script_id);
