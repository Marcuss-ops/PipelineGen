-- database: primary
-- Migration 202: canonical mutation Unit of Work ledger.
-- The row is inserted in the same transaction as the domain mutation,
-- audit event, and transactional outbox event.
CREATE TABLE IF NOT EXISTS canonical_mutations (
    command_id       TEXT PRIMARY KEY,
    idempotency_key  TEXT NOT NULL UNIQUE,
    request_hash     TEXT NOT NULL DEFAULT '',
    status           TEXT NOT NULL CHECK (status IN ('IN_PROGRESS', 'COMPLETED')),
    result_json      TEXT NOT NULL DEFAULT '{}',
    created_at       TEXT NOT NULL,
    completed_at     TEXT,
    error_message    TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_canonical_mutations_status_created
    ON canonical_mutations(status, created_at);
CREATE INDEX IF NOT EXISTS idx_canonical_mutations_request_hash
    ON canonical_mutations(request_hash);
