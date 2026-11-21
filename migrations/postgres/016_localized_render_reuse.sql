-- 016_localized_render_reuse.sql — localized render reuse cache.
--
-- RA-13 (2026-11-21): the table used to be created with CREATE TABLE IF NOT
-- EXISTS plus ALTER TABLE ADD COLUMN IF NOT EXISTS at boot from
-- internal/capabilities/localization/adapters. The schema now has a canonical
-- migration; the platform adapter keeps an idempotent ensure for deployments
-- that bootstrap through the composition root.
--
-- The record is a HINT: the renderer re-stats, re-hashes and re-checks the
-- cached bytes before accepting a hit. The Drive identity columns are optional
-- publication facts; a row written before publication keeps empty values.
CREATE TABLE IF NOT EXISTS localized_render_reuse (
    fingerprint     TEXT PRIMARY KEY,
    local_path      TEXT   NOT NULL,
    sha256          TEXT   NOT NULL,
    size_bytes      BIGINT NOT NULL,
    duration_ms     BIGINT NOT NULL,
    video_codec     TEXT   NOT NULL DEFAULT '',
    audio_codec     TEXT   NOT NULL DEFAULT '',
    backend         TEXT   NOT NULL DEFAULT '',
    created_at      TEXT   NOT NULL,
    drive_file_id   TEXT   NOT NULL DEFAULT '',
    drive_link      TEXT   NOT NULL DEFAULT '',
    drive_folder_id TEXT   NOT NULL DEFAULT ''
);

-- Existing deployments created the table before the publication columns
-- existed; the guarded ADD keeps the migration applicable to both shapes.
ALTER TABLE localized_render_reuse ADD COLUMN IF NOT EXISTS drive_file_id   TEXT NOT NULL DEFAULT '';
ALTER TABLE localized_render_reuse ADD COLUMN IF NOT EXISTS drive_link      TEXT NOT NULL DEFAULT '';
ALTER TABLE localized_render_reuse ADD COLUMN IF NOT EXISTS drive_folder_id TEXT NOT NULL DEFAULT '';
