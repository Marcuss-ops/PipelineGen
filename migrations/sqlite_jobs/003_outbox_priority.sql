-- database: jobs
-- Keep the execution-plane outbox schema aligned with the priority-aware
-- canonical claim query. Existing deployments predate the split-plane copy.
ALTER TABLE outbox_events ADD COLUMN priority INTEGER NOT NULL DEFAULT 5;
CREATE INDEX IF NOT EXISTS idx_outbox_events_status_priority_claim
    ON outbox_events(status, priority, next_attempt_at, id);
