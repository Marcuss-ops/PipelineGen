-- 005_add_performance_indexes.sql
-- Additional performance indexes for velox.db.sqlite tables

-- video_metadata additional indexes
CREATE INDEX IF NOT EXISTS idx_video_metadata_duration
ON video_metadata(duration)
WHERE duration > 0;

CREATE INDEX IF NOT EXISTS idx_video_metadata_resolution
ON video_metadata(width, height)
WHERE width > 0 AND height > 0;

CREATE INDEX IF NOT EXISTS idx_video_metadata_codec
ON video_metadata(codec)
WHERE codec != '';

CREATE INDEX IF NOT EXISTS idx_video_metadata_updated_at
ON video_metadata(updated_at DESC);

-- video_stats_history additional indexes
CREATE INDEX IF NOT EXISTS idx_video_stats_value
ON video_stats_history(stat_type, value);

-- harvester_jobs additional indexes
CREATE INDEX IF NOT EXISTS idx_harvester_jobs_duration
ON harvester_jobs(duration)
WHERE duration > 0;

CREATE INDEX IF NOT EXISTS idx_harvester_jobs_view_count
ON harvester_jobs(view_count DESC)
WHERE view_count > 0;

CREATE INDEX IF NOT EXISTS idx_harvester_jobs_published
ON harvester_jobs(published_at DESC)
WHERE published_at IS NOT NULL;

-- monitored_sources additional indexes
CREATE INDEX IF NOT EXISTS idx_monitored_sources_keyword
ON monitored_sources(keyword)
WHERE keyword != '';

CREATE INDEX IF NOT EXISTS idx_monitored_sources_updated_at
ON monitored_sources(updated_at DESC);

-- artlist_runs additional indexes
CREATE INDEX IF NOT EXISTS idx_artlist_runs_counts
ON artlist_runs(status, processed DESC, failed DESC);

CREATE INDEX IF NOT EXISTS idx_artlist_runs_updated_at
ON artlist_runs(updated_at DESC);

-- scripts additional indexes
CREATE INDEX IF NOT EXISTS idx_scripts_duration
ON scripts(duration)
WHERE duration > 0;

CREATE INDEX IF NOT EXISTS idx_scripts_updated_at
ON scripts(updated_at DESC);

-- script_sections additional indexes
CREATE INDEX IF NOT EXISTS idx_script_sections_type
ON script_sections(section_type);

-- script_stock_matches additional indexes
CREATE INDEX IF NOT EXISTS idx_script_stock_matches_score
ON script_stock_matches(score DESC);

CREATE INDEX IF NOT EXISTS idx_script_stock_matches_source
ON script_stock_matches(stock_source)
WHERE stock_source != '';
