-- database: primary
-- Migration 199: artifact cache registry + durable avoided-work metrics.
--
-- A cache row maps one deterministic computation (source bytes,
-- operation, canonical parameters and processor version) to an immutable
-- CAS object. The cache is a projection of Control Plane + CAS: deleting
-- it only makes processing slower, never changes canonical media state.

CREATE TABLE IF NOT EXISTS artifact_cache_entries (
    cache_key          TEXT PRIMARY KEY,
    source_sha256      TEXT NOT NULL,
    operation          TEXT NOT NULL,
    parameters_json    TEXT NOT NULL,
    processor_version  TEXT NOT NULL,
    artifact_sha256    TEXT NOT NULL,
    size_bytes         INTEGER NOT NULL DEFAULT 0,
    mime_type          TEXT NOT NULL DEFAULT '',
    status             TEXT NOT NULL DEFAULT 'READY'
                       CHECK (status IN ('BUILDING','READY','FAILED','INVALID')),
    lease_id           TEXT NOT NULL DEFAULT '',
    lease_until        TEXT,
    created_at         TEXT NOT NULL,
    last_accessed_at   TEXT NOT NULL,
    updated_at         TEXT NOT NULL,
    error_message      TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_artifact_cache_source_operation
    ON artifact_cache_entries(source_sha256, operation, processor_version);
CREATE INDEX IF NOT EXISTS idx_artifact_cache_status_lease
    ON artifact_cache_entries(status, lease_until);

CREATE TABLE IF NOT EXISTS artifact_cache_metrics (
    operation             TEXT PRIMARY KEY,
    hit_count             INTEGER NOT NULL DEFAULT 0,
    miss_count            INTEGER NOT NULL DEFAULT 0,
    invalidation_count    INTEGER NOT NULL DEFAULT 0,
    avoided_bytes         INTEGER NOT NULL DEFAULT 0,
    avoided_work_ms       INTEGER NOT NULL DEFAULT 0,
    created_at            TEXT NOT NULL,
    updated_at            TEXT NOT NULL
);
