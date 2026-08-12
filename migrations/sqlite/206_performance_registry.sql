-- database: primary
-- Migration 206: canonical performance registry.
-- Performance is durable run history; Prometheus remains a derived view.

CREATE TABLE IF NOT EXISTS performance_runs (
    run_id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL DEFAULT '',
    root_job_id TEXT NOT NULL DEFAULT '',
    video_id TEXT NOT NULL DEFAULT '',
    workload_id TEXT NOT NULL DEFAULT '',
    workload_version TEXT NOT NULL DEFAULT '',
    git_sha TEXT NOT NULL DEFAULT '',
    worker_id TEXT NOT NULL DEFAULT '',
    host_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('RUNNING','SUCCEEDED','FAILED')),
    wall_ms INTEGER NOT NULL DEFAULT 0,
    cpu_user_ms INTEGER NOT NULL DEFAULT 0,
    cpu_system_ms INTEGER NOT NULL DEFAULT 0,
    peak_rss_bytes INTEGER NOT NULL DEFAULT 0,
    disk_read_bytes INTEGER NOT NULL DEFAULT 0,
    disk_write_bytes INTEGER NOT NULL DEFAULT 0,
    network_rx_bytes INTEGER NOT NULL DEFAULT 0,
    network_tx_bytes INTEGER NOT NULL DEFAULT 0,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    started_at TEXT NOT NULL,
    completed_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_performance_runs_job ON performance_runs(job_id, started_at);
CREATE INDEX IF NOT EXISTS idx_performance_runs_workload ON performance_runs(workload_id, workload_version, started_at);

CREATE TABLE IF NOT EXISTS performance_steps (
    step_id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    job_id TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('RUNNING','SUCCEEDED','FAILED')),
    duration_ms INTEGER NOT NULL DEFAULT 0,
    input_count INTEGER NOT NULL DEFAULT 0,
    output_count INTEGER NOT NULL DEFAULT 0,
    input_bytes INTEGER NOT NULL DEFAULT 0,
    output_bytes INTEGER NOT NULL DEFAULT 0,
    cache_hits INTEGER NOT NULL DEFAULT 0,
    cache_misses INTEGER NOT NULL DEFAULT 0,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    started_at TEXT NOT NULL,
    completed_at TEXT,
    FOREIGN KEY(run_id) REFERENCES performance_runs(run_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_performance_steps_run ON performance_steps(run_id, started_at);

CREATE TABLE IF NOT EXISTS performance_artifacts (
    artifact_id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    sha256 TEXT NOT NULL,
    size_bytes INTEGER NOT NULL DEFAULT 0,
    uri TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    FOREIGN KEY(run_id) REFERENCES performance_runs(run_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS benchmark_workloads (
    workload_id TEXT NOT NULL,
    version TEXT NOT NULL,
    input_manifest_sha256 TEXT NOT NULL,
    parameters_json TEXT NOT NULL DEFAULT '{}',
    expected_output_sha256 TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    PRIMARY KEY(workload_id, version)
);
