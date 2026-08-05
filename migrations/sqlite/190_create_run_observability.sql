-- database: observability
CREATE TABLE IF NOT EXISTS job_attempts (
 attempt_id TEXT PRIMARY KEY,
 job_id TEXT NOT NULL,
 run_id TEXT NOT NULL UNIQUE,
 attempt_number INTEGER NOT NULL DEFAULT 1,
 worker_id TEXT,
 lease_id TEXT,
 status TEXT NOT NULL,
 available_at TEXT,
 started_at TEXT,
 finished_at TEXT,
 lease_expires_at TEXT,
 error_code TEXT,
 error TEXT,
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_job_attempts_job ON job_attempts(job_id, attempt_number);
CREATE INDEX IF NOT EXISTS idx_job_attempts_recovery ON job_attempts(status, lease_expires_at);

CREATE TABLE IF NOT EXISTS run_observability (
 run_id TEXT PRIMARY KEY,
 job_id TEXT NOT NULL,
 job_type TEXT NOT NULL DEFAULT '',
 attempt_id TEXT NOT NULL UNIQUE,
 parent_run_id TEXT,
 worker_id TEXT,
 lease_id TEXT,
 lease_expires_at TEXT,
 status TEXT NOT NULL CHECK(status IN ('RUNNING','SUCCEEDED','FAILED','CANCELLED','ABANDONED')),
 created_at TEXT NOT NULL,
 started_at TEXT NOT NULL,
 finished_at TEXT,
 queue_wait_ms INTEGER NOT NULL DEFAULT 0,
 wall_time_ms INTEGER NOT NULL DEFAULT 0,
 active_ms INTEGER NOT NULL DEFAULT 0,
 blocked_ms INTEGER NOT NULL DEFAULT 0,
 accumulated_operation_ms INTEGER NOT NULL DEFAULT 0,
 error_code TEXT,
 error TEXT,
 counters_json TEXT NOT NULL DEFAULT '{}',
 children_json TEXT,
 report_json TEXT NOT NULL DEFAULT '{}',
 observability_degraded INTEGER NOT NULL DEFAULT 0,
 updated_at TEXT NOT NULL,
 FOREIGN KEY(attempt_id) REFERENCES job_attempts(attempt_id)
);
CREATE INDEX IF NOT EXISTS idx_run_observability_job ON run_observability(job_id, created_at);
CREATE INDEX IF NOT EXISTS idx_run_observability_recovery ON run_observability(status, lease_expires_at);

CREATE TABLE IF NOT EXISTS run_stage_observations (
 observation_id TEXT PRIMARY KEY,
 run_id TEXT NOT NULL,
 name TEXT NOT NULL,
 status TEXT NOT NULL,
 duration_ms INTEGER NOT NULL DEFAULT 0,
 attempts INTEGER NOT NULL DEFAULT 0,
 cache_status TEXT,
 error_code TEXT,
 items_input INTEGER NOT NULL DEFAULT 0,
 items_completed INTEGER NOT NULL DEFAULT 0,
 items_failed INTEGER NOT NULL DEFAULT 0,
 bytes_processed INTEGER NOT NULL DEFAULT 0,
 FOREIGN KEY(run_id) REFERENCES run_observability(run_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_run_stage_observations_run ON run_stage_observations(run_id);

CREATE TABLE IF NOT EXISTS run_operation_observations (
 observation_id TEXT PRIMARY KEY,
 run_id TEXT NOT NULL,
 stage TEXT NOT NULL,
 component TEXT NOT NULL,
 operation TEXT NOT NULL,
 provider TEXT,
 status TEXT NOT NULL,
 duration_ms INTEGER NOT NULL DEFAULT 0,
 attempts INTEGER NOT NULL DEFAULT 0,
 items INTEGER NOT NULL DEFAULT 0,
 bytes INTEGER NOT NULL DEFAULT 0,
 cache_status TEXT,
 error_code TEXT,
 FOREIGN KEY(run_id) REFERENCES run_observability(run_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_run_operation_observations_run ON run_operation_observations(run_id);

CREATE TABLE IF NOT EXISTS run_artifact_observations (
 observation_id TEXT PRIMARY KEY,
 run_id TEXT NOT NULL,
 kind TEXT NOT NULL,
 ref TEXT,
 url TEXT,
 stage TEXT,
 bytes INTEGER NOT NULL DEFAULT 0,
 reused INTEGER NOT NULL DEFAULT 0,
 FOREIGN KEY(run_id) REFERENCES run_observability(run_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_run_artifact_observations_run ON run_artifact_observations(run_id);

CREATE TABLE IF NOT EXISTS run_child_observations (
 parent_run_id TEXT NOT NULL,
 child_job_id TEXT NOT NULL,
 child_run_id TEXT NOT NULL,
 status TEXT NOT NULL,
 wall_time_ms INTEGER NOT NULL DEFAULT 0,
 updated_at TEXT NOT NULL,
 PRIMARY KEY(parent_run_id, child_job_id),
 FOREIGN KEY(parent_run_id) REFERENCES run_observability(run_id) ON DELETE CASCADE
);
