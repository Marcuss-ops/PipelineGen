CREATE TABLE IF NOT EXISTS jobs_new (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'created',
    payload_json TEXT NOT NULL DEFAULT '{}',
    result_json TEXT NOT NULL DEFAULT '{}',
    error TEXT DEFAULT '',
    attempts INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 3,
    created_at TEXT NOT NULL,
    started_at TEXT,
    finished_at TEXT,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_jobs_new_status ON jobs_new(status);
CREATE INDEX IF NOT EXISTS idx_jobs_new_type ON jobs_new(type);
CREATE INDEX IF NOT EXISTS idx_jobs_new_created_at ON jobs_new(created_at DESC);
