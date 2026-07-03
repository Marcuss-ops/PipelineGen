-- database: primary
-- Migration 128 — Corrective: align origin/provider indexes with FASE 1B design
-- (Task 9 fix, July 2026).
--
-- CONTEXT (see migration 115 for the original column additions):
--
-- Migration 115 (`115_add_image_origin_provider.sql`) added origin and
-- provider columns with full indexes (`idx_media_assets_origin`,
-- `idx_media_assets_provider`) and a backfill (`provider = 'unknown'`).
--
-- A later design revision (which landed as the now-removed duplicate
-- migration 126) changed the intent to:
--   1. A partial index on origin: `WHERE origin != ''` (excludes
--      unclassified rows, matches pattern from migration 109).
--   2. No index on provider (low cardinality, 10 canonical values).
--   3. Origin DEFAULT '' instead of 'retrieved' (additive safety).
--
-- This corrective migration applies those adjustments in a way that
-- is safe against both (a) fresh DBs where only 115 ran and (b) DBs
-- where both 115 and the old 126 ran. All DDL operations use
-- IF EXISTS / IF NOT EXISTS guards so the migration runner can
-- re-apply this file without errors.
--
-- The origin DEFAULT is not ALTERed here because SQLite does not
-- support ALTER TABLE ... ALTER COLUMN ... SET DEFAULT. New rows
-- inserted without an explicit origin AFTER this migration runs
-- will still inherit DEFAULT 'retrieved' from 115. The fix for
-- that is the application-layer ImageOrigin resolver (which reads
-- origin at ingest time and never relies on the column default for
-- classification). This is documented here so future operators
-- reading the migration ledger can trace the decision.

-- 1. Drop the redundant provider index (migration 115 created it;
--    the design revision intentionally omits it — see 126 rationale).
DROP INDEX IF EXISTS idx_media_assets_provider;

-- 2. Drop the full origin index and recreate as partial.
--    The old index covers ALL rows including origin='retrieved';
--    the partial index excludes '' rows (unclassified) to match
--    the pattern established by migration 109.
DROP INDEX IF EXISTS idx_media_assets_origin;

CREATE INDEX IF NOT EXISTS idx_media_assets_origin
    ON media_assets(origin)
    WHERE origin != '';
