-- 002_add_image_indexes.sql
-- Additional performance indexes for images tables

-- subjects additional indexes
CREATE INDEX IF NOT EXISTS idx_subjects_updated_at
ON subjects(updated_at DESC);

-- images additional indexes
CREATE INDEX IF NOT EXISTS idx_images_resolution
ON images(width, height)
WHERE width > 0 AND height > 0;

CREATE INDEX IF NOT EXISTS idx_images_size
ON images(file_size_bytes DESC)
WHERE file_size_bytes > 0;

CREATE INDEX IF NOT EXISTS idx_images_status
ON images(status) WHERE status != '';

CREATE INDEX IF NOT EXISTS idx_images_updated_at
ON images(updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_images_mime_type
ON images(mime_type)
WHERE mime_type != '';

-- Unique index on file_hash for images
CREATE UNIQUE INDEX IF NOT EXISTS ux_images_file_hash
ON images(file_hash)
WHERE file_hash IS NOT NULL AND file_hash != '';
