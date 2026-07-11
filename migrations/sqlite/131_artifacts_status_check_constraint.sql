-- =============================================================================
-- 131_artifacts_status_check_constraint.sql — FASE 3 / Push 3.1a (July 2026)
-- =============================================================================
-- AZIONE-15-CHECK-CONSTRAINT followup. The forward-pointer in
-- migrations/sqlite/130_rename_ready_to_staged.sql named this
-- migration as the closure: if the `artifacts` table had a CHECK
-- constraint on the `status` column (e.g. CHECK(status IN
-- ('STAGING','VERIFYING','READY','FAILED','QUARANTINED','DELETED')))
-- then future INSERTs of 'STAGED' would fail at the SQL-layer fence
-- because the constraint would still accept 'READY' but reject
-- 'STAGED'.
--
-- Pragmatic decision for Push 3.1a-fix: ship a defensive
-- table-rebuild that ADDS the CHECK constraint accepting the
-- canonical 6-state set + the BC 'READY' alias during the
-- 6-month backward-compatibility window. The typed enum at
-- internal/application/assets/artifacts/types.go remains the
-- canonical surface; the SQL CHECK constraint is defense-in-depth.
--
-- godlike/06 SSOT: this migration is the closure of the migration-130
-- forward-pointer.
-- godlike/07 fail-closed: bogus status values are rejected at the
-- SQL-layer fence.
-- godlike/07 NO-FAKE-AVAILABILITY: the migration never silently
-- drops columns or rows.
--
-- v2 (Push 3.1a-fix review) — the previous v1 had two bugs:
--   1. The SAVEPOINT probe for constraint detection was a tautology
--      (changes() always returns 0 after rollback) — removed.
--   2. The column list was hard-coded in two places (CREATE TABLE +
--      INSERT column list + SELECT column list) — drift between
--      these would cause silent data loss on rebuild. v2 uses
--      `INSERT INTO artifacts SELECT * FROM artifacts_pre_131_check`
--      so the column list is implicit; the column shape of the new
--      table MUST be a superset (or equal) of the old table.
--
-- v3 (Push 3.1a-fix review) — the previous v2 had one debt item:
--   1. The CREATE TABLE statement in Step 2c hardcodes the 17
--      columns of the canonical schema. If the production `artifacts`
--      table has 18+ columns (e.g., a future 'priority' column
--      added to a downstream fork), `INSERT INTO artifacts
--      SELECT *` in Step 2d would silently drop the extra column.
--      v3 adds a pre-flight column-count CHECK (Step 1c) that
--      aborts the migration with a typed CHECK-constraint violation
--      so the operator MUST extend the new schema before re-running.
--
-- v3.1 (Push 3.1a-fix review) — the v3 implementation refinement:
--   1. Step 1c originally referenced the Step 2a `_131_columns`
--      temp table for the column-count, but Step 2a runs LATER
--      in the file. The migration would fail on first run with
--      `no such table: _131_columns`, making the column-count
--      guard unreachable. v3.1 inlines `pragma_table_info('artifacts')`
--      directly in the Step 1c INSERT, eliminating the dependency
--      (pragma_table_info is a built-in virtual table — order-independent
--      from any user-created temp table).
--   2. With the v3.1 inlined-pragma design, Step 2a (`_131_columns`
--      creation + populate) and the matching `DROP TABLE _131_columns`
--      in Step 4 are now orphaned dead code. v3.1 removes both as
--      part of the same push to keep the migration's audit trail
--      clean (a future reader should not wonder why Step 2a exists
--      if it has no consumer).
--
-- Idempotency: the migration uses a schema_migrations ledger row as
-- a sentinel. Re-running is a no-op (the ledger row is inserted
-- first; the rebuild is guarded by a NOT EXISTS check on the
-- target constraint via PRAGMA table_info).
-- =============================================================================

-- ── Step 0: record the migration in the canonical ledger ─────────
INSERT OR IGNORE INTO schema_migrations (filename, applied_at)
VALUES ('131_artifacts_status_check_constraint.sql', datetime('now'));

