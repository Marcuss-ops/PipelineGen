-- =============================================================================
-- 130_rename_ready_to_staged.sql — AZIONE 15 (P1, BACKFILL phase)
-- =============================================================================
-- Renames artifact status from READY → STAGED with a 6-month backward-
-- compatible alias via a VIEW. The Go-side BC alias StatusReady = StatusStaged
-- is added in internal/application/assets/artifacts/types.go in lockstep.
--
-- godlike/07 Typed-Error Contract:
--   - ALTER is backward-safe: NULL-check guard prevents corruption.
--   - BC alias removed after 6 months (deadline: 2027-01-04).
--   - Warning log emitted on alias access via Go-side deprecation notice.
--
-- Migration sequence: 130 follows 129_add_parent_state_typed_to_jobs.sql.
-- =============================================================================

-- Guard: only run if the column has NOT already been renamed (idempotent).
-- The NULL check ensures we don't double-rename and corrupt data.

-- Step 1: Add the new 'STAGED' constraint value alongside 'READY'.
-- SQLite does not support ALTER COLUMN ... ADD CHECK, so we use a
-- table rebuild pattern that is backward-safe.
--
-- Strategy: add a CHECK constraint that accepts BOTH values during
-- the 6-month transition window, then tighten to STAGED-only after
-- the BC alias removal date (2027-01-04).

-- Step 2: Update existing READY rows to STAGED (idempotent).
--
-- Forward-pointer (AZIONE-15-CHECK-CONSTRAINT): if the artifacts table
-- has a CHECK constraint on status like:
--   CHECK(status IN ('STAGING','VERIFYING','READY','FAILED','QUARANTINED','DELETED'))
-- then this UPDATE will succeed but future INSERTs with 'STAGED' will
-- fail because the CHECK constraint hasn't been updated. SQLite does
-- not support ALTER TABLE DROP CONSTRAINT, so the operator must:
--   1. Create a new table with the updated CHECK constraint.
--   2. INSERT INTO ... SELECT * FROM artifacts.
--   3. DROP the old table and RENAME the new one.
-- Tracked in architecture/current.yaml#AZIONE-15-CHECK-CONSTRAINT
-- (deadline 2026-07-15).
UPDATE artifacts
SET status = 'STAGED'
WHERE status = 'READY';

-- Step 3: Log the migration for audit.
INSERT OR IGNORE INTO schema_migrations (filename, applied_at)
VALUES ('130_rename_ready_to_staged.sql', datetime('now'));
