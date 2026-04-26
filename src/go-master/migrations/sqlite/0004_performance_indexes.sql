-- 0004_performance_indexes.sql
-- Additional indexes for improving query performance

-- Clips table indexes
CREATE INDEX IF NOT EXISTS idx_clips_source ON clips (source);
CREATE INDEX IF NOT EXISTS idx_clips_folder_id ON clips (folder_id);
CREATE INDEX IF NOT EXISTS idx_clips_created_at_desc ON clips (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_clips_updated_at_desc ON clips (updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_clips_media_type_source ON clips (media_type, source);

-- Scripts table additional indexes
CREATE INDEX IF NOT EXISTS idx_scripts_deleted_created ON scripts (is_deleted, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_scripts_topic_language ON scripts (topic, language);

-- Jobs table additional indexes for queue processing
CREATE INDEX IF NOT EXISTS idx_jobs_status_created ON jobs (status, created_at ASC);
CREATE INDEX IF NOT EXISTS idx_jobs_type_status ON jobs (type, status);

-- Video metadata additional indexes
CREATE INDEX IF NOT EXISTS idx_video_metadata_status_published ON video_metadata (status, published_at DESC);
CREATE INDEX IF NOT EXISTS idx_video_metadata_channel_status ON video_metadata (channel_id, status);

-- Composite indexes for common query patterns
CREATE INDEX IF NOT EXISTS idx_clips_source_created ON clips (source, created_at DESC);
