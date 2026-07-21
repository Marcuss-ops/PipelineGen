-- database: primary
-- Migration 171: structured provider metadata for any external catalog asset.
-- Keeps provider-provided metadata (creator, country, page URL, raw JSON)
-- in a dedicated table so the media_assets row stays canonical and provider
-- details remain queryable without parsing metadata_json.
CREATE TABLE IF NOT EXISTS asset_provider_metadata (
    asset_id TEXT PRIMARY KEY,
    provider TEXT NOT NULL,
    external_id TEXT NOT NULL,
    creator TEXT,
    country TEXT,
    location TEXT,
    collection_id TEXT,
    collection_title TEXT,
    page_url TEXT,
    thumbnail_url TEXT,
    preview_url TEXT,
    license_class TEXT,
    provider_metadata_hash TEXT,
    raw_metadata_json TEXT,
    fetched_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now')),
    FOREIGN KEY(asset_id) REFERENCES media_assets(id) ON DELETE CASCADE,
    UNIQUE(provider, external_id)
);

CREATE INDEX IF NOT EXISTS idx_asset_provider_metadata_provider_external
    ON asset_provider_metadata(provider, external_id);
