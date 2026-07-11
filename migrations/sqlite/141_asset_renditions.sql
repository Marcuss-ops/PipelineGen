-- 141_asset_renditions.sql
--
-- Rendition table for media assets. Each row represents a single technical
-- variant (master, mezzanine, proxy, thumbnail, etc.) linked to an asset
-- and optionally to a specific asset_locations row.
CREATE TABLE IF NOT EXISTS asset_renditions (
    id TEXT PRIMARY KEY,
    asset_id TEXT NOT NULL,
    location_id INTEGER,
    kind TEXT NOT NULL DEFAULT 'master',
    container TEXT,
    codec TEXT,
    width INTEGER,
    height INTEGER,
    fps REAL,
    bitrate INTEGER,
    color_space TEXT,
    sha256 TEXT,
    size_bytes INTEGER NOT NULL DEFAULT 0,
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now')),

    FOREIGN KEY (asset_id) REFERENCES media_assets(id) ON DELETE CASCADE,
    FOREIGN KEY (location_id) REFERENCES asset_locations(id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_asset_renditions_asset
    ON asset_renditions (asset_id);

CREATE INDEX IF NOT EXISTS idx_asset_renditions_location
    ON asset_renditions (location_id);

CREATE INDEX IF NOT EXISTS idx_asset_renditions_kind
    ON asset_renditions (kind);
