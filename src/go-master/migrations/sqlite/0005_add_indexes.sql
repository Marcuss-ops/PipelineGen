-- Add indexes for commonly queried columns to improve performance

-- Index for media_type column (used in WHERE clauses)
CREATE INDEX IF NOT EXISTS idx_clips_media_type ON clips(media_type);

-- Index for group_name column (used in WHERE clauses)
CREATE INDEX IF NOT EXISTS idx_clips_group_name ON clips(group_name);

-- Index for source column (may be used in filtering)
CREATE INDEX IF NOT EXISTS idx_clips_source ON clips(source);

-- Composite index for media_type + group_name (common query pattern)
CREATE INDEX IF NOT EXISTS idx_clips_media_group ON clips(media_type, group_name);
