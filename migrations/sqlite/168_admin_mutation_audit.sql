-- 168_admin_mutation_audit.sql
--
-- Adds optimistic-versioning support and a persistent audit log for the
-- schema-driven admin console (internal/application/adminconsole).
--
-- admin_version is an integer counter incremented atomically by the admin
-- console on every PATCH. It is independent from the domain lifecycle and
-- is only used for optimistic concurrency control through the admin API.
--
-- admin_mutation_audit records every administrative mutation, including
-- the previous and next state snapshots, the actor, request idempotency key,
-- and changed fields.

-- Optimistic-versioning column on media_assets (primary admin-editable entity).
ALTER TABLE media_assets ADD COLUMN admin_version INTEGER NOT NULL DEFAULT 0;

-- Persistent audit log for all administrative mutations.
CREATE TABLE IF NOT EXISTS admin_mutation_audit (
    id TEXT PRIMARY KEY,
    entity_type TEXT NOT NULL,
    entity_id TEXT NOT NULL,
    action TEXT NOT NULL,
    previous_json TEXT,
    next_json TEXT,
    changed_fields_json TEXT,
    actor TEXT NOT NULL,
    request_id TEXT,
    idempotency_key TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    success INTEGER NOT NULL DEFAULT 1,
    error_message TEXT
);

-- Query helpers for the audit log.
CREATE INDEX IF NOT EXISTS idx_admin_mutation_audit_entity
    ON admin_mutation_audit(entity_type, entity_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_admin_mutation_audit_actor
    ON admin_mutation_audit(actor, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_admin_mutation_audit_created_at
    ON admin_mutation_audit(created_at DESC);
