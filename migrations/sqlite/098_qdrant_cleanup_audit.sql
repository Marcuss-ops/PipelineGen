-- 098_qdrant_cleanup_audit.sql
--
-- QDRANT-005 closure (PR5 followup, June 2026): promote the
-- qdrant_cleanup_audit table from a lazy CREATE TABLE IF NOT EXISTS
-- inside qdrant.Reaper.persistReaperAudit onto the canonical migration
-- runner. Justification:
--   1. CREATE TABLE on the Reaper hot path costs the operator a
--      per-run schema-management syscall (CREATE → catalog lock →
--      journal append). On contended databases this stalls; on
--      read-only / constraint-blocked mounts it errors at the
--      boundary that is most expensive to recover from (a partial
--      Reap run that already mutated Qdrant).
--   2. Migration runner is the canonical schema owner. Schema
--      declared in a migration is contractually bound to the
--      companion code in the same PR; declarations split between
--      migrations/ and internal/platform/sqlite/<pkg>/
--      drift silently.
--   3. Tests that construct synthetic DBs (CI fixtures, replay
--      tooling) include this table by default when running
--      migrations; the test currently passes because persistReaperAudit
--      self-heals via CREATE. After this migration lands the table
--      is presumed-present and any test that forgets to apply
--      migrations surfaces immediately with the canonical
--      "no such table" error rather than the silent CREATE.
--
-- Schema must reproduce the projection that persistReaperAudit
-- INSERTs — diff-by-eye alignment between schema columns and INSERT
-- variable list is the contract for "schema matches code":
--   - run_id TEXT PRIMARY KEY drives INSERT OR REPLACE semantics
--     (re-run safety on the same run id produced by generateRunID)
--   - collection NOT NULL because every audit row corresponds to a
--     specific Reap target
--   - started_at / completed_at stored as RFC3339 TEXT to mirror the
--     existing canonical convention across the db (artlist_jobs,
--     outbox_events, etc.)
--   - status NOT NULL because the StatusPartial / StatusOK /
--     StatusNoop / StatusFailed / StatusRunning set is the
--     operator-facing signal
--   - points_scanned / points_affected INTEGER NOT NULL DEFAULT 0
--     so dashboards can SUM() without first guarding NULL
--   - errors_json / keys_redacted_json store the JSON-stringified
--     slices that persistReaperAudit serialises with jsonMarshal
--     (we DO NOT store JSON objects — SQLite stores JSON as TEXT
--     and reading back is identical to reading back a stringified
--     representation; pinning TEXT here lets future tooling
--     (jq-style filters, dashboards) do straightforward JSON.parse)
--   - dry_run INTEGER NOT NULL DEFAULT 0 (SQLite has no native BOOLEAN;
--     0/1 matches persistReaperAudit's dryRun int projection)
--
-- Idempotent: CREATE TABLE IF NOT EXISTS + IF NOT EXISTS on the
-- collection index. Re-applying the migration on a database that
-- already has the table (pre-PR5 production dbs where the lazy CREATE
-- ran first) is a no-op.
--
-- Companion code:
--   internal/infrastructure/qdrant/reaper.go
--     persistReaperAudit (INSERT path only — lazy CREATE removed by
--     this PR). Reap() callers that supply ReaperOptions.DB
--     non-nil receive an audit row on every non-noop run; misshapen
--     rows are surfaced via the AuditPersisted=false signal
--     documented on ReaperResult.
--   internal/infrastructure/qdrant/reaper_test.go
--     Contract smoke tests (DefaultReaperKeys empty, batch hard
--     cap, no-keys StatusNoop, redactPayload). None of the existing
--     tests exercise the audit DB path; the contract for "schema
--     is migration-owned" is enforced by the existence of this
--     file rather than by a runtime test — the runner picks it
--     up at boot, the runtime INSERT error if the table is missing
--     surfaces a loud failure rather than the silent CREATE.
--
-- Order rationale: PRIMARY KEY first (SQLite convention) → target
-- (collection) → time window (started_at, completed_at) → outcome
-- (status, points_scanned, points_affected) → payload metadata
-- (errors_json, dry_run, keys_redacted_json). Index on collection
-- because every audit query (reaper dashboards, last-N-per-collection
-- trend lines) starts with "WHERE collection = ?".
--
-- Migration number note: this migration lands at 098 because
-- 097_add_media_assets_search_terms.sql already occupies the slot
-- (historical naming drift in branches that re-introduced the
-- 'search terms' column). The QDRANT-005 audit-priorities doc
-- referred to this table as "migration 97" — adjust the audit-priorities
-- reference when cherry-picking the change into audit-history.

CREATE TABLE IF NOT EXISTS qdrant_cleanup_audit (
    run_id           TEXT PRIMARY KEY,
    collection       TEXT NOT NULL,
    started_at       TEXT NOT NULL,
    completed_at     TEXT NOT NULL,
    status           TEXT NOT NULL,
    points_scanned   INTEGER NOT NULL DEFAULT 0,
    points_affected  INTEGER NOT NULL DEFAULT 0,
    errors_json      TEXT NOT NULL DEFAULT '[]',
    dry_run          INTEGER NOT NULL DEFAULT 0,
    keys_redacted_json TEXT NOT NULL DEFAULT '[]'
);

-- Required by audit dashboards: per-collection trend lines scan by
-- collection, then order by completed_at. Composite index keeps the
-- hot query path off a full table scan. Conditional so reapplying is
-- a no-op on dbs that already have it.
CREATE INDEX IF NOT EXISTS idx_qdrant_cleanup_audit_collection_completed
    ON qdrant_cleanup_audit(collection, completed_at);
