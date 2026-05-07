-- 002_add_media_indexes.sql
-- Additional performance indexes for media tables

-- media_items additional indexes
CREATE INDEX IF NOT EXISTS idx_media_items_type_status
ON media_items(media_type, status);

CREATE INDEX IF NOT EXISTS idx_media_items_updated_at
ON media_items(updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_media_items_duration
ON media_items(duration_secs)
WHERE duration_secs > 0;

CREATE INDEX IF NOT EXISTS idx_media_items_external
ON media_items(external_id)
WHERE external_id IS NOT NULL;

-- media_files additional indexes
CREATE INDEX IF NOT EXISTS idx_media_files_type_status
ON media_files(location_kind, status);

CREATE INDEX IF NOT EXISTS idx_media_files_resolution
ON media_files(width, height)
WHERE width > 0 AND height > 0;

CREATE INDEX IF NOT EXISTS idx_media_files_duration
ON media_files(duration_secs)
WHERE duration_secs > 0;

CREATE INDEX IF NOT EXISTS idx_media_files_size
ON media_files(file_size_bytes DESC)
WHERE file_size_bytes > 0;

CREATE INDEX IF NOT EXISTS idx_media_files_updated_at
ON media_files(updated_at DESC);

-- Unique index on file_hash for media_files
CREATE UNIQUE INDEX IF NOT EXISTS ux_media_files_file_hash
ON media_files(file_hash)
WHERE file_hash IS NOT NULL AND file_hash != '';
