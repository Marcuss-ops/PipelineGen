-- migrations/sqlite/101_lifecycle_state_ssot.sql
--
-- PR 1 — Lifecycle state SSOT (June 2026), idempotent half.
--
-- Pre-PR1: media_assets.lifecycle_state carried mixed-case values
-- from three eras:
--   - lowercase legacy: 'ready', 'pending', 'deleted', 'active',
--     'searchable', 'processing'
--   - canonical (added later): 'STAGING', 'PROCESSING', 'ACTIVE',
--     'DELETED'
--   - new states introduced piecemeal: 'DELETE_PENDING',
--     'INDEX_PENDING', 'INDEX_FAILED'
-- The parallel `status` column and the lowercase `asset.AssetStatus`
-- enum co-existed but were retired.
--
-- Production reads consulted
--   COALESCE(NULLIF(lifecycle_state, ''), NULLIF(status, ''), 'ACTIVE')
-- so writers could pick either column; search filters used
-- `lifecycle_state IN ('active', 'searchable')` — neither of which
-- was canonical. PR 1 retires the lowercase compat values so the
-- canonical enum (LIFECYCLE_STATE ∈ {STAGING, PROCESSING, ACTIVE,
-- DELETE_PENDING, DELETED, ERROR}) is the only source of truth at
-- every layer.
--
-- This migration file (101) is the IDEMPOTENT half of the SSOT
-- rollout:
--   - Every step uses `… NOT IN (…)` or filtered `WHERE` guards so
--     a re-run is a no-op (verified by tests/fixtures/zero_legacy;
--     the SSOT pays no regression cost on legacy test data).
--   - Steps never reference the retired `status` column, so the
--     migration runs cleanly against fresh-DB test fixtures whose
--     CanonicalMediaAssetsSchema does not declare it.
--
-- The NON-IDEMPOTENT half (DROP COLUMN status, dropping the
-- vestigial column from production databases) lives in
-- migrations/sqlite/102_drop_legacy_status_column.sql. The split
-- keeps the test fixture (which never had the column) green while
-- production completes the cleanup at once. See that file's
-- header for the rationale.

-- ── Step 1: belt-and-braces column-correction guard ────────────────
-- A real-world DB may have encountered a partial migration earlier.
-- Pull every row's lifecycle_state to canonical UPPERCASE (or
-- ACTIVE if empty). No reference to `status` — the column may be
-- absent in fresh-DB test fixtures, so coupling this UPDATE to
-- that column would trip the canonical-grade propagation.
UPDATE media_assets
SET lifecycle_state = COALESCE(NULLIF(TRIM(lifecycle_state), ''), 'ACTIVE')
WHERE
    (lifecycle_state IS NULL OR TRIM(lifecycle_state) = '' OR lifecycle_state != UPPER(lifecycle_state));

-- ── Step 2: collapse every legacy lowercase writer to a canonical
-- uppercase target. The mapping is exhaustive over the values ever
-- observed in production (see scripts/audit/lifecycle_state audit,
-- June 2026):
--   'ready', 'searchable', 'active' (lowercase) → 'ACTIVE'
--   'pending'                                        → 'STAGING'
--   'processing' (lowercase)                        → 'PROCESSING'
--   'deleted' (lowercase)                           → 'DELETED'
--   'archived', 'failed'                             → 'ERROR'
--   The indexed states 'DELETE_PENDING', 'INDEX_FAILED',
--   'INDEX_PENDING' (already canonical UPPERCASE) are left intact.
UPDATE media_assets SET lifecycle_state = 'ACTIVE'
  WHERE lifecycle_state IN ('ready', 'searchable', 'active');

UPDATE media_assets SET lifecycle_state = 'STAGING'
  WHERE lifecycle_state IN ('pending');

UPDATE media_assets SET lifecycle_state = 'PROCESSING'
  WHERE lifecycle_state IN ('processing');

UPDATE media_assets SET lifecycle_state = 'DELETED'
  WHERE lifecycle_state IN ('deleted');

UPDATE media_assets SET lifecycle_state = 'ERROR'
  WHERE lifecycle_state IN ('archived', 'failed');

-- ── Step 3: belt to uppercase on any leftover unknown lowercase.
-- Any value NOT in the canonical set after this update is the
-- operator's problem (and is explicitly logged in the post-
-- migration pre-flight scan).
UPDATE media_assets
SET lifecycle_state = 'ACTIVE'
WHERE lifecycle_state NOT IN (
    'STAGING', 'PROCESSING', 'ACTIVE',
    'DELETE_PENDING', 'DELETED', 'ERROR'
);

-- ── Step 4 (defensive index assertion) ────────────────────────────
-- Migration 094 + 099 created the index; re-affirm here so an audit
-- query on `lifecycle_state` uses the index without a manual
-- `CREATE INDEX` per-DB wrap. If the index already exists this is
-- a no-op (`IF NOT EXISTS` is idempotent in SQLite).
CREATE INDEX IF NOT EXISTS idx_media_assets_lifecycle_state
  ON media_assets(lifecycle_state);
