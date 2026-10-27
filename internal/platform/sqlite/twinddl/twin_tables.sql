-- twin_tables.sql — SINGLE SOURCE for the twin table declarations.
--
-- The nine table families below are declared twice by hand on the two SQLite
-- planes (canonical on migrations/sqlite, projection copy on
-- migrations/sqlite_jobs/001_jobs_plane.sql) and outbox_events is declared a
-- third time on the PostgreSQL media SSOT. Hand rewrites drift
-- (docs/migrations/BASELINE_INVENTORY.md "Twin declarations", audit point 7):
-- the job_checkpoints disappearance of 272 and the outbox_events.event_key
-- uniqueness divergence of 285 are the recorded instances.
--
-- This file is the ONE generated-from DDL source. cmd/twin-ddl-gen renders the
-- two plane projections from it (generated/twin_canonical.sql and
-- generated/twin_jobs_plane.sql), and the parity tests require BOTH planes'
-- migration declarations to keep matching those projections — so any drift
-- between the declarations of the two planes fails the build.
--
-- Canonical shape = the primary-plane post-everything shape (CREATE TABLE plus
-- every ALTER TABLE ADD COLUMN that followed). The jobs-plane projection
-- diverges ONLY by the pinned rules in projection.go:
--   1. CHECK constraints are dropped (the hand copy predates them and SQLite
--      cannot add a CHECK via ALTER TABLE; the value sets are application-
--      enforced by typed state enums on both planes).
--   2. Three historical timestamp DEFAULT expressions are dropped
--      (artifact_stages.created_at/updated_at, job_results.created_at): the
--      shared writers always set timestamps explicitly.
-- Column ORDER is contract-free (compared as a set), UNIQUE index NAMES are
-- contract-free (shape is what writers' ON CONFLICT clauses bind to), and
-- non-unique indexes are per-plane access patterns (not declared here).
--
-- The declarations below are byte-equivalent to the primary corpus
-- (147/119/092+186/216/242/243+245/244/246/247+249/272, converged by 285);
-- the parity test folds those files and requires equality with this source.

CREATE TABLE IF NOT EXISTS artifact_stages (
    id                 TEXT PRIMARY KEY,
    job_id             TEXT NOT NULL DEFAULT '',
    local_path         TEXT NOT NULL DEFAULT '',
    hash               TEXT NOT NULL DEFAULT '',
    size               INTEGER NOT NULL DEFAULT 0,
    mime               TEXT NOT NULL DEFAULT '',
    requirement        TEXT NOT NULL DEFAULT 'optional'
        CHECK (requirement IN ('required','optional')),
    destination        TEXT NOT NULL DEFAULT '',
    state              TEXT NOT NULL DEFAULT 'STAGED'
        CHECK (state IN ('STAGED','PUBLISHED','SUCCEEDED','FAILED_PERMANENT')),
    attempt_count      INTEGER NOT NULL DEFAULT 0,
    last_error         TEXT NOT NULL DEFAULT '',
    published_location TEXT NOT NULL DEFAULT '',
    published_at       TEXT,
    created_at         TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at         TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS job_checkpoints (
    job_id            TEXT NOT NULL,
    stage             TEXT NOT NULL,
    unit_id           TEXT NOT NULL,
    input_fingerprint TEXT NOT NULL,
    status            TEXT NOT NULL,
    artifact_sha256   TEXT NOT NULL DEFAULT '',
    artifact_uri      TEXT NOT NULL DEFAULT '',
    processor_version TEXT NOT NULL,
    completed_at      TEXT NOT NULL,
    PRIMARY KEY(job_id, stage, unit_id)
);

CREATE TABLE IF NOT EXISTS preparation_units (
    unit_fingerprint     TEXT PRIMARY KEY,
    fingerprint          TEXT NOT NULL DEFAULT '',
    unit_id              TEXT NOT NULL DEFAULT '',
    job_type             TEXT NOT NULL DEFAULT '',
    unit_kind            TEXT NOT NULL,
    fingerprint_version  TEXT NOT NULL,
    processor_version    TEXT NOT NULL,
    input_manifest_json  TEXT NOT NULL DEFAULT '{}',
    state                TEXT NOT NULL
        CHECK (state IN ('PLANNED', 'RUNNING', 'READY', 'FAILED', 'STALE')),
    resource_class       TEXT NOT NULL
        CHECK (resource_class IN ('NETWORK', 'DISK', 'CPU_LIGHT', 'CPU_HEAVY', 'LLM', 'TTS', 'GPU', 'DRIVE')),
    speculation_level    INTEGER NOT NULL DEFAULT 0
        CHECK (speculation_level BETWEEN 0 AND 5),
    cost_class           TEXT NOT NULL DEFAULT 'MEDIUM'
        CHECK (cost_class IN ('CHEAP', 'MEDIUM', 'EXPENSIVE')),
    reusable             INTEGER NOT NULL DEFAULT 1,
    preemptible          INTEGER NOT NULL DEFAULT 1,
    expected_work_ms     INTEGER NOT NULL DEFAULT 0,
    actual_work_ms       INTEGER NOT NULL DEFAULT 0,
    result_kind          TEXT NOT NULL DEFAULT 'NONE'
        CHECK (result_kind IN ('NONE', 'ARTIFACT_CACHE', 'CONTENT_OBJECT', 'DOMAIN_CACHE', 'INLINE_JSON')),
    result_ref           TEXT NOT NULL DEFAULT '',
    result_metadata_json TEXT NOT NULL DEFAULT '{}',
    artifact_id          TEXT NOT NULL DEFAULT '',
    cache_key            TEXT NOT NULL DEFAULT '',
    result_json          TEXT NOT NULL DEFAULT '{}',
    scheduler_owner      TEXT NOT NULL DEFAULT '',
    lease_owner          TEXT NOT NULL DEFAULT '',
    lease_until          TEXT,
    lease_expires_at     TEXT,
    attempt_count        INTEGER NOT NULL DEFAULT 0,
    created_at           TEXT NOT NULL,
    updated_at           TEXT NOT NULL,
    started_at           TEXT,
    ready_at             TEXT,
    last_accessed_at     TEXT,
    expires_at           TEXT,
    last_error_code      TEXT NOT NULL DEFAULT '',
    last_error_message   TEXT NOT NULL DEFAULT '',
    error                TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS preparation_job_units (
    job_id               TEXT NOT NULL,
    unit_id              TEXT NOT NULL,
    fingerprint          TEXT NOT NULL,
    required             INTEGER NOT NULL DEFAULT 1,
    adopted              INTEGER NOT NULL DEFAULT 0,
    queue_rank           INTEGER,
    planned_at           TEXT NOT NULL,
    adopted_at           TEXT,
    phase                TEXT NOT NULL DEFAULT '',
    scene_id             TEXT NOT NULL DEFAULT '',
    language             TEXT NOT NULL DEFAULT '',
    queue_distance       INTEGER NOT NULL DEFAULT 0,
    speculation_ceiling  INTEGER NOT NULL DEFAULT 0,
    priority_score       REAL NOT NULL DEFAULT 0,
    critical_path_ms     INTEGER NOT NULL DEFAULT 0,
    adoption_state       TEXT NOT NULL DEFAULT 'PENDING',
    promoted_at          TEXT,
    invalidated_at       TEXT,
    checkpoint_stage     TEXT NOT NULL DEFAULT '',
    checkpoint_unit_id   TEXT NOT NULL DEFAULT '',
    PRIMARY KEY(job_id, unit_id)
);

CREATE TABLE IF NOT EXISTS preparation_dependencies (
    job_id             TEXT NOT NULL,
    unit_id            TEXT NOT NULL,
    depends_on_unit_id TEXT NOT NULL,
    dependency_kind    TEXT NOT NULL DEFAULT 'HARD'
        CHECK (dependency_kind IN ('HARD', 'SOFT')),
    created_at         TEXT NOT NULL,
    PRIMARY KEY(job_id, unit_id, depends_on_unit_id)
);

CREATE TABLE IF NOT EXISTS preparation_claim_snapshots (
    job_id                 TEXT NOT NULL,
    attempt_id             TEXT NOT NULL,
    job_revision           INTEGER NOT NULL DEFAULT 0,
    claimed_at             TEXT NOT NULL,
    total_units            INTEGER NOT NULL DEFAULT 0,
    required_units         INTEGER NOT NULL DEFAULT 0,
    ready_units            INTEGER NOT NULL DEFAULT 0,
    running_units          INTEGER NOT NULL DEFAULT 0,
    missing_units          INTEGER NOT NULL DEFAULT 0,
    prepared_ratio         REAL NOT NULL DEFAULT 0,
    estimated_saved_ms     INTEGER NOT NULL DEFAULT 0,
    speculative_work_ms    INTEGER NOT NULL DEFAULT 0,
    queue_wait_ms          INTEGER NOT NULL DEFAULT 0,
    queue_position_at_plan INTEGER NOT NULL DEFAULT 0,
    metadata_json          TEXT NOT NULL DEFAULT '{}',
    PRIMARY KEY(job_id, attempt_id)
);

CREATE TABLE IF NOT EXISTS preparation_attempts (
    attempt_id            TEXT PRIMARY KEY,
    unit_fingerprint      TEXT NOT NULL,
    trigger_job_id        TEXT NOT NULL DEFAULT '',
    worker_id             TEXT NOT NULL DEFAULT '',
    host                  TEXT NOT NULL DEFAULT '',
    execution_mode        TEXT NOT NULL
        CHECK (execution_mode IN ('SPECULATIVE', 'ACTIVE', 'ADOPTION_CHECK')),
    resource_class        TEXT NOT NULL,
    scheduler_priority    REAL NOT NULL DEFAULT 0,
    status                TEXT NOT NULL
        CHECK (status IN ('RUNNING', 'READY', 'FAILED', 'CANCELLED', 'PREEMPTED', 'HIT')),
    expected_work_ms      INTEGER NOT NULL DEFAULT 0,
    workload_dimension    TEXT NOT NULL DEFAULT '',
    workload_amount       REAL NOT NULL DEFAULT 0,
    queued_at             TEXT,
    started_at            TEXT NOT NULL,
    finished_at           TEXT,
    queue_wait_ms         INTEGER NOT NULL DEFAULT 0,
    wall_ms               INTEGER NOT NULL DEFAULT 0,
    singleflight_wait_ms  INTEGER NOT NULL DEFAULT 0,
    bytes_read            INTEGER NOT NULL DEFAULT 0,
    bytes_written         INTEGER NOT NULL DEFAULT 0,
    network_rx_bytes      INTEGER NOT NULL DEFAULT 0,
    network_tx_bytes      INTEGER NOT NULL DEFAULT 0,
    cache_hit             INTEGER NOT NULL DEFAULT 0,
    preempted_by_active   INTEGER NOT NULL DEFAULT 0,
    estimated_saved_ms    INTEGER NOT NULL DEFAULT 0,
    error_code            TEXT NOT NULL DEFAULT '',
    error_message         TEXT NOT NULL DEFAULT '',
    created_at            TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS job_results (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id         TEXT NOT NULL,
    attempt        INTEGER NOT NULL DEFAULT 0,
    result_hash    TEXT NOT NULL DEFAULT '',
    codec_id       TEXT NOT NULL DEFAULT '',
    result_payload TEXT NOT NULL DEFAULT '{}',
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS uniq_job_results_dedup
    ON job_results(job_id, attempt, result_hash);

CREATE TABLE IF NOT EXISTS outbox_events (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type      TEXT NOT NULL,
    aggregate_id    TEXT NOT NULL DEFAULT '',
    aggregate_type  TEXT NOT NULL DEFAULT '',
    payload_json    TEXT NOT NULL DEFAULT '',
    event_key       TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'pending',
    attempt_count   INTEGER NOT NULL DEFAULT 0,
    max_attempts    INTEGER NOT NULL DEFAULT 10,
    last_error      TEXT NOT NULL DEFAULT '',
    next_attempt_at TEXT,
    worker_id       TEXT NOT NULL DEFAULT '',
    lease_id        TEXT NOT NULL DEFAULT '',
    lease_expiry    TEXT,
    completed_at    TEXT,
    priority        INTEGER NOT NULL DEFAULT 5,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_outbox_events_event_key
    ON outbox_events(event_key) WHERE event_key != '';
