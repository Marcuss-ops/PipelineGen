-- 010_media_drop_legacy_file_md5.sql
-- PostgreSQL + pgvector media domain — contract phase of the legacy digest
-- bucket. Mirrors SQLite 281_legacy_file_md5_contract.sql (godlike/06 parity).
--
-- The `legacy_file_md5` columns were the continuation of the pre-152
-- `file_hash` bucket (mixed 32-hex MD5 / 64-hex SHA-256). The canonical
-- content address is `content_sha256` (media_assets) — a digest is a content
-- address only when it IS a SHA-256.
--
--   1. RECONCILE — valid 64-hex values fill an empty canonical column.
--   2. REMOVAL — non-reconcilable values are removed (asset_locations rows
--      survive without them; there is no entity-image catalog on this side).
--   3. RENAME — asset_locations / asset_versions take `content_sha256`.
--   4. DROP — media_assets loses the column and its index.
--
-- Apply once, after 009_drop_media_asset_faces.sql, to the dedicated media
-- database. NOT idempotent (DROP/RENAME have no IF EXISTS).

-- ── 1. reconcile ────────────────────────────────────────────────────────

UPDATE media_assets SET content_sha256 = LOWER(legacy_file_md5)
 WHERE content_sha256 = ''
   AND legacy_file_md5 ~ '^[0-9A-Fa-f]{64}$';

-- ── 2. removal of non-reconcilable values ───────────────────────────────

UPDATE asset_locations SET legacy_file_md5 = ''
 WHERE legacy_file_md5 <> ''
   AND legacy_file_md5 !~ '^[0-9A-Fa-f]{64}$';

UPDATE asset_versions SET legacy_file_md5 = ''
 WHERE legacy_file_md5 <> ''
   AND legacy_file_md5 !~ '^[0-9A-Fa-f]{64}$';

-- ── 3. rename ───────────────────────────────────────────────────────────

ALTER TABLE asset_locations RENAME COLUMN legacy_file_md5 TO content_sha256;
ALTER TABLE asset_versions RENAME COLUMN legacy_file_md5 TO content_sha256;

-- ── 4. drop ─────────────────────────────────────────────────────────────

DROP INDEX IF EXISTS idx_media_assets_legacy_file_md5;
ALTER TABLE media_assets DROP COLUMN legacy_file_md5;
