-- 069: Rebuild the jobs table CHECK constraint to accept the final
-- uppercase job lifecycle values.
--
-- Migration 053 introduced the atomic jobs table with PENDING/LEASED/
-- RUNNING/SUCCEEDED/RETRY_WAIT/FAILED/CANCELLED. Migration 066 updated
-- row data to the uppercase canonical names, but the table CHECK
-- constraint itself was never rebuilt. That left fresh databases with a
-- stale constraint rejecting QUEUED inserts from the current job
-- enqueuer. This migration recreates the table with the canonical
-- uppercase lifecycle and preserves all existing rows.

UPDATE jobs SET status = 'QUEUED'      WHERE status IN ('queued', 'PENDING');
UPDATE jobs SET status = 'LEASED'      WHERE status IN ('leased', 'LEASED');
UPDATE jobs SET status = 'RUNNING'     WHERE status IN ('running', 'RUNNING');
UPDATE jobs SET status = 'RETRY_WAIT'  WHERE status IN ('retry_wait', 'RETRY_WAIT');
UPDATE jobs SET status = 'SUCCEEDED'   WHERE status IN ('completed', 'SUCCEEDED');
UPDATE jobs SET status = 'FAILED'      WHERE status IN ('failed', 'FAILED');
UPDATE jobs SET status = 'CANCELLED'   WHERE status IN ('cancelled', 'CANCELLED');

DROP VIEW IF EXISTS media_index_outbox_pending;
DROP TRIGGER IF EXISTS trg_outbox_updated_at;

CREATE TABLE jobs_new (
    id              TEXT    NOT NULL PRIMARY KEY,
    type            TEXT    NOT NULL,
    status          TEXT    NOT NULL CHECK(status IN (
                            'QUEUED',
                            'LEASED',
                            'RUNNING',
                            'SUCCEEDED',
                            'RETRY_WAIT',
                            'FAILED',
                            'CANCELLED'
                        )),
    priority        INTEGER NOT NULL DEFAULT 0,
    project         TEXT    NOT NULL DEFAULT '',
    video_name      TEXT    NOT NULL DEFAULT '',
    active_key      TEXT    NOT NULL DEFAULT '',
    correlation_id  TEXT    NOT NULL DEFAULT '',
    payload_json    TEXT    NOT NULL DEFAULT '{}',
    result_json     TEXT    NOT NULL DEFAULT '{}',
    progress        INTEGER NOT NULL DEFAULT 0,
    error           TEXT    NOT NULL DEFAULT '',
    retry_count     INTEGER NOT NULL DEFAULT 0,
    max_retries     INTEGER NOT NULL DEFAULT 3,
    worker_id       TEXT    NOT NULL DEFAULT '',
    lease_id        TEXT    NOT NULL DEFAULT '',
    lease_expiry    TEXT,
    created_at      TEXT    NOT NULL,
    updated_at      TEXT    NOT NULL,
    started_at      TEXT,
    completed_at    TEXT,
    cancelled_at    TEXT,
    revision        INTEGER NOT NULL DEFAULT 1
);

INSERT INTO jobs_new (
    id, type, status, priority, project, video_name, active_key,
    correlation_id, payload_json, result_json, progress, error,
    retry_count, max_retries, worker_id, lease_id, lease_expiry,
    created_at, updated_at, started_at, completed_at, cancelled_at, revision
) SELECT
    id, type, status, priority, project, video_name, active_key,
    correlation_id,
    COALESCE(payload_json, '{}'),
    COALESCE(result_json, '{}'),
    progress,
    COALESCE(error, ''),
    retry_count, max_retries,
    COALESCE(worker_id, ''),
    lease_id, lease_expiry,
    created_at, updated_at,
    started_at, completed_at, cancelled_at,
    COALESCE(revision, 1)
FROM jobs;

DROP TABLE jobs;
ALTER TABLE jobs_new RENAME TO jobs;

CREATE INDEX IF NOT EXISTS idx_jobs_claim
    ON jobs(status, priority DESC, created_at ASC);

CREATE INDEX IF NOT EXISTS idx_jobs_expired_leases
    ON jobs(lease_expiry)
    WHERE status IN ('LEASED', 'RUNNING');

CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_active_lease
    ON jobs(lease_id)
    WHERE lease_id IS NOT NULL AND lease_id <> '';
