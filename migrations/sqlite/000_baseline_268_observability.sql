-- 000_baseline_268_observability.sql — plane-pure SQLite baseline (observability)
-- database: observability
-- baseline-covers: 308
--
-- Generated from a clean database that ran the WHOLE incremental corpus
-- (including 307/308), so this dump contains exactly the objects the plane
-- owns: no business table is created and then dropped on a fresh install.
-- The runner applies it in preference to the frozen shared dump
-- 000_baseline_268.sql (see migrations_discovery.go::selectBaselineForPlane).
--
-- DO NOT EDIT MANUALLY — regenerate via `go run ./cmd/gen_baseline`
-- and verify with `go test ./internal/platform/sqlite -run TestMigrations_PlaneBaselines`.

CREATE TABLE IF NOT EXISTS api_requests (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts DATETIME DEFAULT CURRENT_TIMESTAMP,
  request_id TEXT,
  method TEXT NOT NULL,
  path TEXT NOT NULL,
  status INTEGER,
  duration_ms REAL,
  client_ip TEXT,
  user_id TEXT,
  bytes_in INTEGER,
  bytes_out INTEGER,
  user_agent TEXT,
  error TEXT
);
CREATE INDEX IF NOT EXISTS idx_api_requests_ts ON api_requests(ts);
CREATE INDEX IF NOT EXISTS idx_api_requests_path_status ON api_requests(path, status);
CREATE INDEX IF NOT EXISTS idx_api_requests_user ON api_requests(user_id, ts);
CREATE INDEX IF NOT EXISTS idx_api_requests_request_id ON api_requests(request_id);
CREATE TABLE IF NOT EXISTS outbox_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL DEFAULT '',
    aggregate_type TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL DEFAULT '',
    event_key TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 10,
    last_error TEXT NOT NULL DEFAULT '',
    next_attempt_at TEXT,
    worker_id TEXT NOT NULL DEFAULT '',
    lease_id TEXT NOT NULL DEFAULT '',
    lease_expiry TEXT,
    completed_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT ''
, priority INTEGER NOT NULL DEFAULT 5);
CREATE INDEX IF NOT EXISTS idx_outbox_events_status_next_attempt
    ON outbox_events(status, next_attempt_at, id);
CREATE TABLE IF NOT EXISTS monitor_enqueue_outbox (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    discovery_id      TEXT NOT NULL,
    idempotency_key   TEXT NOT NULL UNIQUE,
    payload_json      TEXT NOT NULL,
    state             TEXT NOT NULL DEFAULT 'pending',
    created_at        TEXT NOT NULL DEFAULT (datetime('now')),
    dispatched_at     TEXT,
    job_id            TEXT,
    error             TEXT
, retry_count INTEGER NOT NULL DEFAULT 0, next_retry_at TEXT, lease_id TEXT NOT NULL DEFAULT '', lease_until TEXT);
CREATE INDEX IF NOT EXISTS idx_monitor_outbox_drain
    ON monitor_enqueue_outbox(state, next_retry_at, created_at)
    WHERE state IN ('pending', 'dispatching');
