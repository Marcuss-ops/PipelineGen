-- database: primary
-- Migration 115 — Add origin/provider columns to media_assets
-- (image-territories action plan, July 2026, FASE 1B).
--
-- Why this migration exists:
--
-- FASE 0 audit (docs/plans/image-territories-audit.md) observed that
-- media_assets conflates AI-generated and web-retrieved images under
-- a single source='image' discriminant, with 0 rows materialized with
-- a typed origin/provider attribution in this database. The fix is to
-- add two new first-class columns populated by the canonical typed
-- enums declared in `internal/domain/asset/image_taxonomy.go` (Step 1)
-- and `internal/domain/asset/image_enums.go` (FASE 1A):
--
--   origin   TEXT NOT NULL DEFAULT ''    -- ImageOrigin:
--                                            '' | retrieved | generated | uploaded
--   provider TEXT NOT NULL DEFAULT ''    -- ImageProvider:
--                                            '' | wikipedia | duckduckgo | searxng |
--                                            drive | google-slides | flux | nvidia |
--                                            upload | unknown
--
-- Both columns have DEFAULT '' for additive safety (existing rows are
-- marked as "unclassified" rather than blocking on a NOT NULL DEFAULT
-- non-empty constraint). The backfill step (FASE 4 step 4D) will
-- promote rows where source_image_url/google vids marker/etc. are
-- observable to their canonical origin/provider using the migration
-- 117 UPDATE statements documented in the audit.
--
-- Idempotence: SQLite `ALTER TABLE … ADD COLUMN` does NOT support
-- `IF NOT EXISTS`. Following the precedent of migration 109, this file
-- has the IF NOT EXISTS token dropped; the migration runner's
-- `isDuplicateColumnError` soft-skip handles the reapply case.
--
-- Index decision: A partial index on (origin) WHERE origin != ''
-- mirrors the convention introduced by migration 109 (which adds
-- idx_media_assets_discovered_via, idx_media_assets_monitored_source_id,
-- idx_media_assets_ext_discovered as partial indexes with WHERE
-- predicates). Empirically, queries of the shape
-- `WHERE source='image' AND origin='generated'` are the canonical
-- routing predicate (FASE 6 ImageSearchResolver); the partial index
-- keeps index size minimal by excluding the "unclassified" rows that
-- have origin='' until FASE 4 backfill promotes them.
--
-- We do NOT add an index on provider because (a) the canonical
-- search-routing key is origin not provider, (b) provider cardinality
-- is small (10 canonical values vs 3 origin values), and (c) the
-- selective ratio is high enough that a non-indexed scan is preferable
-- to maintaining an underutilised B-tree.

ALTER TABLE media_assets ADD COLUMN origin TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN provider TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_media_assets_origin
    ON media_assets(origin)
    WHERE origin != '';
