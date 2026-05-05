-- 004_create_artlist_runs.sql
-- Artlist runs table for tracking Artlist pipeline runs

CREATE TABLE IF NOT EXISTS artlist_runs (
    id TEXT PRIMARY KEY,
    term TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    requested INTEGER DEFAULT 0,
    found INTEGER DEFAULT 0,
    processed INTEGER DEFAULT 0,
    skipped INTEGER DEFAULT 0,
    failed INTEGER DEFAULT 0,
    tag_folder_id TEXT DEFAULT '',
    strategy TEXT DEFAULT '',
    error TEXT DEFAULT '',
    started_at TEXT,
    ended_at TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_artlist_runs_term ON artlist_runs(term);
CREATE INDEX IF NOT EXISTS idx_artlist_runs_status ON artlist_runs(status);
CREATE INDEX IF NOT EXISTS idx_artlist_runs_created ON artlist_runs(created_at DESC);
