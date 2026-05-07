-- 007_add_asset_index_columns.sql
-- Add processing metadata columns to asset_index table

ALTER TABLE asset_index ADD COLUMN processing_stage TEXT;
ALTER TABLE asset_index ADD COLUMN error_message TEXT;
ALTER TABLE asset_index ADD COLUMN retry_count INTEGER DEFAULT 0;
ALTER TABLE asset_index ADD COLUMN last_attempt_at TEXT;
ALTER TABLE asset_index ADD COLUMN processed_at TEXT;

CREATE INDEX IF NOT EXISTS idx_asset_index_processing_stage
ON asset_index(processing_stage)
WHERE processing_stage IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_asset_index_retry_count
ON asset_index(retry_count DESC)
WHERE retry_count > 0;

CREATE INDEX IF NOT EXISTS idx_asset_index_processed_at
ON asset_index(processed_at DESC)
WHERE processed_at IS NOT NULL;
