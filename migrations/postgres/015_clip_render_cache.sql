-- 015_clip_render_cache.sql — deterministic clip.render cache.
--
-- RA-13 (2026-11-21): the table used to be created with CREATE TABLE IF NOT
-- EXISTS at boot from internal/capabilities/cliprender. The schema now has a
-- canonical migration; the platform adapter keeps an idempotent ensure for
-- deployments that bootstrap through the composition root.
--
-- The table carries no foreign key to media_assets: it is a HINT, and the
-- cache read validates the referenced row (identity + content sha256 +
-- ACTIVE lifecycle) before serving a hit. The index supports the asset
-- lookup used by cleanup and diagnostics.
CREATE TABLE IF NOT EXISTS clip_render_cache (
    fingerprint  TEXT PRIMARY KEY,
    asset_id     TEXT NOT NULL,
    storage_key  TEXT NOT NULL DEFAULT '',
    artifact_url TEXT NOT NULL DEFAULT '',
    content_type TEXT NOT NULL DEFAULT '',
    sha256       TEXT NOT NULL,
    size_bytes   BIGINT NOT NULL,
    duration_sec REAL  NOT NULL DEFAULT 0,
    width        INTEGER NOT NULL DEFAULT 0,
    height       INTEGER NOT NULL DEFAULT 0,
    fps_num      INTEGER NOT NULL DEFAULT 0,
    fps_den      INTEGER NOT NULL DEFAULT 1,
    backend      TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_clip_render_cache_asset
    ON clip_render_cache (asset_id);
