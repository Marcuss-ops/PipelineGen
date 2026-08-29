-- =============================================================================
-- 248_preparation_claim_snapshots.sql — Preparation Fabric (Control Plane) v2
-- =============================================================================
--
-- Context:
--   Stores a durable "photograph" of how ready a job was AT THE INSTANT IT WAS
--   CLAIMED. Without this snapshot the data is quickly lost: immediately after
--   claim the units transition RUNNING → READY and MISS → READY, so the
--   pristine pre-claim state (how much speculative work had already finished)
--   would otherwise be unrecoverable.
--
--   This is the table the dashboard is built on:
--
--     required = 38   ready = 31   running = 5   missing = 2
--     prepared_ratio = 81.6%
--     estimated work saved = 231.4 sec
--
--   The primary KPI is prepared_at_claim_ratio = ready_required_units /
--   total_required_units, which measures directly how much critical-path work
--   was moved ahead of the claim:
--
--     cold jobs    0–20%
--     normal queue 50–80%
--     N+1 spec    80–95%
--     warm replay 95–100%
--
--   One row per (job, attempt): a job re-attempted (new job_revision) inserts a
--   fresh snapshot rather than mutating the prior one.
--
--   Idempotency:
--     * CREATE TABLE IF NOT EXISTS → idempotent.
--     * CREATE INDEX IF NOT EXISTS → idempotent.
-- =============================================================================

CREATE TABLE IF NOT EXISTS preparation_claim_snapshots (
    job_id                  TEXT NOT NULL,
    attempt_id              TEXT NOT NULL,

    job_revision            INTEGER NOT NULL DEFAULT 0,

    claimed_at              TEXT NOT NULL,

    total_units             INTEGER NOT NULL DEFAULT 0,
    required_units          INTEGER NOT NULL DEFAULT 0,

    ready_units             INTEGER NOT NULL DEFAULT 0,
    running_units           INTEGER NOT NULL DEFAULT 0,
    missing_units           INTEGER NOT NULL DEFAULT 0,

    prepared_ratio          REAL NOT NULL DEFAULT 0,

    estimated_saved_ms      INTEGER NOT NULL DEFAULT 0,

    speculative_work_ms     INTEGER NOT NULL DEFAULT 0,

    queue_wait_ms           INTEGER NOT NULL DEFAULT 0,

    queue_position_at_plan  INTEGER NOT NULL DEFAULT 0,

    metadata_json           TEXT NOT NULL DEFAULT '{}',

    PRIMARY KEY (job_id, attempt_id)
);

-- Claim-time KPI scanning / dashboard rollups.
CREATE INDEX IF NOT EXISTS idx_preparation_claim_snapshots_claimed
    ON preparation_claim_snapshots(claimed_at);