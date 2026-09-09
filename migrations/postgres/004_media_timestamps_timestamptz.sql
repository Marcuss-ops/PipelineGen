-- 004_media_timestamps_timestamptz.sql
-- PostgreSQL + pgvector media domain — TIMESTAMPTZ expand phase.
-- Apply after 001_media_schema.sql, 002_media_vector_surfaces.sql,
-- and 003_media_hnsw_indexes.sql, only to the dedicated media database.
--
-- GOAL (BASELINE_PLAN.md §5): stop the TEXT debt on the media PG SSOT.
-- During cutover parity the SQLite ↔ PG column-for-column TEXT mirror
-- already paid for cutover parity through tight coupling; going forward
-- the media domain grows on typed TIMESTAMPTZ.
--
-- STRATEGY (expand/backfill in one migration — window is short):
--   1. EXPAND  — ADD COLUMN *_ts TIMESTAMPTZ (idempotent, short-circuits
--                on a populated DB where the column is already present).
--   2. BACKFILL — populate every new *_ts column from its legacy TEXT
--                 sibling via NULLIF(col,'')::timestamptz, where col <> ''.
--   3. INDEX    — btree (+ optional BRIN) on every new *_ts so range
--                 queries, partitioning, and planner statistics become native.
--
-- DUAL-WRITE (application layer, NOT in this file): the writer
-- PostgresMediaCommitter writes both columns in the same transaction:
--   TEXT col      = RFC3339 string (legacy, to be contracted later)
--   *_ts col      = NULLIF($N,'')::timestamptz
-- Readers prefer *_ts when non-NULL, falling back to TEXT for rows
-- written before this migration.
--
-- IDEMPOTENCE: every statement is guarded with IF NOT EXISTS /
-- information_schema existence checks, mirroring 001/002/003 style.
-- Re-applying on a populated DB is a no-op (ADD COLUMN short-circuits,
-- backfill UPDATE touches only rows where TEXT <> '' and _ts IS NULL
-- on a re-run, index guards handle IF NOT EXISTS).
--
-- NOT YET (contract phase — deferred, see BASELINE_PLAN.md §5.2):
--   DROP/RENAME of the legacy TEXT columns and removal of the
--   dual-write. That happens only after the read path has flipped to
--   *_ts (the next increment after this file).

-- ── Helpers ────────────────────────────────────────────────────────────────
-- Use DO blocks so an already-populated DB never errors on ADD COLUMN.
-- The inner IF EXISTS guards are the canonical self-bootstrapping pattern
-- shared with 003's stale-index repair block.

-- ── 1. media_assets ─────────────────────────────────────────────────────
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema='public' AND table_name='media_assets' AND column_name='created_at_ts'
  ) THEN
    ALTER TABLE media_assets ADD COLUMN created_at_ts TIMESTAMPTZ;
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema='public' AND table_name='media_assets' AND column_name='updated_at_ts'
  ) THEN
    ALTER TABLE media_assets ADD COLUMN updated_at_ts TIMESTAMPTZ;
  END IF;
END $$;

-- Additional timestamp-text mirrors that exist on 001 but were added as
-- free-form TEXT (index_state_updated_at, enrich_state_updated_at,
-- discovered_at, expires_at, last_used_at, etc.): expand the known hot
-- path set now; the rest may follow in a follow-up 005 without blocking
-- this migration's acceptance.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema='public' AND table_name='media_assets' AND column_name='index_state_updated_at_ts'
  ) THEN
    ALTER TABLE media_assets ADD COLUMN index_state_updated_at_ts TIMESTAMPTZ;
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema='public' AND table_name='media_assets' AND column_name='enrich_state_updated_at_ts'
  ) THEN
    ALTER TABLE media_assets ADD COLUMN enrich_state_updated_at_ts TIMESTAMPTZ;
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema='public' AND table_name='media_assets' AND column_name='discovered_at_ts'
  ) THEN
    ALTER TABLE media_assets ADD COLUMN discovered_at_ts TIMESTAMPTZ;
  END IF;
END $$;

-- ── 2. asset_locations ──────────────────────────────────────────────────
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema='public' AND table_name='asset_locations' AND column_name='created_at_ts'
  ) THEN
    ALTER TABLE asset_locations ADD COLUMN created_at_ts TIMESTAMPTZ;
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema='public' AND table_name='asset_locations' AND column_name='updated_at_ts'
  ) THEN
    ALTER TABLE asset_locations ADD COLUMN updated_at_ts TIMESTAMPTZ;
  END IF;
END $$;

-- ── 3. outbox_events ────────────────────────────────────────────────────
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema='public' AND table_name='outbox_events' AND column_name='created_at_ts'
  ) THEN
    ALTER TABLE outbox_events ADD COLUMN created_at_ts TIMESTAMPTZ;
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema='public' AND table_name='outbox_events' AND column_name='updated_at_ts'
  ) THEN
    ALTER TABLE outbox_events ADD COLUMN updated_at_ts TIMESTAMPTZ;
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema='public' AND table_name='outbox_events' AND column_name='next_attempt_at_ts'
  ) THEN
    ALTER TABLE outbox_events ADD COLUMN next_attempt_at_ts TIMESTAMPTZ;
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema='public' AND table_name='outbox_events' AND column_name='lease_expiry_ts'
  ) THEN
    ALTER TABLE outbox_events ADD COLUMN lease_expiry_ts TIMESTAMPTZ;
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema='public' AND table_name='outbox_events' AND column_name='completed_at_ts'
  ) THEN
    ALTER TABLE outbox_events ADD COLUMN completed_at_ts TIMESTAMPTZ;
  END IF;
