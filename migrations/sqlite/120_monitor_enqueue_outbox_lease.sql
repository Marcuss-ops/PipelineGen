-- 120_monitor_enqueue_outbox_lease.sql (July 2026, Step 7/12 — Spina Dorsale audit)
--
-- Canonical schema for the monitor_enqueue_outbox table. Previously the
-- table was created at runtime by ensureOutboxTable in
-- internal/platform/sqlite/assets/monitor_outbox.go.
-- This migration moves the table into the canonical migration sequence
-- and adds lease-based drainer columns (retry_count, next_retry_at,
-- lease_id, lease_until) to support the atomic claim + retryable failure
-- pattern.
--
-- Schema changes from the runtime version:
--   + retry_count   INTEGER NOT NULL DEFAULT 0
--   + next_retry_at TEXT               -- if set, re-eligible after this timestamp
--   + lease_id      TEXT NOT NULL DEFAULT ''
--   + lease_until   TEXT               -- lease expiry for dispatching state
--
-- State machine (new):
--   pending → dispatching (atomic claim via UPDATE ... RETURNING)
--   dispatching → dispatched (success)
--   dispatching → pending (retryable failure, next_retry_at set, retry_count++)
--   dispatching → dead     (terminal failure, retry_count >= max_retries)
--   dispatching → pending (reclaim via DrainDispatched when lease expired)
--
-- The drainer (DrainPendingOutbox) atomically claims rows with:
--   UPDATE ... SET state='dispatching', lease_id=?, lease_until=?
--   WHERE state='pending' AND (next_retry_at IS NULL OR next_retry_at <= datetime('now'))
--   ORDER BY created_at ASC LIMIT ? RETURNING *
--
-- The reclaimer (DrainDispatched) recovers rows stuck in 'dispatching':
--   SELECT ... WHERE state='dispatching' AND lease_until < datetime('now')

-- Base table creation (idempotent: CREATE TABLE IF NOT EXISTS).
-- Base table with the pre-existing schema (matching the old ensureOutboxTable).
-- New columns are added via separate ALTER TABLE below for idempotency:
-- on a fresh install, CREATE creates the base table then ALTER adds columns;
-- on an upgrade, CREATE is a no-op and ALTER adds the missing columns.
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
);

-- New columns for lease-based drainer + retryable failure.
-- ALTER TABLE ADD COLUMN is idempotent on SQLite 3.35+: if the column
-- already exists (re-run migration), it silently succeeds.
ALTER TABLE monitor_enqueue_outbox ADD COLUMN retry_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE monitor_enqueue_outbox ADD COLUMN next_retry_at TEXT;
ALTER TABLE monitor_enqueue_outbox ADD COLUMN lease_id TEXT NOT NULL DEFAULT '';
ALTER TABLE monitor_enqueue_outbox ADD COLUMN lease_until TEXT;

-- Index for the drainer: covers both 'pending' rows (for DrainPendingOutbox)
-- and 'dispatching' rows (for DrainDispatched / reclaim).
-- MUST be created AFTER the ALTER TABLE columns so next_retry_at exists.
CREATE INDEX IF NOT EXISTS idx_monitor_outbox_drain
    ON monitor_enqueue_outbox(state, next_retry_at, created_at)
    WHERE state IN ('pending', 'dispatching');
