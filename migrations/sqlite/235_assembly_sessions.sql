CREATE TABLE IF NOT EXISTS assembly_sessions (
    assembly_id TEXT PRIMARY KEY,
    parent_job_id TEXT NOT NULL,
    preparation_job_id TEXT NOT NULL DEFAULT '',
    preparation_id TEXT NOT NULL DEFAULT '',
    preparation_hash TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    revision INTEGER NOT NULL DEFAULT 1,
    runtime_assets_json TEXT NOT NULL DEFAULT '[]',
    finalize_plan_json TEXT NOT NULL DEFAULT '',
    project TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_assembly_sessions_parent_job ON assembly_sessions(parent_job_id);
