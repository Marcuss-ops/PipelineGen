-- 156_text_track_spec_columns.sql
--
-- PR-CATALOG-MULTILINGUA step 2 gap-fill (Italian plan, July 2026).
-- User spec for the canonical text-track surface requires columns
-- that are NOT yet present on asset_text_tracks and
-- asset_text_track_segments:
--
--   asset_text_tracks gaps:
--     - source_track_id   (FK to this same table; audit-trail
--                         link from a translation back to its
--                         source-language track; ON DELETE SET NULL
--                         so the audit link survives deletion of the
--                         source row)
--     - source_text_hash  (TEXT NOT NULL DEFAULT ''; the SHA-256 of
--                         the source text — duplicates the hash that
--                         was already used to compute translation_key
--                         in 155, but persisted at the row level so
--                         future agents can read it without
--                         re-deriving from a parent query)
--
--   asset_text_track_segments gaps:
--     - text_hash         (TEXT NOT NULL DEFAULT ''; segment-level
--                          dedup fingerprint, same invariant as
--                          asset_text_tracks.text_hash)
--
-- The user's spec also requires a CHECK constraint locking
-- text_kind to the 5-element enum {transcript, description,
-- visual_summary, short_summary, search_document}. Existing
-- rows in migration 137 carry text_kind values listed in the
-- 137 header comment ("summary", "title", "keywords") that
-- would fail the strict CHECK — recreating the table to add
-- the constraint would crash any deployment with seeded data.
-- Per the thinker's recommendation, the strict CHECK lands in
-- a SEPARATE migration (157 in step 7) once a data-backfill
-- migration has remapped legacy values to the 5-element enum.
-- godlike/06 SSOT: the constraint is the SSOT of the
-- enumeration; until 157 lands, the column accepts legacy plus
-- new values, and future agents must NOT add values outside the
-- 5-element set in new code paths (it would lock 157 into a
-- data-backfill).
--
-- ─── Additive design rationale (godlike/06 SSOT discipline) ────
-- Every new column is:
--   - nullable (source_track_id) OR DEFAULT '' (text/source_text)
--   - foreign-keyed (source_track_id → asset_text_tracks.id) so
--     orphans are impossible at the SQL boundary
--   - ON DELETE SET NULL so bulk re-acquisition / backfill can
--     remove the source track without leaving dangling FKs
--   - covered by an index (segments.text_hash) for the future
--     "skip-WAV if a duplicate segment already exists" fast
--     path the resolver plans to add
--
-- No table recreation is required because SQLite supports
-- ALTER TABLE ADD COLUMN with FK constraints and DEFAULT values
-- since 3.6.19 (the project's SQLite is 3.45+ via mattn/go-sqlite3).
--
-- ─── Pre-migration data ────────────────────────────────────────
-- Existing rows are untouched. source_track_id is NULL for
-- pre-migration rows (no parent track reference persisted).
-- source_text_hash is '' for pre-migration rows (the parent
-- text_hash was the only fingerprint stored; the application
-- layer can back-fill it via a one-time job in step 8 if
-- needed). segments.text_hash is '' for pre-migration rows
-- (the segments table was created at 144 without the hash;
-- a back-fill pass would compute it from the segment text).
-- All three are non-fatal for the lookup gates; future agents
-- treat '' as "no hash available, fall through to full-text
-- comparison".
--
-- ─── Forward-prevention note ──────────────────────────────────
-- A future agent that wants to add source_track_id strings
-- (e.g. "asset_text_tracks.v2") is the wrong path; the column
-- is the canonical FK link. A future agent that wants to
-- rename source_text_hash to source_content_hash is a typo;
-- source_text_hash is the persisted fingerPRINT, not the
-- persisted text (text_content is the text). The
-- code-reviewer-minimax-m3 fleet is the canonical detector
-- for both missteps.

-- ─── asset_text_tracks New Columns ─────────────────────────────

-- source_track_id: FK to this same table. When this row IS
-- a source-language track (e.g. a whisper EN transcript),
-- source_track_id is NULL. When this row is a translation
-- (e.g. IT translation of an EN transcript), source_track_id
-- points to the EN row id. ON DELETE SET NULL so audit links
-- survive bulk-redeletion of source rows; future
-- backfill/audit queries that need the link can still see it
-- on surviving children.
ALTER TABLE asset_text_tracks
    ADD COLUMN source_track_id INTEGER
    REFERENCES asset_text_tracks(id) ON DELETE SET NULL;

-- source_text_hash: persisted SHA-256 of the source text. The
-- lookup-before-translate gate (migration 155) computes the
-- translation_key from this hash; persisting it on the row
-- means future agents can read the source hash directly
-- without joining to a parent row. DEFAULT '' makes the
-- migration trivially additive (existing rows get '' for
-- now and a back-fill job can populate them).
ALTER TABLE asset_text_tracks
    ADD COLUMN source_text_hash TEXT NOT NULL DEFAULT '';

-- ─── asset_text_track_segments New Column + Index ─────────────

-- text_hash: per-segment SHA-256. Same invariant as
-- asset_text_tracks.text_hash (dedup, change detection,
-- source_version inclusion). Two adjacent segments MAY
-- legitimately share text (silent cue, repeated music cue)
-- so no UNIQUE constraint here. DEFAULT '' for back-fill
-- compatibility.
ALTER TABLE asset_text_track_segments
    ADD COLUMN text_hash TEXT NOT NULL DEFAULT '';

-- Covering index for the future resolver fast-path "skip if
-- a segment with this exact text_hash already exists in the
-- track". Non-unique (no cardinality-pinning guarantee).
CREATE INDEX IF NOT EXISTS idx_asset_text_track_segments_hash
    ON asset_text_track_segments (text_hash);
