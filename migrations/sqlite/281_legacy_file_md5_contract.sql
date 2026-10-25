-- database: primary
-- 281_legacy_file_md5_contract.sql
--
-- CONTRACT phase of 229_rename_file_hash_to_legacy_file_md5.sql (expand →
-- backfill → cutover → contract). 229 kept the mixed 32/64-hex bucket alive
-- "for the minimum-blast-radius window" and promised that "a future
-- contract-phase PR will DROP the old columns". This is that PR's data half;
-- the Go half removes every read and write of the bucket in the same change.
--
-- Demolition rule (godlike/06, one owner per fact): a digest is a content
-- address only when it IS a SHA-256 (64 hex). Everything else held in
-- `legacy_file_md5` is a legacy record with no reconcilable identity.
--
--   1. RECONCILE — a valid 64-hex legacy value fills an empty canonical
--      column (media_assets.content_sha256 + its binary_sha256 projection,
--      asset_index.content_hash, and the Asset metadata_json.content_hash
--      projection). When both are set the canonical column wins: it is the
--      verified fact, the legacy bucket was "never a decision input"
--      (media-identity gate). The binary_sha256 projection mirrors migration
--      152's fill-if-empty rule and supersedes the demolished runtime
--      BackfillContentSHA256, which owned this projection before the bucket
--      it read from was dropped.
--   2. FORCED REMOVAL (operator-approved 2026-10-24) — catalog rows whose
--      digest is not a valid SHA-256 are deleted. An entity-image binding
--      that cannot name its bytes is exactly the record class behind the
--      content-address HTTP 422s (TICKET-OVERLAY-THROUGHPUT-100-MIN runs
--      v3–v5). Historical MD5-only candidates are knowingly lost.
--   3. RENAME — tables without a canonical content column take
--      `content_sha256` as the honest name of the surviving values.
--   4. DROP — media_assets / asset_index lose the legacy column entirely.
--
-- IDEMPOTENCY: NOT idempotent (DROP/RENAME COLUMN have no IF EXISTS).
-- Applied once by the migration runner, after 280.

-- ── 1. reconcile ────────────────────────────────────────────────────────

UPDATE media_assets
   SET content_sha256 = LOWER(legacy_file_md5),
       binary_sha256 = CASE
           WHEN binary_sha256 = '' THEN LOWER(legacy_file_md5)
           ELSE binary_sha256 END
 WHERE content_sha256 = '' AND length(legacy_file_md5) = 64
   AND legacy_file_md5 NOT GLOB '*[^0-9A-Fa-f]*';

UPDATE asset_index SET content_hash = LOWER(legacy_file_md5)
 WHERE content_hash = '' AND length(legacy_file_md5) = 64
   AND legacy_file_md5 NOT GLOB '*[^0-9A-Fa-f]*';

-- Asset metadata plane: the accessor read the key "legacy_file_md5"; after
-- this migration the only content-address key left in metadata is
-- "content_hash", so the value moves before the key disappears.
UPDATE media_assets
   SET metadata_json = json_set(metadata_json, '$.content_hash',
                                LOWER(json_extract(metadata_json, '$.legacy_file_md5')))
 WHERE json_extract(metadata_json, '$.legacy_file_md5') IS NOT NULL
   AND length(json_extract(metadata_json, '$.legacy_file_md5')) = 64
   AND json_extract(metadata_json, '$.legacy_file_md5') NOT GLOB '*[^0-9A-Fa-f]*'
   AND COALESCE(json_extract(metadata_json, '$.content_hash'), '') = '';

UPDATE media_assets
   SET metadata_json = json_remove(metadata_json, '$.legacy_file_md5')
 WHERE json_extract(metadata_json, '$.legacy_file_md5') IS NOT NULL;

-- `file_hash` is the SAME legacy bucket under its older alias (229: "the
-- continuation of the same legacy bucket"). It gets the identical treatment:
-- promote a valid SHA-256 into content_hash when absent, then remove the key.
UPDATE media_assets
   SET metadata_json = json_set(metadata_json, '$.content_hash',
                                LOWER(json_extract(metadata_json, '$.file_hash')))
 WHERE json_extract(metadata_json, '$.file_hash') IS NOT NULL
   AND length(json_extract(metadata_json, '$.file_hash')) = 64
   AND json_extract(metadata_json, '$.file_hash') NOT GLOB '*[^0-9A-Fa-f]*'
   AND COALESCE(json_extract(metadata_json, '$.content_hash'), '') = '';

UPDATE media_assets
   SET metadata_json = json_remove(metadata_json, '$.file_hash')
 WHERE json_extract(metadata_json, '$.file_hash') IS NOT NULL;

-- ── 2. forced removal / value removal ───────────────────────────────────

-- A materialized binding without a valid SHA-256 cannot name its bytes.
DELETE FROM entity_image_catalog_materializations
 WHERE status = 'materialized'
   AND NOT (length(legacy_file_md5) = 64 AND legacy_file_md5 NOT GLOB '*[^0-9A-Fa-f]*');

-- Remaining catalog rows (any status) carrying a non-SHA digest: the record
-- itself is legacy and cannot be reconciled — removed, not repaired.
DELETE FROM entity_image_catalog_candidates
 WHERE legacy_file_md5 != ''
   AND NOT (length(legacy_file_md5) = 64 AND legacy_file_md5 NOT GLOB '*[^0-9A-Fa-f]*');

DELETE FROM entity_image_catalog_materializations
 WHERE legacy_file_md5 != ''
   AND NOT (length(legacy_file_md5) = 64 AND legacy_file_md5 NOT GLOB '*[^0-9A-Fa-f]*');

-- Non-catalog rows keep existing; only the non-reconcilable value goes.
UPDATE asset_locations SET legacy_file_md5 = ''
 WHERE legacy_file_md5 != ''
   AND NOT (length(legacy_file_md5) = 64 AND legacy_file_md5 NOT GLOB '*[^0-9A-Fa-f]*');

UPDATE asset_versions SET legacy_file_md5 = ''
 WHERE legacy_file_md5 != ''
   AND NOT (length(legacy_file_md5) = 64 AND legacy_file_md5 NOT GLOB '*[^0-9A-Fa-f]*');

UPDATE asset_subtitle_artifacts SET legacy_file_md5 = ''
 WHERE legacy_file_md5 != ''
   AND NOT (length(legacy_file_md5) = 64 AND legacy_file_md5 NOT GLOB '*[^0-9A-Fa-f]*');

UPDATE artlist_clips SET legacy_file_md5 = ''
 WHERE legacy_file_md5 != ''
   AND NOT (length(legacy_file_md5) = 64 AND legacy_file_md5 NOT GLOB '*[^0-9A-Fa-f]*');

-- ── 3. rename ───────────────────────────────────────────────────────────

ALTER TABLE asset_locations RENAME COLUMN legacy_file_md5 TO content_sha256;
ALTER TABLE asset_versions RENAME COLUMN legacy_file_md5 TO content_sha256;
ALTER TABLE asset_subtitle_artifacts RENAME COLUMN legacy_file_md5 TO content_sha256;
ALTER TABLE artlist_clips RENAME COLUMN legacy_file_md5 TO content_sha256;
ALTER TABLE entity_image_catalog_candidates RENAME COLUMN legacy_file_md5 TO content_sha256;
ALTER TABLE entity_image_catalog_materializations RENAME COLUMN legacy_file_md5 TO content_sha256;

-- ── 4. drop ─────────────────────────────────────────────────────────────

ALTER TABLE media_assets DROP COLUMN legacy_file_md5;
ALTER TABLE asset_index DROP COLUMN legacy_file_md5;
