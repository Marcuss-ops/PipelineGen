-- 024_script_research_sources.sql
-- Tracks which web/youtube/transcript sources were used during script generation.

CREATE TABLE IF NOT EXISTS script_research_sources (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    script_id INTEGER NOT NULL,
    query TEXT NOT NULL DEFAULT '',
    url TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    snippet TEXT NOT NULL DEFAULT '',
    source_type TEXT NOT NULL DEFAULT 'web',
    used_in_sections TEXT NOT NULL DEFAULT '[]',
    relevance_score REAL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (script_id) REFERENCES scripts(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_research_sources_script ON script_research_sources(script_id);
