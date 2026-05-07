-- clips_004_add_metadata_columns.sql
-- Add detailed metadata columns to clips table for better querying

ALTER TABLE clips ADD COLUMN duration_seconds REAL;
ALTER TABLE clips ADD COLUMN width INTEGER DEFAULT 0;
ALTER TABLE clips ADD COLUMN height INTEGER DEFAULT 0;
ALTER TABLE clips ADD COLUMN fps REAL DEFAULT 0;
ALTER TABLE clips ADD COLUMN codec TEXT DEFAULT '';
ALTER TABLE clips ADD COLUMN size_bytes INTEGER DEFAULT 0;
ALTER TABLE clips ADD COLUMN processing_stage TEXT;
ALTER TABLE clips ADD COLUMN error_message TEXT;
ALTER TABLE clips ADD COLUMN retry_count INTEGER DEFAULT 0;
ALTER TABLE clips ADD COLUMN last_attempt_at TEXT;
ALTER TABLE clips ADD COLUMN processed_at TEXT;

-- Indexes for new columns
CREATE INDEX IF NOT EXISTS idx_clips_resolution
ON clips(width, height)
WHERE width > 0 AND height > 0;

CREATE INDEX IF NOT EXISTS idx_clips_size
ON clips(size_bytes DESC)
WHERE size_bytes > 0;

CREATE INDEX IF NOT EXISTS idx_clips_fps
ON clips(fps)
WHERE fps > 0;

CREATE INDEX IF NOT EXISTS idx_clips_processing_stage
ON clips(processing_stage)
WHERE processing_stage IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_clips_retry_count
ON clips(retry_count DESC)
WHERE retry_count > 0;

CREATE INDEX IF NOT EXISTS idx_clips_processed_at
ON clips(processed_at DESC)
WHERE processed_at IS NOT NULL;

-- Extract metadata from JSON and populate new columns (run once after migration)
-- UPDATE clips SET
--     duration_seconds = CAST(json_extract(metadata, '$.durationSeconds') AS REAL),
--     width = CAST(json_extract(metadata, '$.width') AS INTEGER),
--     height = CAST(json_extract(metadata, '$.height') AS INTEGER),
--     fps = CAST(json_extract(metadata, '$.fps') AS REAL),
--     codec = json_extract(metadata, '$.codec'),
--     size_bytes = CAST(json_extract(metadata, '$.sizeBytes') AS INTEGER)
-- WHERE metadata IS NOT NULL AND metadata != '{}' AND duration_seconds IS NULL;
