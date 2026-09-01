-- database: jobs
-- The script submission contract commits operations, jobs, and outbox_events
-- in one transaction. Keep the operations ledger on the same execution-plane
-- database as jobs when the split topology is enabled.
CREATE TABLE IF NOT EXISTS operations (
    operation_id TEXT PRIMARY KEY,
    scope TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    job_id TEXT NOT NULL,
    state TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    supersedes_operation_id TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_operations_idem_lookup
    ON operations(scope, idempotency_key, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_operations_state
    ON operations(state, updated_at DESC);
