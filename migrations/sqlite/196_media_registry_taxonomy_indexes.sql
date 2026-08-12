-- database: primary
-- Stable lookup indexes for the canonical media taxonomy.

CREATE INDEX IF NOT EXISTS idx_media_assets_namespace_kind
    ON media_assets(namespace, asset_kind, source_type);