CREATE TABLE IF NOT EXISTS admin_mutation_audit (
    id TEXT PRIMARY KEY,
    entity_type TEXT NOT NULL,
    entity_id TEXT NOT NULL,
    action TEXT NOT NULL,
    previous_json TEXT,
    next_json TEXT,
    changed_fields_json TEXT,
    actor TEXT NOT NULL,
    request_id TEXT,
    idempotency_key TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    success INTEGER NOT NULL DEFAULT 1,
    error_message TEXT
);
CREATE INDEX IF NOT EXISTS idx_admin_mutation_audit_entity
    ON admin_mutation_audit(entity_type, entity_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_admin_mutation_audit_actor
    ON admin_mutation_audit(actor, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_admin_mutation_audit_created_at
    ON admin_mutation_audit(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_outbox_events_status_priority_claim
    ON outbox_events(status, priority, next_attempt_at, id);
CREATE TABLE IF NOT EXISTS replay_bundles (
    original_job_id TEXT PRIMARY KEY,

    version TEXT NOT NULL,
    plan_sha256 TEXT NOT NULL,

    renderer_version TEXT NOT NULL,
    rust_protocol_version TEXT NOT NULL,
    ffmpeg_version TEXT NOT NULL,
    encoder_policy_hash TEXT NOT NULL DEFAULT '',

    render_plan_json TEXT NOT NULL,
    assets_json TEXT NOT NULL,

    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_replay_bundles_plan ON replay_bundles(plan_sha256);
CREATE TABLE IF NOT EXISTS resource_observations (
    observation_id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    job_id TEXT NOT NULL,
    worker_id TEXT NOT NULL DEFAULT '',
    host TEXT NOT NULL DEFAULT '',
    observed_at TEXT NOT NULL,

    cpu_avg_pct REAL,
    cpu_peak_pct REAL,
    rss_avg_bytes INTEGER,
    rss_peak_bytes INTEGER,
    gpu_avg_pct REAL,
    gpu_peak_pct REAL,
    vram_peak_bytes INTEGER,
    encoder_avg_pct REAL,
    temperature_peak_c REAL,
    disk_read_bytes INTEGER,
    disk_write_bytes INTEGER,
    network_rx_bytes INTEGER,
    network_tx_bytes INTEGER,

    metadata_json TEXT NOT NULL DEFAULT '{}'
, attempt_id TEXT NOT NULL DEFAULT '', swap_in_bytes INTEGER, swap_out_bytes INTEGER, disk_util_pct REAL, io_wait_pct REAL, disk_queue_depth REAL, decoder_avg_pct REAL, cpu_temp_peak_c REAL, gpu_temp_peak_c REAL, throttled INTEGER);
CREATE INDEX IF NOT EXISTS idx_resource_observations_run
    ON resource_observations(run_id, observed_at);
CREATE INDEX IF NOT EXISTS idx_resource_observations_job
    ON resource_observations(job_id, observed_at);
CREATE INDEX IF NOT EXISTS idx_resource_observations_host
    ON resource_observations(host, observed_at);
CREATE INDEX IF NOT EXISTS idx_resource_observations_attempt
    ON resource_observations(attempt_id, observed_at);
CREATE UNIQUE INDEX IF NOT EXISTS ux_outbox_events_event_key
    ON outbox_events(event_key) WHERE event_key != '';
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
 updated_at TEXT NOT NULL, workflow_payload_json TEXT NOT NULL DEFAULT '{}',
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
 bytes_processed INTEGER NOT NULL DEFAULT 0, started_at TEXT, finished_at TEXT, error_message TEXT NOT NULL DEFAULT '',
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
 error_code TEXT, queue_wait_ms INTEGER NOT NULL DEFAULT 0, worker_id TEXT, queued_at TEXT, started_at TEXT, finished_at TEXT, metadata_json TEXT NOT NULL DEFAULT '{}', output_duration_ms INTEGER NOT NULL DEFAULT 0, source_sha256 TEXT NOT NULL DEFAULT '', source_duration_ms INTEGER NOT NULL DEFAULT 0, source_size_bytes INTEGER NOT NULL DEFAULT 0, width INTEGER NOT NULL DEFAULT 0, height INTEGER NOT NULL DEFAULT 0, fps REAL NOT NULL DEFAULT 0, input_codec TEXT NOT NULL DEFAULT '', output_codec TEXT NOT NULL DEFAULT '', cache_hit INTEGER NOT NULL DEFAULT 0, strategy TEXT NOT NULL DEFAULT '', output_size_bytes INTEGER NOT NULL DEFAULT 0, cpu_user_ms INTEGER NOT NULL DEFAULT 0, cpu_system_ms INTEGER NOT NULL DEFAULT 0, created_at TEXT, error_message TEXT NOT NULL DEFAULT '',
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
CREATE INDEX IF NOT EXISTS idx_run_operation_observations_lifecycle
    ON run_operation_observations(run_id, started_at, finished_at);
CREATE INDEX IF NOT EXISTS idx_run_operation_observations_worker
    ON run_operation_observations(run_id, worker_id, started_at);
CREATE TABLE IF NOT EXISTS run_resource_reports (
    run_id             TEXT PRIMARY KEY,
    job_id             TEXT NOT NULL,
    attempt_id         TEXT NOT NULL UNIQUE,
    schema_version     INTEGER NOT NULL,
    started_at         TEXT,
    finished_at        TEXT,
    sample_count       INTEGER NOT NULL DEFAULT 0,
    report_json        TEXT NOT NULL,
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS run_resource_samples (
    sample_id          TEXT PRIMARY KEY,
    run_id             TEXT NOT NULL,
    job_id             TEXT NOT NULL,
    attempt_id         TEXT NOT NULL,
    observed_at        TEXT NOT NULL,
    sample_json        TEXT NOT NULL,
    created_at         TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_run_resource_reports_job
    ON run_resource_reports(job_id, updated_at);
CREATE INDEX IF NOT EXISTS idx_run_resource_samples_run
    ON run_resource_samples(run_id, observed_at);
CREATE INDEX IF NOT EXISTS idx_run_resource_samples_job
    ON run_resource_samples(job_id, observed_at);
CREATE TABLE IF NOT EXISTS run_resource_aggregates (
    run_id          TEXT PRIMARY KEY,
    job_id          TEXT NOT NULL,
    attempt_id      TEXT NOT NULL UNIQUE,
    schema_version  INTEGER NOT NULL,
    sample_count    INTEGER NOT NULL DEFAULT 0,
    first_observed_at TEXT,
    last_observed_at  TEXT,
    aggregate_json  TEXT NOT NULL,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_run_resource_samples_retention
    ON run_resource_samples(observed_at);
CREATE INDEX IF NOT EXISTS idx_run_resource_aggregates_retention
    ON run_resource_aggregates(updated_at);
CREATE TABLE IF NOT EXISTS run_report_snapshots (
    snapshot_id  INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id       TEXT NOT NULL,
    report_json  TEXT NOT NULL,
    captured_at  TEXT NOT NULL,
    FOREIGN KEY(run_id) REFERENCES run_observability(run_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_run_report_snapshots_run
    ON run_report_snapshots(run_id, snapshot_id);
CREATE TRIGGER trg_run_observability_snapshot_insert
AFTER INSERT ON run_observability
BEGIN
    INSERT INTO run_report_snapshots (run_id, report_json, captured_at)
    VALUES (NEW.run_id, NEW.report_json, NEW.updated_at);
END;
CREATE TRIGGER trg_run_observability_snapshot_update
AFTER UPDATE OF report_json ON run_observability
WHEN OLD.report_json IS NOT NEW.report_json
BEGIN
    INSERT INTO run_report_snapshots (run_id, report_json, captured_at)
    VALUES (NEW.run_id, NEW.report_json, NEW.updated_at);
END;
CREATE TRIGGER trg_run_report_snapshots_append_only
BEFORE UPDATE ON run_report_snapshots
BEGIN
    SELECT RAISE(ABORT, 'run_report_snapshots is append-only');
END;
CREATE TABLE IF NOT EXISTS run_artifact_checkpoints (
    run_id             TEXT NOT NULL,
    artifact_kind      TEXT NOT NULL,
    artifact_key       TEXT NOT NULL,

    schema_version     INTEGER NOT NULL
        CHECK (schema_version > 0),
    input_fingerprint  TEXT NOT NULL
        CHECK (input_fingerprint <> ''),
    processor_version  TEXT NOT NULL
        CHECK (processor_version <> ''),

    artifact_id        TEXT NOT NULL DEFAULT '',
    artifact_sha256    TEXT NOT NULL DEFAULT '',
    artifact_uri       TEXT NOT NULL DEFAULT '',
    artifact_size_bytes INTEGER NOT NULL DEFAULT 0
        CHECK (artifact_size_bytes >= 0),
    artifact_duration_us INTEGER NOT NULL DEFAULT 0
        CHECK (artifact_duration_us >= 0),

    payload_json       TEXT NOT NULL DEFAULT '{}',
    payload_sha256     TEXT NOT NULL
        CHECK (payload_sha256 <> ''),

    attempt            INTEGER NOT NULL DEFAULT 0
        CHECK (attempt >= 0),
    completed_at       TEXT NOT NULL,
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL,

    PRIMARY KEY (run_id, artifact_kind, artifact_key),
    FOREIGN KEY (run_id) REFERENCES run_observability(run_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_fk_run_observability_attempt_id ON run_observability(attempt_id);
CREATE INDEX IF NOT EXISTS idx_fk_run_child_observations_parent_run_id ON run_child_observations(parent_run_id);
CREATE INDEX IF NOT EXISTS idx_fk_run_artifact_checkpoints_run_id ON run_artifact_checkpoints(run_id);