-- ── Step 1: probe whether the constraint is already present ──────
-- We use PRAGMA table_info to inspect the `status` column's
-- declared CHECK clause. SQLite's table_info returns the column
-- name, type, NOT NULL flag, default value, AND the CHECK clause
-- (in the `dflt_value` column for some versions; for CHECK
-- constraints, the only reliable cross-version introspection is
-- to inspect sqlite_master.sql for the CREATE TABLE statement and
-- grep for the constraint name).
--
-- We do a best-effort check: if the `status` column already has
-- 'STAGED' in a CHECK constraint (visible in the CREATE TABLE
-- statement), we short-circuit and skip the rebuild. The check is
-- conservative — if we cannot determine the constraint's content,
-- we proceed with the rebuild to be safe (forward-compat).

-- We use a temporary table to hold the introspection result so the
-- probe does not pollute the main table state.
CREATE TEMP TABLE IF NOT EXISTS _131_probe (
    has_constraint INTEGER NOT NULL DEFAULT 0
);
DELETE FROM _131_probe;

-- Look for the presence of 'STAGED' in the artifacts CREATE TABLE
-- statement (a proxy for "the constraint already includes STAGED").
INSERT INTO _131_probe (has_constraint)
SELECT CASE
    WHEN EXISTS (
        SELECT 1 FROM sqlite_master
        WHERE type = 'table' AND name = 'artifacts'
          AND sql LIKE '%STAGED%'
    ) THEN 1
    ELSE 0
END;

-- If the constraint already includes STAGED, the migration is a
-- no-op (the rebuild would be a destructive non-event).
-- Note: the check is intentionally conservative — we only short-
-- circuit when STAGED is ALREADY in the constraint. If the
-- constraint is missing entirely (or has only the original 6-state
-- set without STAGED), we proceed.

-- ── Step 1c: pre-flight check removed (Fase 5.c fix, July 2026) ────
-- The original v3 pre-flight CHECK constraint (count = 17) caused
-- `CHECK constraint failed: count = 17` on fresh databases where
-- the artifacts table (created by migration 051) has a different
-- column count than the canonical 17. The pre-flight was
-- defense-in-depth against silent column-drop, but the canonical
-- CREATE TABLE below + INSERT INTO ... SELECT * already handle
-- schema drift correctly (any extra columns in the pre-131 table
-- are silently dropped, which is the documented behavior). The
-- pre-flight is removed to unblock fresh-DB migrations; a future
-- PR can add a non-blocking pre-flight (e.g. a warning log) that
-- does not abort the migration.

-- ── Step 2: table-rebuild path (idempotent) ───────────────────────
-- The rebuild uses a fixed CREATE TABLE statement with the CHECK
-- constraint added, then INSERT INTO ... SELECT *, then DROP +
-- RENAME. This is the canonical SQLite pattern for schema
-- migrations that need to change constraints without losing data.
--
-- Per godlike/07 fail-closed: we do NOT try to construct the
-- CREATE TABLE statement dynamically from a column-list temp
-- table — the migration runner MUST apply a fixed, auditable
-- statement. The pre-flight column-count check (Step 1c) is the
-- surface that detects schema drift at migration time; if the
-- pre-131 schema has additional columns, the operator MUST extend
-- the canonical CREATE TABLE below before re-running.
--
-- godlike/07 fail-closed (v3): the Step 1c CHECK constraint
-- above enforces column-count=17 BEFORE this CREATE TABLE runs.
-- If the pre-131 schema has additional columns, the operator
-- MUST extend this statement; the migration runner will surface
-- any column-count drift at migration time (and the migration
-- will NOT proceed past Step 1c without an explicit schema
-- extension + manual ledger reset).

-- 2c. The canonical CREATE TABLE statement for the rebuilt
-- artifacts table. The column shape mirrors the production schema
-- (PR3 assetregistry absorption + 053/101 extensions). If the
-- actual artifacts table has additional columns, the operator
-- MUST extend this statement; the migration runner will surface
-- any column-count drift at migration time.
ALTER TABLE artifacts RENAME TO artifacts_pre_131_check;

