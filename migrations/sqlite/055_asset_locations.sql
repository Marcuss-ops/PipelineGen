-- Migration 055: asset_locations — normalised physical locations for media assets.
--
-- Separates the physical location of an asset (local path, Drive file ID,
-- object storage URI) from the asset's identity (media_assets). An asset
-- can have multiple locations (e.g. a local download + a Drive copy),
-- with one designated as primary (is_primary=1).
--
-- The media_assets.drive_link / local_path / download_link / drive_file_id
-- columns remain for backward compatibility; those columns will be deprecated
-- and migrated into this table in a future PR.
CREATE TABLE IF NOT EXISTS asset_locations (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    asset_id        TEXT NOT NULL
                    REFERENCES media_assets(id)
                    ON DELETE CASCADE,
    location_kind   TEXT NOT NULL        -- 'local' | 'drive' | 'object_storage'
                    CHECK (location_kind IN ('local', 'drive', 'object_storage')),
    uri             TEXT NOT NULL,       -- local path, drive_file_id, s3://...
    mime_type       TEXT NOT NULL DEFAULT '',
    file_size_bytes INTEGER NOT NULL DEFAULT 0,
    file_hash       TEXT NOT NULL DEFAULT '',
    is_primary      INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL DEFAULT '',
    updated_at      TEXT NOT NULL DEFAULT '',
    UNIQUE (asset_id, location_kind)
);

CREATE INDEX IF NOT EXISTS idx_asset_locations_asset
    ON asset_locations (asset_id);

CREATE INDEX IF NOT EXISTS idx_asset_locations_primary
    ON asset_locations (asset_id)
    WHERE is_primary = 1;
