-- 012_media_legacy_file_md5_converge.sql
-- PostgreSQL + pgvector media domain — IDEMPOTENT convergence twin of
-- 010_media_drop_legacy_file_md5.sql and 011_media_asset_versions_content_sha256.sql.
--
-- WHY THIS EXISTS. 010/011 are deployment-managed one-shots and are
-- deliberately excluded from MediaSelfBootstrapDDL (010 is not idempotent:
-- DROP/RENAME have no IF EXISTS). But every FRESH database — including the
-- one the integration suite bootstraps — therefore stopped at the pre-010
-- shape (asset_locations.legacy_file_md5) while the current writer family
-- upserts asset_locations.content_sha256, failing every commit with
-- "column content_sha256 of relation asset_locations does not exist".
-- A self-bootstrap that cannot reach the schema the code expects is a
-- silent deployment-managed dependency: this file converges both one-shots
-- with guarded, re-runnable statements so the self-bootstrap is sufficient.
--
-- End state is IDENTICAL to 010-then-011:
--   media_assets      : content_sha256 reconciled, legacy_file_md5 dropped
--   asset_locations   : content_sha256 (renamed from legacy_file_md5)
--   asset_versions    : content_sha256 (renamed, or added + backfilled)
--
-- Idempotent by construction: every DROP/RENAME is guarded by
-- information_schema existence probes, every UPDATE converges (re-running
-- changes nothing). Safe on databases where 010/011 already ran.

-- ── media_assets: reconcile then drop the legacy bucket ─────────────────
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'media_assets'
          AND column_name = 'legacy_file_md5'
    ) THEN
        UPDATE media_assets SET content_sha256 = LOWER(legacy_file_md5)
         WHERE content_sha256 = ''
           AND legacy_file_md5 ~ '^[0-9A-Fa-f]{64}$';
        ALTER TABLE media_assets DROP COLUMN legacy_file_md5;
    END IF;
END $$;
DROP INDEX IF EXISTS idx_media_assets_legacy_file_md5;

-- ── asset_locations: converge to content_sha256 ────────────────────────
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'asset_locations'
          AND column_name = 'legacy_file_md5'
    ) THEN
        UPDATE asset_locations SET legacy_file_md5 = ''
         WHERE legacy_file_md5 <> ''
           AND legacy_file_md5 !~ '^[0-9A-Fa-f]{64}$';
        IF NOT EXISTS (
            SELECT 1 FROM information_schema.columns
            WHERE table_schema = 'public'
              AND table_name = 'asset_locations'
              AND column_name = 'content_sha256'
        ) THEN
            ALTER TABLE asset_locations RENAME COLUMN legacy_file_md5 TO content_sha256;
        ELSE
            UPDATE asset_locations SET content_sha256 = LOWER(legacy_file_md5)
             WHERE content_sha256 = ''
               AND legacy_file_md5 ~ '^[0-9A-Fa-f]{64}$';
            ALTER TABLE asset_locations DROP COLUMN legacy_file_md5;
        END IF;
    END IF;
END $$;

-- ── asset_versions: converge to content_sha256 (010 rename OR 011 add) ─
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'asset_versions'
          AND column_name = 'legacy_file_md5'
    ) THEN
        UPDATE asset_versions SET legacy_file_md5 = ''
         WHERE legacy_file_md5 <> ''
           AND legacy_file_md5 !~ '^[0-9A-Fa-f]{64}$';
        IF NOT EXISTS (
            SELECT 1 FROM information_schema.columns
            WHERE table_schema = 'public'
              AND table_name = 'asset_versions'
              AND column_name = 'content_sha256'
        ) THEN
            ALTER TABLE asset_versions RENAME COLUMN legacy_file_md5 TO content_sha256;
        ELSE
            UPDATE asset_versions SET content_sha256 = LOWER(legacy_file_md5)
             WHERE content_sha256 = ''
               AND legacy_file_md5 ~ '^[0-9A-Fa-f]{64}$';
            ALTER TABLE asset_versions DROP COLUMN legacy_file_md5;
        END IF;
    END IF;
END $$;

-- 011 semantics on databases that never saw it: add the column and map the
-- legacy sha-tier file_hash into it. Both statements converge on re-run.
ALTER TABLE asset_versions
    ADD COLUMN IF NOT EXISTS content_sha256 TEXT NOT NULL DEFAULT '';
UPDATE asset_versions
   SET content_sha256 = file_hash
 WHERE content_sha256 = '' AND file_hash <> '';
