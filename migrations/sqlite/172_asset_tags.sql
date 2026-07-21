-- database: primary
-- Migration 172: asset tags with provenance.
-- Each tag is kept in a dedicated row with an explicit source, so provider,
-- semantic, manual, transcript, visual and imported tags live in the same
-- table but are never mixed up. normalized_tag is the canonical search key.
CREATE TABLE IF NOT EXISTS asset_tags (
    asset_id TEXT NOT NULL,
    tag TEXT NOT NULL,
    normalized_tag TEXT NOT NULL,
    source TEXT NOT NULL,
    confidence REAL,
    language TEXT,
    created_at TEXT DEFAULT (datetime('now')),
    PRIMARY KEY(asset_id, normalized_tag, source),
    FOREIGN KEY(asset_id) REFERENCES media_assets(id) ON DELETE CASCADE,
    CHECK (source IN ('provider', 'semantic', 'manual', 'transcript', 'visual', 'import'))
);

CREATE INDEX IF NOT EXISTS idx_asset_tags_asset_id
    ON asset_tags(asset_id);

CREATE INDEX IF NOT EXISTS idx_asset_tags_normalized_tag
    ON asset_tags(normalized_tag);
