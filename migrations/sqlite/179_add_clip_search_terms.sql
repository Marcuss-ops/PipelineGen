CREATE TABLE IF NOT EXISTS clip_search_terms (
    clip_id TEXT NOT NULL,
    term    TEXT NOT NULL,
    source  TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (clip_id, term)
);