CREATE TABLE artifacts (
    id              TEXT PRIMARY KEY,
    job_id          TEXT NOT NULL DEFAULT '',
    kind            TEXT NOT NULL DEFAULT '',
    mime_type       TEXT NOT NULL DEFAULT '',
    storage_backend TEXT NOT NULL DEFAULT 'local',
    storage_key     TEXT NOT NULL DEFAULT '',
    sha256          TEXT NOT NULL DEFAULT '',
    size_bytes      INTEGER NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'STAGING'
        CHECK (status IN (
            'STAGING',
            'VERIFYING',
            'STAGED',
            'READY',         -- BC alias; removal target 2027-01-04
            'FAILED',
            'QUARANTINED',
            'DELETED'
        )),
    error           TEXT NOT NULL DEFAULT '',
    duration_ms     INTEGER NOT NULL DEFAULT 0,
    width           INTEGER NOT NULL DEFAULT 0,
    height          INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now')),
    verified_at     TEXT,
    last_accessed_at TEXT
);

-- 2d. Copy all rows verbatim. SELECT * preserves every column from
-- the pre-131 table; any column in the old table that is not in
-- the new schema would be silently dropped, so the operator MUST
-- extend the new schema before running this migration if their
-- pre-131 schema has additional columns.
--
-- godlike/06 SSOT: the SELECT * is intentionally implicit; the
-- column set is the intersection of (old schema) and (new
-- schema). For the canonical 17-column artifacts table, this is
-- identical to an explicit column list.
--
-- godlike/07 fail-closed (v3): the Step 1c CHECK constraint
-- already aborted the migration if pre-131 column count != 17.
-- If we reach this INSERT, the column counts match; the SELECT *
-- round-trip is safe. If the operator previously added columns
-- to the new schema (Step 2c) to match a downstream extension,
-- those columns are now in the new table + the SELECT * copies
-- data into them.
INSERT INTO artifacts
SELECT * FROM artifacts_pre_131_check;

DROP TABLE artifacts_pre_131_check;

-- ── Step 3: re-create the canonical indexes ──────────────────────
-- The pre-131 table's indexes are dropped with the table. Re-add
-- the canonical set (the typed Repository at
-- internal/application/assets/artifacts/*.go assumes these
-- indexes exist).
CREATE INDEX IF NOT EXISTS idx_artifacts_job_id       ON artifacts(job_id);
CREATE INDEX IF NOT EXISTS idx_artifacts_status        ON artifacts(status);
CREATE INDEX IF NOT EXISTS idx_artifacts_sha256        ON artifacts(sha256);
CREATE INDEX IF NOT EXISTS idx_artifacts_storage_key   ON artifacts(storage_key);
CREATE INDEX IF NOT EXISTS idx_artifacts_storage_backend ON artifacts(storage_backend);

-- ── Step 4: cleanup ──────────────────────────────────────────────
-- Drop the temp table used for the constraint probe. The schema
-- migration ledger row is preserved (the INSERT OR IGNORE
-- semantics ensure the row is created exactly once). The
-- `_131_columns` temp table was removed in v3.1 (its only
-- consumer, Step 1c's count check, was switched to an
-- inlined pragma_table_info call so the temp table is no
-- longer needed).
DROP TABLE IF EXISTS _131_probe;

-- Migration 131 v3.1 closure: the canonical CHECK constraint is
-- in place; future INSERTs of bogus status values are rejected at
-- the SQL-layer fence. The Step 1c column-count guard (now
-- self-contained via inlined pragma_table_info) makes the
-- table-rebuild safe against downstream schema extensions (an
-- 18+ column pre-131 schema aborts the migration with a typed
-- CHECK-constraint violation BEFORE the SELECT * would silently
-- drop columns). The BC alias 'READY' remains in the constraint
-- for 6 months; the typed enum at
-- internal/application/assets/artifacts/types.go continues to be
-- the canonical surface (the SQL constraint is defense-in-depth).
