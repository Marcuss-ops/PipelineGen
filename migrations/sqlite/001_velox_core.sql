-- 001_initial_schema.sql
-- Consolidated schema for the main (velox) database including Jobs and Asset Index

-- 1. Main Core Tables
CREATE TABLE IF NOT EXISTS monitored_sources (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    url TEXT NOT NULL,
    kind TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    metadata_json TEXT,
    last_harvester_run TEXT,
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS video_metadata (
    video_id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    description TEXT,
    published_at TEXT,
    channel_id TEXT,
    channel_title TEXT,
    tags_json TEXT,
    duration_secs INTEGER,
    view_count INTEGER,
    like_count INTEGER,
    comment_count INTEGER,
    thumbnail_url TEXT,
    category_id TEXT,
    language TEXT,
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS video_stats_history (
    video_id TEXT NOT NULL,
    timestamp TEXT NOT NULL DEFAULT (datetime('now')),
    view_count INTEGER,
    like_count INTEGER,
    comment_count INTEGER,
    PRIMARY KEY (video_id, timestamp),
    FOREIGN KEY (video_id) REFERENCES video_metadata(video_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS artlist_runs (
    id TEXT PRIMARY KEY,
    term TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued',
    root_folder_id TEXT,
    tag_folder_id TEXT,
    requested_count INTEGER DEFAULT 0,
    found_count INTEGER DEFAULT 0,
    processed_count INTEGER DEFAULT 0,
    skipped_count INTEGER DEFAULT 0,
    failed_count INTEGER DEFAULT 0,
    error_message TEXT,
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS scripts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    topic TEXT NOT NULL DEFAULT '',
    duration INTEGER NOT NULL DEFAULT 0,
    language TEXT NOT NULL DEFAULT 'en',
    template TEXT NOT NULL DEFAULT '',
    mode TEXT NOT NULL DEFAULT '',
    narrative_text TEXT,
    timeline_json TEXT,
    entities_json TEXT,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    full_document TEXT,
    model_used TEXT NOT NULL DEFAULT '',
    ollama_base_url TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL DEFAULT 1,
    parent_script_id INTEGER,
    is_deleted INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS script_sections (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    script_id INTEGER NOT NULL,
    section_type TEXT NOT NULL DEFAULT '',
    section_title TEXT NOT NULL DEFAULT '',
    content TEXT,
    sort_order INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (script_id) REFERENCES scripts(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS script_stock_matches (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    script_id INTEGER NOT NULL,
    segment_index INTEGER NOT NULL DEFAULT 0,
    stock_path TEXT NOT NULL DEFAULT '',
    stock_source TEXT NOT NULL DEFAULT '',
    score REAL NOT NULL DEFAULT 0,
    matched_terms TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (script_id) REFERENCES scripts(id) ON DELETE CASCADE
);

-- 2. Media Repository Tables
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

CREATE TABLE IF NOT EXISTS media_tags (
    media_asset_id TEXT NOT NULL,
    tag TEXT NOT NULL,
    PRIMARY KEY (media_asset_id, tag),
    FOREIGN KEY (media_asset_id) REFERENCES media_items(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS asset_tree_nodes (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL,
    asset_id TEXT NOT NULL,
    name TEXT NOT NULL,
    type TEXT NOT NULL,
    parent_id TEXT NOT NULL DEFAULT '',
    root_id TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL DEFAULT '',
    depth INTEGER NOT NULL DEFAULT 0,
    is_folder INTEGER NOT NULL DEFAULT 0,
    drive_file_id TEXT NOT NULL DEFAULT '',
    drive_link TEXT NOT NULL DEFAULT '',
    metadata TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- 3. Jobs System Tables (Merged)
CREATE TABLE IF NOT EXISTS jobs (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued',
    priority INTEGER NOT NULL DEFAULT 0,
    project TEXT NOT NULL DEFAULT '',
    video_name TEXT NOT NULL DEFAULT '',
    active_key TEXT NOT NULL DEFAULT '',
    payload_json TEXT,
    result_json TEXT,
    error TEXT,
    worker_id TEXT,
    lease_expiry TEXT,
    retry_count INTEGER NOT NULL DEFAULT 0,
    max_retries INTEGER NOT NULL DEFAULT 3,
    progress INTEGER NOT NULL DEFAULT 0,
    started_at TEXT,
    completed_at TEXT,
    cancelled_at TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS job_events (
    id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL,
    type TEXT NOT NULL,
    message TEXT NOT NULL DEFAULT '',
    data_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);

-- 4. Asset Index Tables (Merged)
CREATE TABLE IF NOT EXISTS asset_index (
    asset_id TEXT PRIMARY KEY,
    asset_type TEXT NOT NULL,
    source TEXT NOT NULL,
    source_id TEXT NOT NULL,
    operation_key TEXT,
    group_name TEXT,
    subfolder TEXT,
    local_path TEXT,
    drive_link TEXT,
    download_link TEXT,
    file_hash TEXT,
    content_hash TEXT,
    status TEXT NOT NULL DEFAULT 'ready',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS asset_links (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    asset_id TEXT NOT NULL,
    link_type TEXT NOT NULL,
    url TEXT NOT NULL,
    label TEXT,
    FOREIGN KEY (asset_id) REFERENCES asset_index(id) ON DELETE CASCADE
);

-- 5. Indexes
CREATE INDEX IF NOT EXISTS idx_jobs_status_priority ON jobs(status, priority DESC);
CREATE INDEX IF NOT EXISTS idx_jobs_type ON jobs(type);
CREATE INDEX IF NOT EXISTS idx_job_events_job ON job_events(job_id);

CREATE INDEX IF NOT EXISTS idx_asset_index_hash ON asset_index(content_hash);
CREATE INDEX IF NOT EXISTS idx_asset_index_source ON asset_index(source);
CREATE INDEX IF NOT EXISTS idx_asset_index_status ON asset_index(status);

CREATE INDEX IF NOT EXISTS idx_scripts_topic ON scripts(topic);
CREATE INDEX IF NOT EXISTS idx_media_items_status ON media_items(status);
CREATE INDEX IF NOT EXISTS idx_asset_tree_path ON asset_tree_nodes(path);
CREATE INDEX IF NOT EXISTS idx_asset_tree_parent ON asset_tree_nodes(parent_id);
