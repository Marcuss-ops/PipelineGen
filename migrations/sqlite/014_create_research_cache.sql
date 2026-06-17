-- 014_create_research_cache.sql
-- Cache for autonomous research agent output (agent_script_writer.py)

CREATE TABLE IF NOT EXISTS research_cache (
    key TEXT PRIMARY KEY,
    topic TEXT NOT NULL,
    language TEXT NOT NULL,
    max_steps INTEGER NOT NULL,
    source_text TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    last_used TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_research_cache_topic ON research_cache(topic);
CREATE INDEX IF NOT EXISTS idx_research_cache_last_used ON research_cache(last_used);
