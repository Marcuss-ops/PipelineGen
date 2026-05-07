-- clips_002_add_performance_indexes.sql
-- Additional performance indexes for clips tables

-- Indexes for clips table
CREATE INDEX IF NOT EXISTS idx_clips_status ON clips(status);
CREATE INDEX IF NOT EXISTS idx_clips_updated_at ON clips(updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_clips_drive_link ON clips(drive_link) WHERE drive_link != '';
CREATE INDEX IF NOT EXISTS idx_clips_duration ON clips(duration);

-- Unique index on file_hash for deduplication (where not empty)
CREATE UNIQUE INDEX IF NOT EXISTS ux_clips_file_hash
ON clips(file_hash)
WHERE file_hash IS NOT NULL AND file_hash != '';

-- Indexes for clip_folders table
CREATE INDEX IF NOT EXISTS idx_clip_folders_status ON clip_folders(status) WHERE status != '';
CREATE INDEX IF NOT EXISTS idx_clip_folders_updated_at ON clip_folders(updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_clip_folders_clip_count ON clip_folders(clip_count);
