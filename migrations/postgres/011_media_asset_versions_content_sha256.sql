-- 011_media_asset_versions_content_sha256.sql
--
-- Schema-drift convergence for asset_versions: the asset finalizer
-- (internal/capabilities/assets/finalizer/asset_finalizer_versions.go)
-- inserts the version row with content_sha256 (the sha256-tier content
-- hash of the published artifact), but the live table only carries the
-- legacy file_hash / legacy_file_md5 pair from 006_media_asset_versions.sql.
--
-- Non-destructive (godlike/07): ADD COLUMN IF NOT EXISTS keeps the legacy
-- hash columns readable for old consumers; the rename-style convergence
-- used for asset_locations (010) is not needed here because no live reader
-- selects asset_versions.file_hash.
--
-- Application: deployment-managed one-shot (same policy as 010), applied
-- in a transaction. Re-applying is a no-op.

ALTER TABLE asset_versions
    ADD COLUMN IF NOT EXISTS content_sha256 TEXT NOT NULL DEFAULT '';

-- Backfill: copy the legacy sha-tier file_hash into content_sha256 where
-- the new column is still empty, mirroring the backfill.go mapping
-- ("file_hash" -> "content_sha256"). Idempotent on re-run.
UPDATE asset_versions
SET content_sha256 = file_hash
WHERE content_sha256 = '' AND file_hash <> '';
