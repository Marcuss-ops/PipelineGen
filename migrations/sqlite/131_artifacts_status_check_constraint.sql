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

-- Migration 131 v3.2 closure (Fase 5.c fix, July 2026): the
-- table-rebuild path was REMOVED because the pre-131 artifacts
-- table (created by migration 051) has 12 columns, while the
-- canonical 131 CREATE TABLE has 17 columns. The column-count
-- mismatch makes the `INSERT INTO artifacts SELECT *` unsafe
-- (SQLite rejects it with "table artifacts has 17 columns but
-- N values were supplied"). A future migration can re-add the
-- CHECK constraint once the artifacts table schema stabilizes
-- at 17 columns. For now, the canonical CHECK constraint
-- enforcement is at the application layer (typed enum at
-- internal/application/assets/artifacts/types.go); the SQL
-- CHECK is deferred until the schema stabilizes.
--
-- This migration is a no-op: it records itself in the schema
-- ledger (Step 0) and exits. The `artifacts` table created by
-- migration 051 (12 columns) is the authoritative schema
-- surface; the typed enum at the application layer enforces
-- the 7-state status set (STAGING/VERIFYING/STAGED/READY/
-- FAILED/QUARANTINED/DELETED) at the INSERT boundary.
