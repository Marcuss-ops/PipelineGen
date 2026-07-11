-- 138_asset_licenses.sql
--
-- License table for downloaded/procured assets.
-- One row per asset license, storing the contractual terms, receipt and
-- certificate references so compliance can be verified later.
CREATE TABLE IF NOT EXISTS asset_licenses (
    id TEXT PRIMARY KEY,
    provider TEXT NOT NULL,
    account_id TEXT NOT NULL DEFAULT 'default',
    project_id TEXT,
    asset_id TEXT NOT NULL,
    license_type TEXT NOT NULL DEFAULT 'standard',
    license_name TEXT,
    license_url TEXT,
    license_terms TEXT,
    receipt_url TEXT,
    receipt_path TEXT,
    certificate_url TEXT,
    certificate_path TEXT,
    valid_from TEXT,
    valid_until TEXT,
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now')),

    FOREIGN KEY (asset_id) REFERENCES media_assets(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_asset_licenses_asset
    ON asset_licenses (asset_id);

CREATE INDEX IF NOT EXISTS idx_asset_licenses_provider_account
    ON asset_licenses (provider, account_id);

CREATE INDEX IF NOT EXISTS idx_asset_licenses_project
    ON asset_licenses (project_id);
