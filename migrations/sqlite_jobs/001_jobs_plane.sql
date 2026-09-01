-- Jobs Plane schema. This file is intentionally independent from
-- migrations/sqlite: a jobs database must not receive media-plane tables.
-- The IDs that point to media assets are logical references, never foreign
-- keys into media.db.sqlite.

CREATE TABLE IF NOT EXISTS jobs (
    id TEXT PRIMARY KEY, type TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'QUEUED',
    priority INTEGER NOT NULL DEFAULT 0, project TEXT NOT NULL DEFAULT '',
    video_name TEXT NOT NULL DEFAULT '', active_key TEXT NOT NULL DEFAULT '',
    -- Large request/result JSON lives in job_payloads/job_results. These
    -- columns are intentionally absent from the execution hot row.
    error TEXT NOT NULL DEFAULT '', worker_id TEXT NOT NULL DEFAULT '',
    lease_id TEXT NOT NULL DEFAULT '', lease_expiry TEXT,
    retry_count INTEGER NOT NULL DEFAULT 0, max_retries INTEGER NOT NULL DEFAULT 3,
    progress INTEGER NOT NULL DEFAULT 0, started_at TEXT, completed_at TEXT,
    cancelled_at TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
    revision INTEGER NOT NULL DEFAULT 1, correlation_id TEXT NOT NULL DEFAULT '',
    parent_job_id TEXT NOT NULL DEFAULT '', root_job_id TEXT NOT NULL DEFAULT '',
    client_id TEXT NOT NULL DEFAULT '', idempotency_key TEXT NOT NULL DEFAULT '',
    parent_state_typed TEXT NOT NULL DEFAULT '', git_sha TEXT NOT NULL DEFAULT '',
    app_version TEXT NOT NULL DEFAULT '', project_id TEXT NOT NULL DEFAULT '',
    video_id TEXT NOT NULL DEFAULT '', host TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0, payload_hash TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_jobs_claim ON jobs(status, priority DESC, created_at);
CREATE INDEX IF NOT EXISTS idx_jobs_type_status ON jobs(type, status);
CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_active_key ON jobs(active_key) WHERE active_key <> '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_client_idempotency ON jobs(client_id, idempotency_key) WHERE client_id <> '' AND idempotency_key <> '';

CREATE TABLE IF NOT EXISTS job_payloads (
    job_id TEXT PRIMARY KEY, codec_id TEXT NOT NULL DEFAULT 'json',
    payload TEXT NOT NULL DEFAULT '{}', payload_hash TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS job_events (
    id TEXT PRIMARY KEY, job_id TEXT NOT NULL, type TEXT NOT NULL,
    message TEXT NOT NULL DEFAULT '', data_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL,
    FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_job_events_job ON job_events(job_id, created_at);

CREATE TABLE IF NOT EXISTS job_results (
    id INTEGER PRIMARY KEY AUTOINCREMENT, job_id TEXT NOT NULL,
    attempt INTEGER NOT NULL DEFAULT 0, result_hash TEXT NOT NULL DEFAULT '',
    codec_id TEXT NOT NULL DEFAULT '', result_payload TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL,
    FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS uniq_job_results_dedup ON job_results(job_id, attempt, result_hash);
CREATE INDEX IF NOT EXISTS idx_job_results_job ON job_results(job_id, attempt DESC);

CREATE TABLE IF NOT EXISTS dead_letter_jobs (
    id INTEGER PRIMARY KEY AUTOINCREMENT, job_id TEXT NOT NULL,
    job_type TEXT NOT NULL, correlation_id TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL, payload_json TEXT, retry_count INTEGER NOT NULL DEFAULT 0,
    failed_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_dead_letter_job ON dead_letter_jobs(job_id, failed_at);

CREATE TABLE IF NOT EXISTS job_steps (
    step_id TEXT PRIMARY KEY, job_id TEXT NOT NULL, step_name TEXT NOT NULL,
    step_type TEXT NOT NULL DEFAULT '', status TEXT NOT NULL, started_at TEXT,
    completed_at TEXT, duration_ms INTEGER NOT NULL DEFAULT 0,
    input_count INTEGER NOT NULL DEFAULT 0, output_count INTEGER NOT NULL DEFAULT 0,
    input_bytes INTEGER NOT NULL DEFAULT 0, output_bytes INTEGER NOT NULL DEFAULT 0,
    metrics_json TEXT NOT NULL DEFAULT '{}', error_code TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL,
    FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_job_steps_job ON job_steps(job_id, created_at);
CREATE TABLE IF NOT EXISTS job_registry_metrics (
    metric_id TEXT PRIMARY KEY, job_id TEXT NOT NULL, step_id TEXT,
    metric_name TEXT NOT NULL, metric_value REAL NOT NULL, unit TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS job_registry_events (
    event_id TEXT PRIMARY KEY, job_id TEXT NOT NULL, event_type TEXT NOT NULL,
    payload_json TEXT NOT NULL DEFAULT '{}', created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_job_registry_events_job ON job_registry_events(job_id, created_at);
CREATE TABLE IF NOT EXISTS job_asset_relations (
    job_id TEXT NOT NULL, asset_id TEXT NOT NULL, relation TEXT NOT NULL,
    ordinal INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL,
    PRIMARY KEY(job_id, asset_id, relation)
);

CREATE TABLE IF NOT EXISTS job_checkpoints (
    job_id TEXT NOT NULL, stage TEXT NOT NULL, unit_id TEXT NOT NULL,
    input_fingerprint TEXT NOT NULL, status TEXT NOT NULL,
    artifact_sha256 TEXT NOT NULL DEFAULT '', artifact_uri TEXT NOT NULL DEFAULT '',
    processor_version TEXT NOT NULL, completed_at TEXT NOT NULL,
    PRIMARY KEY(job_id, stage, unit_id)
);

CREATE TABLE IF NOT EXISTS artifact_stages (
    id TEXT PRIMARY KEY, job_id TEXT NOT NULL DEFAULT '', local_path TEXT NOT NULL DEFAULT '',
    hash TEXT NOT NULL DEFAULT '', size INTEGER NOT NULL DEFAULT 0, mime TEXT NOT NULL DEFAULT '',
    requirement TEXT NOT NULL DEFAULT 'optional', destination TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT 'STAGED', attempt_count INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '', published_location TEXT NOT NULL DEFAULT '',
    published_at TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_artifact_stages_job_state ON artifact_stages(job_id, state);

CREATE TABLE IF NOT EXISTS preparation_units (
    unit_fingerprint TEXT PRIMARY KEY, fingerprint TEXT NOT NULL DEFAULT '',
    unit_id TEXT NOT NULL DEFAULT '', job_type TEXT NOT NULL DEFAULT '', unit_kind TEXT NOT NULL,
    fingerprint_version TEXT NOT NULL, processor_version TEXT NOT NULL,
    input_manifest_json TEXT NOT NULL DEFAULT '{}', state TEXT NOT NULL,
    resource_class TEXT NOT NULL, speculation_level INTEGER NOT NULL DEFAULT 0,
    cost_class TEXT NOT NULL DEFAULT 'MEDIUM', reusable INTEGER NOT NULL DEFAULT 1,
    preemptible INTEGER NOT NULL DEFAULT 1, expected_work_ms INTEGER NOT NULL DEFAULT 0,
    actual_work_ms INTEGER NOT NULL DEFAULT 0, result_kind TEXT NOT NULL DEFAULT 'NONE',
    result_ref TEXT NOT NULL DEFAULT '', result_metadata_json TEXT NOT NULL DEFAULT '{}',
    artifact_id TEXT NOT NULL DEFAULT '', cache_key TEXT NOT NULL DEFAULT '',
    result_json TEXT NOT NULL DEFAULT '{}', scheduler_owner TEXT NOT NULL DEFAULT '',
    lease_owner TEXT NOT NULL DEFAULT '', lease_until TEXT, lease_expires_at TEXT,
    attempt_count INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
    started_at TEXT, ready_at TEXT, last_accessed_at TEXT, expires_at TEXT,
    last_error_code TEXT NOT NULL DEFAULT '', last_error_message TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_preparation_units_state ON preparation_units(state, resource_class);
CREATE TABLE IF NOT EXISTS preparation_job_units (
    job_id TEXT NOT NULL, unit_id TEXT NOT NULL, fingerprint TEXT NOT NULL,
    required INTEGER NOT NULL DEFAULT 1, adopted INTEGER NOT NULL DEFAULT 0,
    queue_rank INTEGER, planned_at TEXT NOT NULL, adopted_at TEXT,
    phase TEXT NOT NULL DEFAULT '', scene_id TEXT NOT NULL DEFAULT '', language TEXT NOT NULL DEFAULT '',
    queue_distance INTEGER NOT NULL DEFAULT 0, speculation_ceiling INTEGER NOT NULL DEFAULT 0,
    priority_score REAL NOT NULL DEFAULT 0, critical_path_ms INTEGER NOT NULL DEFAULT 0,
    adoption_state TEXT NOT NULL DEFAULT 'PENDING', promoted_at TEXT, invalidated_at TEXT,
    checkpoint_stage TEXT NOT NULL DEFAULT '', checkpoint_unit_id TEXT NOT NULL DEFAULT '',
    PRIMARY KEY(job_id, unit_id)
);
CREATE TABLE IF NOT EXISTS preparation_dependencies (
    job_id TEXT NOT NULL, unit_id TEXT NOT NULL, depends_on_unit_id TEXT NOT NULL,
    dependency_kind TEXT NOT NULL DEFAULT 'HARD', created_at TEXT NOT NULL,
    PRIMARY KEY(job_id, unit_id, depends_on_unit_id)
);
CREATE TABLE IF NOT EXISTS preparation_claim_snapshots (
    job_id TEXT NOT NULL, attempt_id TEXT NOT NULL, job_revision INTEGER NOT NULL DEFAULT 0,
    claimed_at TEXT NOT NULL, total_units INTEGER NOT NULL DEFAULT 0,
    required_units INTEGER NOT NULL DEFAULT 0, ready_units INTEGER NOT NULL DEFAULT 0,
    running_units INTEGER NOT NULL DEFAULT 0, missing_units INTEGER NOT NULL DEFAULT 0,
    prepared_ratio REAL NOT NULL DEFAULT 0, estimated_saved_ms INTEGER NOT NULL DEFAULT 0,
    speculative_work_ms INTEGER NOT NULL DEFAULT 0, queue_wait_ms INTEGER NOT NULL DEFAULT 0,
    queue_position_at_plan INTEGER NOT NULL DEFAULT 0, metadata_json TEXT NOT NULL DEFAULT '{}',
    PRIMARY KEY(job_id, attempt_id)
);
CREATE TABLE IF NOT EXISTS preparation_attempts (
    attempt_id TEXT PRIMARY KEY, unit_fingerprint TEXT NOT NULL,
    trigger_job_id TEXT NOT NULL DEFAULT '', worker_id TEXT NOT NULL DEFAULT '', host TEXT NOT NULL DEFAULT '',
    execution_mode TEXT NOT NULL, resource_class TEXT NOT NULL, scheduler_priority REAL NOT NULL DEFAULT 0,
    status TEXT NOT NULL, expected_work_ms INTEGER NOT NULL DEFAULT 0,
    workload_dimension TEXT NOT NULL DEFAULT '', workload_amount REAL NOT NULL DEFAULT 0,
    queued_at TEXT, started_at TEXT NOT NULL, finished_at TEXT, queue_wait_ms INTEGER NOT NULL DEFAULT 0,
    wall_ms INTEGER NOT NULL DEFAULT 0, singleflight_wait_ms INTEGER NOT NULL DEFAULT 0,
    bytes_read INTEGER NOT NULL DEFAULT 0, bytes_written INTEGER NOT NULL DEFAULT 0,
    network_rx_bytes INTEGER NOT NULL DEFAULT 0, network_tx_bytes INTEGER NOT NULL DEFAULT 0,
    cache_hit INTEGER NOT NULL DEFAULT 0, preempted_by_active INTEGER NOT NULL DEFAULT 0,
    estimated_saved_ms INTEGER NOT NULL DEFAULT 0, error_code TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS outbox_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT, event_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL DEFAULT '', aggregate_type TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL DEFAULT '', event_key TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending', attempt_count INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 10, last_error TEXT NOT NULL DEFAULT '',
    next_attempt_at TEXT, worker_id TEXT NOT NULL DEFAULT '', lease_id TEXT NOT NULL DEFAULT '',
    lease_expiry TEXT, completed_at TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_jobs_outbox_event_key ON outbox_events(event_key) WHERE event_key <> '';
CREATE INDEX IF NOT EXISTS idx_jobs_outbox_claim ON outbox_events(status, next_attempt_at, id);
