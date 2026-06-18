-- Migration 052: deliveries table for multi-provider artifact delivery.
-- Each delivery pushes a READY artifact to an external target (Drive, YouTube, S3).
-- Lifecycle: PENDING → LEASED → RUNNING → SUCCEEDED/FAILED/RETRY_WAIT/BLOCKED_AUTH.

CREATE TABLE IF NOT EXISTS deliveries (
    id               TEXT PRIMARY KEY,
    artifact_id      TEXT NOT NULL,
    target_id        TEXT NOT NULL DEFAULT '',
    provider         TEXT NOT NULL DEFAULT 'drive',
    status           TEXT NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING','LEASED','RUNNING','RETRY_WAIT','SUCCEEDED','FAILED','BLOCKED_AUTH','CANCELLED')),
    attempt_count    INTEGER NOT NULL DEFAULT 0,
    max_attempts     INTEGER NOT NULL DEFAULT 3,
    next_attempt_at  TEXT,
    lease_id         TEXT NOT NULL DEFAULT '',
    lease_expires_at TEXT,
    remote_id        TEXT NOT NULL DEFAULT '',
    remote_url       TEXT NOT NULL DEFAULT '',
    last_error       TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at       TEXT NOT NULL DEFAULT (datetime('now')),
    completed_at     TEXT
);

CREATE INDEX IF NOT EXISTS idx_deliveries_status ON deliveries(status, next_attempt_at);
CREATE INDEX IF NOT EXISTS idx_deliveries_artifact ON deliveries(artifact_id);
