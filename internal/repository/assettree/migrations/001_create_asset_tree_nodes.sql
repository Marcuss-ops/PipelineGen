-- 001_create_asset_tree_nodes.sql
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

CREATE INDEX IF NOT EXISTS idx_asset_tree_source_parent ON asset_tree_nodes(source, parent_id);
CREATE INDEX IF NOT EXISTS idx_asset_tree_root ON asset_tree_nodes(root_id);
CREATE INDEX IF NOT EXISTS idx_asset_tree_path ON asset_tree_nodes(path);
CREATE INDEX IF NOT EXISTS idx_asset_tree_is_folder ON asset_tree_nodes(is_folder);
