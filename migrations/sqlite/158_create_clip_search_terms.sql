-- database: primary
-- Migration 158: Create clip_search_terms table for fast-path term queries.

CREATE TABLE IF NOT EXISTS clip_search_terms (
    clip_id TEXT NOT NULL,
    term    TEXT NOT NULL,
    source  TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (clip_id, term),
    FOREIGN KEY (clip_id) REFERENCES media_assets(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_clip_search_terms_term ON clip_search_terms(term);
CREATE INDEX IF NOT EXISTS idx_clip_search_terms_source ON clip_search_terms(source);
