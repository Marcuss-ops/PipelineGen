-- database: all
-- 186_add_outbox_events_priority.sql
-- Outbox scheduling priority (higher = claimed first).
--
-- The outbox events pool drains asset.index.requested events in FIFO
-- order by (next_attempt_at, id). A stock acquisition that a script
-- generation job is blocked on can therefore wait behind a long
-- bulk-folder-sync backlog (observed: ~75 pending with 2 processing).
-- This migration adds a producer-stamped priority column; ClaimNext
-- orders by (priority DESC, next_attempt_at ASC, id ASC) so
-- script-required index requests jump the queue without disturbing
-- the lease-and-fence lifecycle.
--
-- Default 5 = normal (bulk-folder-sync / catalog re-sync). Producers
-- that need to be first in line (stock pipeline finalizer emitting
-- asset.index.requested for script-required assets) stamp priority=10.
ALTER TABLE outbox_events
    ADD COLUMN priority INTEGER NOT NULL DEFAULT 5;

-- ClaimNext candidate ordering is (status, priority DESC,
-- next_attempt_at ASC, id ASC). The pre-existing
-- idx_outbox_events_status_next_attempt index is kept for the legacy
-- ordering; this composite index serves the priority-aware claim path.
CREATE INDEX IF NOT EXISTS idx_outbox_events_status_priority_claim
    ON outbox_events(status, priority, next_attempt_at, id);
