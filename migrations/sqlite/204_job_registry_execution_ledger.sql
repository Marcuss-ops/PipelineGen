-- database: primary
-- Migration 204: durable Job Registry execution ledger.
--
-- The existing jobs table remains the canonical queue/job state. These
-- tables are the append/measure surfaces used by worker projections and
-- preserve the complete payload, runtime steps, metrics, events, and
-- job-to-asset lineage without changing the queue contract.

-- Build/runtime identity is optional on historical jobs but is persisted
-- for every new lifecycle projection when present in the payload.
ALTER TABLE jobs ADD COLUMN git_sha TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN app_version TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS job_steps (
    step_id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL,
    step_name TEXT NOT NULL,
    step_type TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    started_at TEXT,
    completed_at TEXT,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    input_count INTEGER NOT NULL DEFAULT 0,
    output_count INTEGER NOT NULL DEFAULT 0,
    input_bytes INTEGER NOT NULL DEFAULT 0,
    output_bytes INTEGER NOT NULL DEFAULT 0,
    metrics_json TEXT NOT NULL DEFAULT '{}',
    error_code TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_job_steps_job_created ON job_steps(job_id, created_at);
CREATE INDEX IF NOT EXISTS idx_job_steps_name_status ON job_steps(step_name, status, started_at);

CREATE TABLE IF NOT EXISTS job_registry_metrics (
    metric_id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL,
    step_id TEXT,
    metric_name TEXT NOT NULL,
    metric_value REAL NOT NULL,
    unit TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE,
    FOREIGN KEY (step_id) REFERENCES job_steps(step_id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_job_registry_metrics_job ON job_registry_metrics(job_id, created_at);
CREATE INDEX IF NOT EXISTS idx_job_registry_metrics_name ON job_registry_metrics(metric_name, created_at);

CREATE TABLE IF NOT EXISTS job_registry_events (
    event_id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    payload_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL,
    FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_job_registry_events_job_created ON job_registry_events(job_id, created_at);
CREATE INDEX IF NOT EXISTS idx_job_registry_events_type_created ON job_registry_events(event_type, created_at);

CREATE TABLE IF NOT EXISTS job_asset_relations (
    job_id TEXT NOT NULL,
    asset_id TEXT NOT NULL,
    relation TEXT NOT NULL,
    ordinal INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    PRIMARY KEY (job_id, asset_id, relation),
    FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_job_asset_relations_asset ON job_asset_relations(asset_id, relation, created_at);
CREATE INDEX IF NOT EXISTS idx_job_asset_relations_job_relation ON job_asset_relations(job_id, relation, ordinal);
