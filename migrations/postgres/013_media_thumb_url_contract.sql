-- 013_media_thumb_url_contract.sql
-- PostgreSQL + pgvector media domain — CONTRACT phase of the media_assets
-- thumbnail mirror pair (migration-museum audit point 8, "media_assets mirror
-- fields": thumb_url vs thumbnail_url).
--
-- WHY THIS EXISTS. `thumbnail_url` is the canonical column (model field
-- ThumbnailURL, written by the canonical upsert of PostgresMediaCommitter);
-- `thumb_url` is its mirror. The pair carried a write/read asymmetry: the
-- committer's compatibility UPDATE targeted the FALLBACK column (thumb_url)
-- while readers preferred `COALESCE(NULLIF(thumbnail_url,''), thumb_url)` —
-- two columns holding one fact, written from opposite ends. The audit pinned
-- thumbnail_url canonical and this file contracts the mirror.
--
-- END STATE: media_assets carries ONE thumbnail column (thumbnail_url).
--   1. BACKFILL — rows whose thumbnail_url is empty take the mirror value.
--   2. DROP    — thumb_url goes away (guarded, so this file is re-runnable).
--
-- The Go side moved in the same change: the committer's mirror UPDATE was
-- removed (the upsert already writes thumbnail_url), and every reader resolves
-- thumbnail_url only. The SQLite media copy is contracted by
-- migrations/sqlite/286_media_mirror_fields_contract.sql.
--
-- IDEMPOTENT by construction: the DROP is guarded by an information_schema
-- existence probe, the backfill converges (re-running changes nothing). Safe
-- on databases where the deployment-managed one-shots never ran and on fresh
-- databases bootstrapped from 001_media_schema.sql (which still declares the
-- mirror for historical parity — this file converges it away).

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'media_assets'
          AND column_name = 'thumb_url'
    ) THEN
        UPDATE media_assets
           SET thumbnail_url = COALESCE(NULLIF(thumbnail_url, ''), NULLIF(thumb_url, ''), '')
         WHERE thumbnail_url = ''
           AND thumb_url <> '';
        ALTER TABLE media_assets DROP COLUMN thumb_url;
    END IF;
END $$;
