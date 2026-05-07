-- 003_add_asset_tags.sql
-- Asset tags table for structured tag management

CREATE TABLE IF NOT EXISTS asset_tags (
    asset_id TEXT NOT NULL,
    tag TEXT NOT NULL,
    source TEXT,
    confidence REAL DEFAULT 1.0,
    created_at TEXT DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (asset_id, tag),
    FOREIGN KEY (asset_id) REFERENCES asset_index(asset_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_asset_tags_tag
ON asset_tags(tag);

CREATE INDEX IF NOT EXISTS idx_asset_tags_asset_id
ON asset_tags(asset_id);

CREATE INDEX IF NOT EXISTS idx_asset_tags_source
ON asset_tags(source)
WHERE source IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_asset_tags_confidence
ON asset_tags(confidence DESC)
WHERE confidence > 0;
