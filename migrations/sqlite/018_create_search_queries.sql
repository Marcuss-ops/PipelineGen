-- 018_create_search_queries.sql
-- Scheduled YouTube topic searches
-- Each row is a recurring query (e.g. "Floyd Mayweather interview")
-- that the Channel Monitor runs periodically to find new videos.
-- Results are stored in search_query_results for dedup.

CREATE TABLE IF NOT EXISTS search_queries (
    id TEXT PRIMARY KEY,
    query TEXT NOT NULL,                     -- "Floyd Mayweather interview"
    category TEXT NOT NULL,                  -- "boxe" (Drive folder di destinazione)
    drive_folder_id TEXT DEFAULT '',         -- Specific Drive folder ID
    min_score INTEGER DEFAULT 60,            -- min similarity score da SearchByTopic
    max_results INTEGER DEFAULT 5,           -- quanti video processare per ciclo
    check_interval TEXT DEFAULT '7d',        -- ogni quanto cercare (e.g. "1h", "24h", "7d")
    last_run_at TEXT,                        -- ultima esecuzione (datetime)
    last_video_published_at TEXT,            -- publishedAfter per YouTube API delta
    is_active INTEGER DEFAULT 1,             -- 0 = sospeso senza cancellare
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS search_query_results (
    query_id TEXT NOT NULL,
    video_id TEXT NOT NULL,                  -- YouTube video ID
    video_title TEXT NOT NULL DEFAULT '',
    channel_name TEXT DEFAULT '',
    published_at TEXT,                       -- data di pubblicazione originale
    processed_at TEXT DEFAULT (datetime('now')),
    score INTEGER DEFAULT 0,                 -- similarity score
    PRIMARY KEY (query_id, video_id)
);

CREATE INDEX IF NOT EXISTS idx_search_queries_active ON search_queries(is_active);
CREATE INDEX IF NOT EXISTS idx_search_queries_interval ON search_queries(check_interval);
CREATE INDEX IF NOT EXISTS idx_search_query_results_video ON search_query_results(video_id);
