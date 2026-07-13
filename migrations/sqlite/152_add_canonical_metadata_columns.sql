-- 152_add_canonical_metadata_columns.sql
--
-- Step 1 of PR-CATALOG-MULTILINGUA (Italian plan, July 2026).
-- Adds 13 canonical metadata columns to `media_assets` so the
-- multilingual clip catalog has a SINGLE row-of-truth surface
-- (identity, source, time, language, integrity, rights, lifecycle).
--
-- EXPAND-phase (godlike/06 SSOT — expand → backfill → cutover → contract):
--   - We ONLY ADD columns. No DROP, no RENAME.
--   - Existing column names (source, youtube_video_id, channel_id,
--     language, file_hash, license, lifecycle_state) remain intact
--     for godlike/07 minimum-blast-radius. A future cutover-phase
--     PR will turn off writers to the legacy columns and switch
--     readers; a contract-phase PR (out of scope here) will DROP them.
--
-- Rationale per column:
--   source_provider     := source (force NOT NULL with '' default)
--   source_video_id     := youtube_video_id (YouTube canonical).
--                          Stock/Artlist will populate via the app
--                          layer in Step 2 (out of scope here).
--   source_channel_id   := channel_id (NOT NULL with '' — was NOT
--                          NULL DEFAULT '' from migration 099).
--   source_url          := youtube_url
--   start_ms            := (start_time * 1000). start_time is TEXT
--                          (seconds, per migration 099); ms is
--                          INTEGER for arithmetic. Two coexisting
--                          surfaces is intentional (TEXT for API,
--                          INTEGER ms for plan-stage timing).
--   end_ms              := (end_time * 1000) — same rationale
--   original_language   := language
--   title               := name (may hold a machine-generated label;
--                          operator can polish via later PR)
--   binary_sha256       := file_hash IFF length(file_hash)=64 (defends
--                          against MD5 pollution — clip_atomic_writer
--                          falls back to MD5 in the empty-file case,
--                          so length() is the disambiguator).
--                          Rows with non-SHA-256 file_hash land at ''
--                          until re-processing lands proper SHA-256s.
--   semantic_hash       := '' (no upstream producer ships it yet;
--                          future visual-summary PR fills from
--                          asset_visual_summaries.source_hash).
--   rights_status       := license IFF license != '' (free-form
--                          legacy values flow through; app-layer
--                          typed enforcer normalizes going forward).
--                          Conservative default 'review_required'
--                          for any row without an existing license.
--   policy_version      := 'v1' (every row gets the canonical
--                          policy version; pre-existing per-row
--                          policy versions unknown).
--   lifecycle_status    := lifecycle_state (canonical rename target;
--                          legacy stays for godlike/07 minimum blast
--                          radius).
--
-- Backfill is idempotent: COALESCE(NULLIF(current, ''), source)
-- passes the existing value through unchanged once it's populated.
-- The WHERE clause is a per-row optimization — only rows with at
-- least one backfillable empty column get touched.
--
-- SQLite note: ADD COLUMN does NOT support `IF NOT EXISTS`. The
-- runner's isDuplicateColumnError soft-skip handles re-applying
-- (mirrors migration 109's header comment).
--
-- Idempotency: synthesizing all 13 ALTER + UPDATE + CREATE INDEX
-- once on a fresh DB is the canonical shape; re-applying on a DB
-- that already has the columns hits "duplicate column name" and
-- is soft-skipped, and the UPDATE is a no-op where values are
-- already populated.

ALTER TABLE media_assets ADD COLUMN source_provider   TEXT    NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN source_video_id   TEXT    NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN source_channel_id TEXT    NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN source_url        TEXT    NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN start_ms          INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media_assets ADD COLUMN end_ms            INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media_assets ADD COLUMN original_language TEXT    NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN title             TEXT    NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN binary_sha256     TEXT    NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN semantic_hash     TEXT    NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN rights_status     TEXT    NOT NULL DEFAULT 'review_required';
ALTER TABLE media_assets ADD COLUMN policy_version    TEXT    NOT NULL DEFAULT 'v1';
ALTER TABLE media_assets ADD COLUMN lifecycle_status  TEXT    NOT NULL DEFAULT 'ACTIVE';

-- Backfill: existing-column → new-canonical-column via COALESCE/NULLIF.
-- WHERE restricts the write to rows that have at least one backfillable
-- empty field, so re-application on a fully-populated DB is a no-op.
UPDATE media_assets SET
    source_provider   = COALESCE(NULLIF(source_provider, ''), source),
    source_video_id   = COALESCE(NULLIF(source_video_id, ''), youtube_video_id),
    source_channel_id = COALESCE(NULLIF(source_channel_id, ''), channel_id),
    source_url        = COALESCE(NULLIF(source_url, ''), youtube_url),
    -- start_time / end_time are TEXT (seconds); multiply by 1000 to ms.
    -- COALESCE()/CASE() guards keep rows already at start_ms=0 backfilled
    -- once; subsequent applications are a no-op because start_ms != 0.
    start_ms          = CASE
        WHEN start_ms = 0 AND NULLIF(start_time, '') IS NOT NULL
        THEN CAST((CAST(start_time AS REAL) * 1000) AS INTEGER)
        ELSE start_ms END,
    end_ms            = CASE
        WHEN end_ms = 0 AND NULLIF(end_time, '') IS NOT NULL
        THEN CAST((CAST(end_time AS REAL) * 1000) AS INTEGER)
        ELSE end_ms END,
    original_language = COALESCE(NULLIF(original_language, ''), language),
    title             = COALESCE(NULLIF(title, ''), name),
    -- Length-64 check defends against MD5 pollution: pre-Step-1
    -- clip_atomic_writer falls back to MD5(file_hash) for empty files;
    -- those rows stay at binary_sha256='' until re-processing lands
    -- a real SHA-256.
    binary_sha256     = CASE
        WHEN binary_sha256 = '' AND file_hash != '' AND length(file_hash) = 64
        THEN file_hash
        ELSE binary_sha256 END,
    -- semantic_hash is not yet producer-shipped — every row stays at ''.
    -- Placeholder line kept to make the canonical column set explicit
    -- against godlike/06 SSOT (one canonical owner per fact).
    -- semantic_hash     : no-op backfill,
    -- Bridge from the free-form `license` to rights_status: copy if
    -- license is non-empty AND rights_status is still at the default
    -- sentinel ('review_required'); otherwise preserve the existing
    -- value (so an operator-applied 'blocked' is never overwritten).
    rights_status     = CASE
        WHEN rights_status = 'review_required' AND COALESCE(NULLIF(license, ''), '') != ''
        THEN license
        ELSE rights_status END,
    -- Every row gets the canonical policy_version='v1'; historical
    -- per-row version codes were never emitted (the column DEFAULT
    -- alone sets this).
    -- lifecycle_status  := lifecycle_state (last legacy-to-canonical bridge).
    lifecycle_status  = COALESCE(NULLIF(lifecycle_status, ''), lifecycle_state)
WHERE
       (source_provider   = ''                                             AND source             != '')
    OR (source_video_id   = ''                                             AND youtube_video_id   != '')
    OR (source_channel_id = ''                                             AND channel_id         != '')
    OR (source_url        = ''                                             AND youtube_url        != '')
    OR (start_ms = 0                                                       AND start_time         != '')
    OR (end_ms   = 0                                                       AND end_time           != '')
    OR (original_language = ''                                             AND language           != '')
    OR (title             = ''                                             AND name               != '')
    OR (binary_sha256     = ''                                             AND file_hash          != ''  AND length(file_hash) = 64)
    OR (rights_status     = 'review_required'                              AND license            != '')
    OR (lifecycle_status  = ''                                             AND lifecycle_state    != '')
;

-- Canonical existence-check index (skip rows at default sentinel).
-- Mirrors the patterns of migrations 094/096/099/109: partial indexes
-- keep the on-disk footprint tight by ignoring rows where the new
-- column is at its empty-string default.
CREATE INDEX IF NOT EXISTS idx_media_assets_canonical_source
    ON media_assets (source_provider, source_video_id, source_channel_id)
    WHERE source_provider != '';
