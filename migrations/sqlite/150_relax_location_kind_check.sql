-- 150_relax_location_kind_check.sql
--
-- Relax CHECK constraint on asset_locations(location_kind) to allow suffixes
-- like "local_master", "drive_master", etc. for per-rendition locations.
-- Recreates both asset_locations and asset_renditions to satisfy SQLite's transaction foreign key checks.

-- Clean up orphaned locations that reference non-existent media_assets to prevent foreign key errors during insert
DELETE FROM asset_locations WHERE asset_id NOT IN (SELECT id FROM media_assets);

DROP TABLE IF EXISTS asset_renditions;

CREATE TABLE asset_locations_new (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    asset_id        TEXT NOT NULL
                    REFERENCES media_assets(id)
                    ON DELETE CASCADE,
    location_kind   TEXT NOT NULL,
    uri             TEXT NOT NULL,       
    mime_type       TEXT NOT NULL DEFAULT '',
    file_size_bytes INTEGER NOT NULL DEFAULT 0,
    file_hash       TEXT NOT NULL DEFAULT '',
    is_primary      INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL DEFAULT '',
    updated_at      TEXT NOT NULL DEFAULT '',
    external_id     TEXT NOT NULL DEFAULT '',
    access_url      TEXT NOT NULL DEFAULT '',
    download_url    TEXT NOT NULL DEFAULT '',
    web_view_link   TEXT NOT NULL DEFAULT '',
    UNIQUE (asset_id, location_kind)
);

INSERT INTO asset_locations_new (id, asset_id, location_kind, uri, mime_type, file_size_bytes, file_hash, is_primary, created_at, updated_at, external_id, access_url, download_url, web_view_link)
SELECT id, asset_id, location_kind, uri, mime_type, file_size_bytes, file_hash, is_primary, created_at, updated_at, external_id, access_url, download_url, web_view_link FROM asset_locations;

DROP TABLE asset_locations;
ALTER TABLE asset_locations_new RENAME TO asset_locations;

CREATE INDEX idx_asset_locations_asset ON asset_locations (asset_id);
CREATE INDEX idx_asset_locations_primary ON asset_locations (asset_id) WHERE is_primary = 1;

CREATE TABLE asset_renditions (
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

CREATE INDEX idx_asset_renditions_asset ON asset_renditions (asset_id);
CREATE INDEX idx_asset_renditions_location ON asset_renditions (location_id);
CREATE INDEX idx_asset_renditions_kind ON asset_renditions (kind);
CREATE UNIQUE INDEX IF NOT EXISTS ux_asset_renditions_asset_kind
    ON asset_renditions (asset_id, kind);
