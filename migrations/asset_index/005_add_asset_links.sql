-- 005_add_asset_links.sql
-- Asset links table for connecting related assets

CREATE TABLE IF NOT EXISTS asset_links (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_asset_id TEXT NOT NULL,
    target_asset_id TEXT NOT NULL,
    relation_type TEXT NOT NULL,
    score REAL DEFAULT 0,
    metadata TEXT,
    created_at TEXT DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(source_asset_id, target_asset_id, relation_type),
    FOREIGN KEY (source_asset_id) REFERENCES asset_index(asset_id) ON DELETE CASCADE,
    FOREIGN KEY (target_asset_id) REFERENCES asset_index(asset_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_asset_links_source
ON asset_links(source_asset_id);

CREATE INDEX IF NOT EXISTS idx_asset_links_target
ON asset_links(target_asset_id);

CREATE INDEX IF NOT EXISTS idx_asset_links_relation
ON asset_links(relation_type);

CREATE INDEX IF NOT EXISTS idx_asset_links_score
ON asset_links(score DESC)
WHERE score > 0;

CREATE INDEX IF NOT EXISTS idx_asset_links_composite
ON asset_links(source_asset_id, relation_type, score DESC);
