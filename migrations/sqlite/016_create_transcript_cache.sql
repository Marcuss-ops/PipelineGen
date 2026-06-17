-- 016_create_transcript_cache.sql
-- Cache for video transcripts to avoid re-downloading VTT files
-- during repeated semantic matching cycles.
-- TTL is managed by the application (default 7 days).

CREATE TABLE IF NOT EXISTS transcript_cache (
    video_id TEXT PRIMARY KEY,           -- YouTube video ID
    transcript_text TEXT NOT NULL,        -- Cleaned transcript text
    language TEXT NOT NULL DEFAULT 'en',  -- Subtitle language
    cached_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_transcript_cache_cached_at ON transcript_cache(cached_at);
