CREATE TABLE IF NOT EXISTS artlist_runs (
    run_id TEXT PRIMARY KEY,
    term TEXT NOT NULL,
    root_folder_id TEXT NOT NULL,
    strategy TEXT NOT NULL DEFAULT 'skip',
    dry_run INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'queued',
    active_key TEXT NOT NULL DEFAULT '',
    found INTEGER NOT NULL DEFAULT 0,
    processed INTEGER NOT NULL DEFAULT 0,
    skipped INTEGER NOT NULL DEFAULT 0,
    failed INTEGER NOT NULL DEFAULT 0,
    estimated_size INTEGER NOT NULL DEFAULT 0,
    last_processed_at TEXT,
    request_json TEXT NOT NULL DEFAULT '{}',
    error TEXT NOT NULL DEFAULT '',
    tag_folder_id TEXT DEFAULT '',
    started_at TEXT NOT NULL DEFAULT (datetime('now')),
    ended_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_artlist_runs_term_status ON artlist_runs (term, status);
CREATE INDEX IF NOT EXISTS idx_artlist_runs_started_at ON artlist_runs (started_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_artlist_runs_active_key ON artlist_runs (active_key) WHERE active_key != '';
