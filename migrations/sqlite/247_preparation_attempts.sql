-- =============================================================================
-- 247_preparation_attempts.sql — Preparation Fabric (Control Plane) v2
-- =============================================================================
--
-- Context:
--   Auditing/telemetry history for EVERY execution attempt against a
--   preparation unit, whether that execution happened speculatively
--   (ahead of the critical path), actively (owned by a job on the critical
--   path), or during an adoption check (verifying an already-prepared result).
--
--   `preparation_units` holds ONLY the CURRENT state of a unit. It must never
--   accumulate history. This table is the append-only ledger that answers the
--   question the whole speculative-execution feature exists for:
--
--       "Is speculation actually SAVING time, or just burning resources?"
--
--   The scheduler feeds on this data (EMA work estimates, saved-ms accounting,
--   preemption decisions). Each attempt carries the trigger job, worker/host,
--   execution mode, per-resource byte counts, work timing, and a cache-hit /
--   preemption / saved-ms summary.
--
--   Idempotency:
--     * CREATE TABLE IF NOT EXISTS → idempotent.
--     * CREATE INDEX IF NOT EXISTS → idempotent.
-- =============================================================================

CREATE TABLE IF NOT EXISTS preparation_attempts (
    attempt_id          TEXT PRIMARY KEY,

    unit_fingerprint    TEXT NOT NULL,

    trigger_job_id      TEXT NOT NULL DEFAULT '',

    worker_id           TEXT NOT NULL DEFAULT '',
    host                TEXT NOT NULL DEFAULT '',

    execution_mode      TEXT NOT NULL
                        CHECK (execution_mode IN ('SPECULATIVE', 'ACTIVE', 'ADOPTION_CHECK')),

    resource_class      TEXT NOT NULL,

    scheduler_priority  REAL NOT NULL DEFAULT 0,

    status              TEXT NOT NULL
                        CHECK (status IN ('RUNNING', 'READY', 'FAILED', 'CANCELLED', 'PREEMPTED', 'HIT')),

    expected_work_ms    INTEGER NOT NULL DEFAULT 0,

    workload_dimension  TEXT NOT NULL DEFAULT '',
    workload_amount     REAL NOT NULL DEFAULT 0,

    queued_at           TEXT,
    started_at          TEXT NOT NULL,
    finished_at         TEXT,

    queue_wait_ms       INTEGER NOT NULL DEFAULT 0,
    wall_ms             INTEGER NOT NULL DEFAULT 0,

    singleflight_wait_ms INTEGER NOT NULL DEFAULT 0,

    bytes_read          INTEGER NOT NULL DEFAULT 0,
    bytes_written       INTEGER NOT NULL DEFAULT 0,
    network_rx_bytes    INTEGER NOT NULL DEFAULT 0,
    network_tx_bytes    INTEGER NOT NULL DEFAULT 0,

    cache_hit           INTEGER NOT NULL DEFAULT 0,

    preempted_by_active INTEGER NOT NULL DEFAULT 0,

    estimated_saved_ms  INTEGER NOT NULL DEFAULT 0,

    error_code          TEXT NOT NULL DEFAULT '',
    error_message       TEXT NOT NULL DEFAULT '',

    created_at          TEXT NOT NULL
);

-- Per-unit execution history (what the scheduler reads for EMA estimates).
CREATE INDEX IF NOT EXISTS idx_preparation_attempts_unit
    ON preparation_attempts(unit_fingerprint, started_at);

-- Per-job attempt history (what dashboards read per job).
CREATE INDEX IF NOT EXISTS idx_preparation_attempts_job
    ON preparation_attempts(trigger_job_id, started_at);

-- Mode × status scan (speculation-efficiency reporting).
CREATE INDEX IF NOT EXISTS idx_preparation_attempts_mode
    ON preparation_attempts(execution_mode, status, started_at);