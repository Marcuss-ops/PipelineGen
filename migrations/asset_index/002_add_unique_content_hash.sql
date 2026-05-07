-- 002_add_unique_content_hash.sql
-- Add unique index on content_hash for deduplication

-- Unique index on content_hash (where not null/empty) for deduplication
CREATE UNIQUE INDEX IF NOT EXISTS ux_asset_index_content_hash
ON asset_index(content_hash)
WHERE content_hash IS NOT NULL AND content_hash != '';

-- Additional performance indexes
CREATE INDEX IF NOT EXISTS idx_asset_index_asset_type
ON asset_index(asset_type);

CREATE INDEX IF NOT EXISTS idx_asset_index_updated_at
ON asset_index(updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_asset_index_local_path
ON asset_index(local_path)
WHERE local_path IS NOT NULL AND local_path != '';
