-- 0001_initial_orchestration.sql
-- Initial durable schema for moving away from queue.json and JSON-first orchestration.
-- SQLite-compatible version

CREATE TABLE IF NOT EXISTS jobs (
    id TEXT PRIMARY KEY,
    project_id TEXT,
    type TEXT NOT NULL,
    status TEXT NOT NULL,
    worker_id TEXT,
    payload TEXT,
    result TEXT,
    error TEXT,
    priority INTEGER NOT NULL DEFAULT 0,
    retries INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    started_at TEXT,
    completed_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_jobs_status_priority_created
    ON jobs (status, priority DESC, created_at ASC);

CREATE INDEX IF NOT EXISTS idx_jobs_worker_id
    ON jobs (worker_id);

CREATE TABLE IF NOT EXISTS job_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL,
    level TEXT,
    message TEXT,
    payload TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_job_events_job_id_created
    ON job_events (job_id, created_at DESC);

CREATE TABLE IF NOT EXISTS workers (
    id TEXT PRIMARY KEY,
    status TEXT NOT NULL,
    hostname TEXT,
    ip_address TEXT,
    capabilities TEXT,
    metadata TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    last_heartbeat_at TEXT
);

CREATE TABLE IF NOT EXISTS worker_heartbeats (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    worker_id TEXT NOT NULL REFERENCES workers(id) ON DELETE CASCADE,
    status TEXT,
    payload TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_worker_heartbeats_worker_id_created
    ON worker_heartbeats (worker_id, created_at DESC);
