-- 092_create_outbox_events.sql
--
-- CREATES the canonical outbox_events table for the event-dispatch
-- pipeline (canonical owner: internal/platform/sqlite/outboxevents/).
--
-- This migration is FORMER OBLIGATION. The table was previously bootstrapped
-- ad-hoc via `sqlite3 data/media/media.db.sqlite < ...sql` on the production
-- database because no migration ever declared it — Repository.Enqueue, the
-- outbox Pool worker, and the realtime.IndexHealth counters all expected this
-- table to exist, and `outboxevents.ClaimNext: no such table: outbox_events`
-- was the symptom of the drift. Migration 042 was the canonical name dropped
-- (see migrations/sqlite/056_drop_media_index_outbox.sql) but no follow-up
-- reproduce-the-schema migration landed.
--
-- Schema must reproduce what the application code expects, exactly:
--   - `id INTEGER PRIMARY KEY AUTOINCREMENT` → scans into Event.ID (int64)
--   - All columns + defaults match Enqueue / scanEvent / MarkFailed queries
--   - UNIQUE index on event_key enables `ON CONFLICT(event_key) WHERE event_key != '' DO NOTHING`
--     used by Enqueue for idempotent enqueue (event_key may be empty for one-shot inserts)
--   - status index speeds up ClaimNext's `WHERE status = 'pending'` lookup
--
-- Column ordering follows the canonical INSERT projection in
-- Repository.Enqueue for diff-by-eye alignment between schema and queries.
--
-- Idempotent: IF NOT EXISTS everywhere. Re-applying on a database that already
-- has the table from the ad-hoc sqlite3 bootstrap is a no-op (no NEW index
-- creation, no schema drift). Verified by reading it back via `PRAGMA table_info`.
--
-- Companion code:
--   internal/platform/sqlite/outboxevents/repository.go
--     Enqueue, ClaimNext (CTE), RenewLease, MarkCompleted, MarkFailed,
--     RequeueExpiredLeases, CountByStatus, ListPending.
--   internal/platform/sqlite/outboxevents/pool.go
--     Drains the table via ClaimNext lease-and-fence pattern.
--
-- Order rationale: PRIMARY KEY first (SQLite convention) → identity columns
-- (event_type, aggregate_*) → payload (payload_json) → idempotency vector
-- (event_key) → lifecycle state (status, attempt_count, max_attempts) →
-- failure tracking (last_error, next_attempt_at) → worker claim slot
-- (worker_id, lease_id, lease_expiry) → completion (completed_at) →
-- audit (created_at, updated_at).

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
);

-- Required by Enqueue: `ON CONFLICT(event_key) WHERE event_key != '' DO NOTHING`.
-- Empty event_key is allowed (one-shot inserts), so the uniqueness triggers
-- only when event_key is non-empty.
CREATE UNIQUE INDEX IF NOT EXISTS ux_outbox_events_event_key
    ON outbox_events(event_key);

-- Optional perf index for ClaimNext's `WHERE status = 'pending' ORDER BY ... LIMIT 1`
-- and CountByStatus. The CTE in ClaimNext scans a candidate set ordered by
-- (next_attempt_at, id), so a composite index helps OLTP throughput.
CREATE INDEX IF NOT EXISTS idx_outbox_events_status_next_attempt
    ON outbox_events(status, next_attempt_at, id);
