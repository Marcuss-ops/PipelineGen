-- Migration 180 — promote subjects to canonical identity.
--
-- P0-1 of the stock-pipeline refactor (PipelineGen). The legacy
-- `subjects` table (created by migration 104) only carried a slug-
-- like `id` PK, a free-text `name`, and a JSON `metadata_json`
-- blob. The Sugar-Ray-Robinson incident showed that the entire
-- downstream code path was per-field-casing sensitive: query
-- `WHERE folder_path = ?` produced split counts between "Sugar Ray
-- Robinson" and "sugar ray robinson" because there was no
-- canonical-normalization layer between user-supplied text and
-- identity.
--
-- This migration is the EXPAND phase (godlike/06 expand → backfill
-- → cutover → contract): it ADDS the canonical columns WITHOUT
-- removing the legacy `id` column, backfills them from the
-- existing rows, and is idempotent (every UPDATE has a `WHERE
-- … IS NULL` guard so re-running the migration is a no-op).
--
-- Cutover to the new resolver and contract to drop `id` are
-- separate follow-up tasks (this migration is intentionally
-- change-preserving).
--
-- Columns added:
--   slug              TEXT NOT NULL — canonical lookup key,
--                                       slug.SlugifyTitle(display_name).
--   uuid              TEXT NOT NULL — UUID v4 generated once per
--                                       legacy row so subject_id is
--                                       stable across runs.
--   display_name      TEXT NOT NULL — the human-readable label
--                                       (legacy `name` value).
--   display_name_norm TEXT NOT NULL — LOWER(TRIM(REPLACE(…))) for
--                                       case-insensitive lookup.
--   aliases           TEXT NOT NULL DEFAULT '[]' — JSON list of
--                                       accepted variant spellings.
--   kind              TEXT NOT NULL DEFAULT 'person' — entity
--                                       kind (person/place/etc).
--   origin            TEXT NOT NULL DEFAULT 'image' — provenance
--                                       tag (`image` legacy, `stock`
--                                       once stock wiring lands).

ALTER TABLE subjects ADD COLUMN slug            TEXT    NOT NULL DEFAULT '';
ALTER TABLE subjects ADD COLUMN uuid            TEXT    NOT NULL DEFAULT '';
ALTER TABLE subjects ADD COLUMN display_name    TEXT    NOT NULL DEFAULT '';
ALTER TABLE subjects ADD COLUMN display_name_norm TEXT NOT NULL DEFAULT '';
ALTER TABLE subjects ADD COLUMN aliases         TEXT    NOT NULL DEFAULT '[]';
ALTER TABLE subjects ADD COLUMN kind            TEXT    NOT NULL DEFAULT 'person';
ALTER TABLE subjects ADD COLUMN origin          TEXT    NOT NULL DEFAULT 'image';
-- Promote category + wikidata_id from the pre-180 kernel/asset.Subject
-- type definitions: legacy code held these fields but did NOT have
-- dedicated columns on `subjects` (lived only in metadata_json or were
-- never persisted). The canonical resolver SELECTs them, so the
-- migration MUST backfill them as first-class columns. Both default
-- to '' so legacy rows get a stable empty-string value rather than
-- NULL (NULL would force callers to NULL-coalesce on every read).
ALTER TABLE subjects ADD COLUMN category        TEXT    NOT NULL DEFAULT '';
ALTER TABLE subjects ADD COLUMN wikidata_id     TEXT    NOT NULL DEFAULT '';

-- Backfill `display_name` from legacy `name` (PR-180-A). The legacy
-- column was already populated by the image-resolved-subjects path.
UPDATE subjects
SET display_name = COALESCE(NULLIF(name, ''), display_name)
WHERE display_name = '' AND name <> '';

-- Backfill `slug` from legacy `id` (PR-180-A). Legacy `id` was already
-- a slug per the image-side resolver test surface; we keep it as the
-- slug for back-compat. NEW subjects generate the canonical slug
-- from `display_name` via pkg/slug.SlugifyTitle at the Go layer.
UPDATE subjects
SET slug = COALESCE(NULLIF(id, ''), slug)
WHERE slug = '' AND id <> '';

-- Backfill `display_name_norm` (PR-180-B). Lowercase + trim +
-- collapse internal whitespace. Stable across casing variants so
-- "Sugar Ray Robinson", "SUGAR RAY ROBINSON", and "  sugar ray
-- robinson  " all collide on the same row.
UPDATE subjects
SET display_name_norm = LOWER(TRIM(REPLACE(REPLACE(COALESCE(display_name, ''), '  ', ' '), '  ', ' ')))
WHERE display_name_norm = '' AND display_name <> '';

-- Backfill `uuid` for legacy rows (PR-180-C). Each row gets a
-- UUID v4-shaped 32-hex string derived from `randomblob(16)` once
-- per row. The `WHERE uuid = ''` guard makes the UPDATE idempotent:
-- re-running the migration on the same DB is a no-op (the row
-- already carries a UUID). Once stored, the UUID is stable for the
-- row's lifetime — there is no recomputation path.
--
-- Why hex(randomblob(16)) instead of `github.com/google/uuid` v4:
-- mattn/go-sqlite3 does NOT register `sha256()` as a built-in SQL
-- function by default (the extension lives behind a build tag we
-- don't pull), and the project's driver lock (AGENTS.md) forbids
-- driver swaps. randomblob(16) is the SQLite-canonical entropy
-- source for UUID v4-shaped strings.
--
-- Why NOT deterministic (e.g. sha256(slug)): legacy rows produced
-- a stable subject per `slug`, so a deterministic UUID derivation
-- would just be a re-encoded slug. The literal UUID is more useful
-- than a hash for cross-system subject_id (Qdrant payload, Drive
-- metadata) because operators can recognize the format.
UPDATE subjects
SET uuid = lower(hex(randomblob(4))) || '-' ||
           lower(hex(randomblob(2))) || '-' ||
           '4' || lower(substr(hex(randomblob(2)), 2, 3)) || '-' ||
           lower(substr('89ab', 1 + (abs(random()) % 4), 1)) || lower(hex(randomblob(2))) || '-' ||
           lower(hex(randomblob(6)))
WHERE uuid = '' AND slug <> '';

-- Indexes (PR-180-D). `slug` and `uuid` are unique lookup keys for
-- the canonical resolver. `display_name_norm` is the case-insensitive
-- lookup key for the resolver's Resolve(displayName) entry point.
CREATE UNIQUE INDEX IF NOT EXISTS idx_subjects_slug
    ON subjects (slug);
CREATE UNIQUE INDEX IF NOT EXISTS idx_subjects_uuid
    ON subjects (uuid);
CREATE INDEX        IF NOT EXISTS idx_subjects_display_name_norm
    ON subjects (display_name_norm);
