-- 139_asset_releases.sql
--
-- Release table for downloaded/procured assets.
-- One row per asset release record, storing model/property release status,
-- certificate and receipt references.
CREATE TABLE IF NOT EXISTS asset_releases (
    id TEXT PRIMARY KEY,
    asset_id TEXT NOT NULL,
    release_type TEXT NOT NULL CHECK (release_type IN ('model', 'property', 'both')),
    model_release_url TEXT,
    model_release_path TEXT,
    property_release_url TEXT,
    property_release_path TEXT,
    certificate_url TEXT,
    certificate_path TEXT,
    receipt_url TEXT,
    receipt_path TEXT,
    status TEXT DEFAULT 'pending',
    verified_at TEXT,
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now')),

    FOREIGN KEY (asset_id) REFERENCES media_assets(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_asset_releases_asset
    ON asset_releases (asset_id);

CREATE INDEX IF NOT EXISTS idx_asset_releases_status
    ON asset_releases (status);
