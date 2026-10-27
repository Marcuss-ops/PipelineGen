-- database: primary
-- 286_media_mirror_fields_contract.sql
--
-- CONTRACT phase of the media mirror-field demolition (migration-museum audit
-- point 8, "media_assets mirror fields", and the digest history of audit
-- point 8's table). One PR, three mirror families, each with its decision
-- recorded in docs/migrations/BASELINE_INVENTORY.md:
--
-- ── 1. thumb_url vs thumbnail_url (the thumbnail pair) ───────────────────
--
-- `thumbnail_url` is the canonical column (model field ThumbnailURL; the PG
-- committer upserts it, and the PostgreSQL SSOT contract is
-- migrations/postgres/013_media_thumb_url_contract.sql). `thumb_url` was its
-- mirror — the one pair with a live write/read asymmetry (the writer targeted
-- the fallback column while readers preferred the canonical one). EXPAND is
-- long done (both columns have existed since 059/091); this is the CONTRACT:
--
--   1. BACKFILL — rows whose thumbnail_url is empty take the mirror value.
--   2. DROP    — thumb_url goes away; every Go reader resolves thumbnail_url
--      only (same-change code cutover, verified by go build ./... and the
--      package tests).
--
-- ── 2. the digest triple (file_hash ↔ content_sha256 ↔ binary_sha256) ───
--
-- `file_hash` is the pre-229 MIXED bucket (32-hex MD5 or 64-hex SHA-256).
-- 229 copied it to legacy_file_md5 and PROMISED a final DROP that never
-- landed; 281/008+010/011/012 then contracted the legacy_file_md5 half on
-- both planes. This file finishes the job on the residual `file_hash`
-- columns (the "survive on 8 tables" family of BASELINE_INVENTORY):
--
--   1. RECONCILE — a valid 64-hex value fills an empty canonical column
--      (media_assets.content_sha256 + its binary_sha256 fill-if-empty
--      projection, asset_index.content_hash, and content_sha256 on the
--      renamed tables). The digest is never fabricated: MD5-length and
--      non-hex values are NOT promoted, and a populated canonical column
--      always wins (same guard as 281).
--   2. DROP — media_assets, asset_index, asset_locations, asset_versions,
--      artlist_clips and entity_image_catalog_materializations lose the dead
--      column. `voiceovers.file_hash` and
--      `asset_subtitle_artifacts.file_hash` STAY: those are their own
--      artifact digests, actively written (voiceovers_repository,
--      subtitle_artifact_repository) — a mirror of nothing.
--
-- ── 3. search_terms ("tripla") — nothing to drop here ────────────────────
--
-- media_assets.search_terms was added three times by history (070/091/097),
-- which left its FORMAT ambiguous, and its codec was hand-copied three times
-- (app/wiring encodeStringSliceJSON, postgres/media clipTagsJSON +
-- decodeTagsJSON). Both triples were resolved in CODE, not in SQL: the single
-- owner is internal/kernel/asset/detail (EncodeStringArrayColumn /
-- ParseStringArrayColumn, contract test search_terms_test.go), and the wire
-- sites delegate to it. search_text and search_terms are two different facts
-- (full-text body vs JSON keyword array) and both stay — the audit records
-- that conflation explicitly, so no SQL cleanup applies.
--
-- CODE CUTOVER (same change): the PostgreSQL committer no longer writes the
-- thumb_url mirror (its upsert already writes thumbnail_url), and every Go
-- reader resolves thumbnail_url only (pgmedia media_repository, imagesrepo
-- search/aggregate, imagesregistry clips_repository).
--
-- IDEMPOTENCY: NOT idempotent. SQLite's `ALTER TABLE … DROP COLUMN` has no
-- `IF EXISTS` (precedent: 270_drop_entity_image_materialization_local_path,
-- 102, 230, 232). Apply ONCE, after the code that referenced the columns has
-- shipped (the commit that carries this file). The mirror columns are dropped
-- on the PRIMARY plane only: the cache-plane historical copies are outside
-- this contract (see BASELINE_INVENTORY), and the legacy_* quarantine copies
-- are destroyed wholesale by 287_legacy_quarantine_drop.sql.

-- ── 1. thumbnail pair ─────────────────────────────────────────────────────

UPDATE media_assets
   SET thumbnail_url = COALESCE(NULLIF(thumbnail_url, ''), NULLIF(thumb_url, ''), '')
 WHERE thumbnail_url = '' AND thumb_url <> '';

ALTER TABLE media_assets DROP COLUMN thumb_url;

-- ── 2. digest triple residue ──────────────────────────────────────────────

UPDATE media_assets
   SET content_sha256 = LOWER(file_hash),
       binary_sha256 = CASE
           WHEN binary_sha256 = '' THEN LOWER(file_hash)
           ELSE binary_sha256 END
 WHERE content_sha256 = '' AND length(file_hash) = 64
   AND file_hash NOT GLOB '*[^0-9A-Fa-f]*';

UPDATE asset_index SET content_hash = LOWER(file_hash)
 WHERE content_hash = '' AND length(file_hash) = 64
   AND file_hash NOT GLOB '*[^0-9A-Fa-f]*';

UPDATE asset_locations SET content_sha256 = LOWER(file_hash)
 WHERE content_sha256 = '' AND length(file_hash) = 64
   AND file_hash NOT GLOB '*[^0-9A-Fa-f]*';

UPDATE asset_versions SET content_sha256 = LOWER(file_hash)
 WHERE content_sha256 = '' AND length(file_hash) = 64
   AND file_hash NOT GLOB '*[^0-9A-Fa-f]*';

UPDATE artlist_clips SET content_sha256 = LOWER(file_hash)
 WHERE content_sha256 = '' AND length(file_hash) = 64
   AND file_hash NOT GLOB '*[^0-9A-Fa-f]*';

UPDATE entity_image_catalog_materializations SET content_sha256 = LOWER(file_hash)
 WHERE content_sha256 = '' AND length(file_hash) = 64
   AND file_hash NOT GLOB '*[^0-9A-Fa-f]*';

ALTER TABLE media_assets DROP COLUMN file_hash;
ALTER TABLE asset_index DROP COLUMN file_hash;
ALTER TABLE asset_locations DROP COLUMN file_hash;
ALTER TABLE asset_versions DROP COLUMN file_hash;
ALTER TABLE artlist_clips DROP COLUMN file_hash;
ALTER TABLE entity_image_catalog_materializations DROP COLUMN file_hash;

-- voiceovers.file_hash and asset_subtitle_artifacts.file_hash are DELIBERATELY
-- untouched: live artifact digests with active writers, not mirrors.
