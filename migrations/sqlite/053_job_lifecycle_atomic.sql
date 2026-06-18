-- 053_job_lifecycle_atomic.sql
-- Canonical 7-state job lifecycle with atomic CAS operations.
--
-- States: PENDING → LEASED → RUNNING → SUCCEEDED / RETRY_WAIT / FAILED / CANCELLED
-- PENDING / LEASED may also transition directly to CANCELLED.
-- RETRY_WAIT may transition to PENDING, FAILED, or CANCELLED.

-- 1. Add lease_id column for fencing token
ALTER TABLE jobs ADD COLUMN lease_id TEXT NOT NULL DEFAULT '';

-- 2. Mirgrate existing row states to canonical naming (best-effort: old code
--    already uses the 5 canonical states; this is a safety net).
UPDATE jobs SET status = 'PENDING' WHERE status = 'queued';
UPDATE jobs SET status = 'RUNNING' WHERE status = 'running';
UPDATE jobs SET status = 'SUCCEEDED' WHERE status = 'completed';
UPDATE jobs SET status = 'FAILED' WHERE status = 'failed';
UPDATE jobs SET status = 'CANCELLED' WHERE status = 'cancelled';

-- 3. Replace the old CHECK constraint.
--    SQLite does not support ALTER COLUMN; we rebuild the table.
--    Since this is a development database with manageable row counts,
--    the CREATE TABLE … AS SELECT pattern is safe inside a transaction.

BEGIN IMMEDIATE;

CREATE TABLE jobs_new (
    id              TEXT    NOT NULL PRIMARY KEY,
    type            TEXT    NOT NULL,
    status          TEXT    NOT NULL CHECK(status IN (
                            'PENDING',
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

INSERT INTO jobs_new SELECT * FROM jobs;

DROP TABLE jobs;
ALTER TABLE jobs_new RENAME TO jobs;

COMMIT;

-- 4. Index for ClaimNext: orders by priority DESC, created_at ASC
CREATE INDEX IF NOT EXISTS idx_jobs_claim
    ON jobs(status, priority DESC, created_at ASC);

-- 5. Index for the reaper: finds LEASED/RUNNING with expired lease
CREATE INDEX IF NOT EXISTS idx_jobs_expired_leases
    ON jobs(lease_expiry)
    WHERE status IN ('LEASED', 'RUNNING');

-- 6. Unique index: at most one active lease at a time
CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_active_lease
    ON jobs(lease_id)
    WHERE lease_id IS NOT NULL AND lease_id <> '';