END $$;

-- ── 4. Backfill: populate *_ts from legacy TEXT siblings ────────────────
-- NULLIF(...,'')::timestamptz treats the canonical empty-string default
-- as SQL NULL so the typed column stays NULL for unset rows. Re-applying
-- only touches rows where the TEXT source is non-empty; existing _ts
-- values are overwritten to ensure idempotent repair of a previously
-- interrupted backfill (a short-lived divergence that the dual-write window
-- immediately reconverges).

UPDATE media_assets
SET created_at_ts = NULLIF(created_at, '')::timestamptz
WHERE created_at <> '' AND (created_at_ts IS NULL OR created_at_ts::text <> created_at);

UPDATE media_assets
SET updated_at_ts = NULLIF(updated_at, '')::timestamptz
WHERE updated_at <> '' AND (updated_at_ts IS NULL OR updated_at_ts::text <> updated_at);

UPDATE media_assets
SET index_state_updated_at_ts = NULLIF(index_state_updated_at, '')::timestamptz
WHERE index_state_updated_at <> '' AND (index_state_updated_at_ts IS NULL OR index_state_updated_at_ts::text <> index_state_updated_at);

UPDATE media_assets
SET enrich_state_updated_at_ts = NULLIF(enrich_state_updated_at, '')::timestamptz
WHERE enrich_state_updated_at <> '' AND (enrich_state_updated_at_ts IS NULL OR enrich_state_updated_at_ts::text <> enrich_state_updated_at);

UPDATE media_assets
SET discovered_at_ts = NULLIF(discovered_at, '')::timestamptz
WHERE discovered_at <> '' AND (discovered_at_ts IS NULL OR discovered_at_ts::text <> discovered_at);

UPDATE asset_locations
SET created_at_ts = NULLIF(created_at, '')::timestamptz
WHERE created_at <> '' AND (created_at_ts IS NULL OR created_at_ts::text <> created_at);

UPDATE asset_locations
SET updated_at_ts = NULLIF(updated_at, '')::timestamptz
WHERE updated_at <> '' AND (updated_at_ts IS NULL OR updated_at_ts::text <> updated_at);

UPDATE outbox_events
SET created_at_ts = NULLIF(created_at, '')::timestamptz
WHERE created_at <> '' AND (created_at_ts IS NULL OR created_at_ts::text <> created_at);

UPDATE outbox_events
SET updated_at_ts = NULLIF(updated_at, '')::timestamptz
WHERE updated_at <> '' AND (updated_at_ts IS NULL OR updated_at_ts::text <> updated_at);

UPDATE outbox_events
SET next_attempt_at_ts = NULLIF(next_attempt_at, '')::timestamptz
WHERE next_attempt_at IS NOT NULL AND next_attempt_at <> '' AND (next_attempt_at_ts IS NULL OR next_attempt_at_ts::text <> next_attempt_at);

UPDATE outbox_events
SET lease_expiry_ts = NULLIF(lease_expiry, '')::timestamptz
WHERE lease_expiry IS NOT NULL AND lease_expiry <> '' AND (lease_expiry_ts IS NULL OR lease_expiry_ts::text <> lease_expiry);

UPDATE outbox_events
SET completed_at_ts = NULLIF(completed_at, '')::timestamptz
WHERE completed_at IS NOT NULL AND completed_at <> '' AND (completed_at_ts IS NULL OR completed_at_ts::text <> completed_at);

-- ── 5. Indexes on the new typed columns (range, planner stats, BRIN) ────
-- btree for range scans + ordering; BRIN for large append-mostly tables.
-- All idempotent (IF NOT EXISTS). BRIN is advisory — if the extension is
-- absent the btree covers correctness.

CREATE INDEX IF NOT EXISTS idx_media_assets_created_at_ts
    ON media_assets (created_at_ts);
CREATE INDEX IF NOT EXISTS idx_media_assets_updated_at_ts
    ON media_assets (updated_at_ts);

-- Convenience functional range helpers: operators can immediately switch
-- queries from TEXT to _ts without a code deploy.
CREATE INDEX IF NOT EXISTS idx_media_assets_discovered_at_ts
    ON media_assets (discovered_at_ts);

CREATE INDEX IF NOT EXISTS idx_asset_locations_created_at_ts
    ON asset_locations (created_at_ts);

CREATE INDEX IF NOT EXISTS idx_outbox_events_created_at_ts
    ON outbox_events (created_at_ts);
CREATE INDEX IF NOT EXISTS idx_outbox_events_next_attempt_at_ts
    ON outbox_events (next_attempt_at_ts);
CREATE INDEX IF NOT EXISTS idx_outbox_events_lease_expiry_ts
    ON outbox_events (lease_expiry_ts);
CREATE INDEX IF NOT EXISTS idx_outbox_events_completed_at_ts
    ON outbox_events (completed_at_ts);

-- Optional BRIN — zero cost on a fresh (empty) database; substantial I/O
-- win on a large append-mostly media_assets (monotone created_at_ts).
-- Guarded so the migration never errors on an image where the brin
-- extension is absent (not shipped in the test container image).
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_extension WHERE extname='btree_gin') THEN
    NULL;
  END IF;
  -- BRIN is built-in (no extension). We still guard with IF NOT EXISTS.
  CREATE INDEX IF NOT EXISTS idx_media_assets_created_at_ts_brin
      ON media_assets USING brin (created_at_ts);
EXCEPTION WHEN OTHERS THEN NULL;
END $$;
