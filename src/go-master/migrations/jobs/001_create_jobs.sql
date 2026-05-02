CREATE TABLE IF NOT EXISTS jobs (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    status TEXT NOT NULL,
    priority INTEGER NOT NULL DEFAULT 0,

    project TEXT DEFAULT '',
    video_name TEXT DEFAULT '',

    active_key TEXT DEFAULT '',
    payload_json TEXT NOT NULL DEFAULT '{}',
    result_json TEXT NOT NULL DEFAULT '{}',

    progress INTEGER NOT NULL DEFAULT 0,
    error TEXT DEFAULT '',

    retry_count INTEGER NOT NULL DEFAULT 0,
    max_retries INTEGER NOT NULL DEFAULT 3,

    worker_id TEXT DEFAULT '',
    lease_expiry TEXT,

    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    started_at TEXT,
    completed_at TEXT,
    cancelled_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_jobs_status_priority
ON jobs(status, priority DESC, created_at ASC);

CREATE INDEX IF NOT EXISTS idx_jobs_type_status
ON jobs(type, status);

CREATE INDEX IF NOT EXISTS idx_jobs_project
ON jobs(project);

CREATE INDEX IF NOT EXISTS idx_jobs_lease
ON jobs(lease_expiry);

CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_active_key
ON jobs(active_key)
WHERE active_key != '';

CREATE TABLE IF NOT EXISTS job_events (
    id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL,
    type TEXT NOT NULL,
    message TEXT NOT NULL DEFAULT '',
    data_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL,
    FOREIGN KEY(job_id) REFERENCES jobs(id)
);

CREATE INDEX IF NOT EXISTS idx_job_events_job_id
ON job_events(job_id, created_at ASC);
