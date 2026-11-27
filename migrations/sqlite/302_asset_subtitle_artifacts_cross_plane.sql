-- database: primary,observability,cache
-- DB-AUDIT-02 (2026-11-27): asset_subtitle_artifacts deliberately stayed on
-- SQLite while media_assets and asset_text_tracks moved to PostgreSQL. The
-- old FKs pointed at SQLite tables that the media cutover left empty, so every
-- insert either failed with "FOREIGN KEY constraint failed" (when enforcement
-- was on) or created an orphan (on the DualPool writer, which did not enforce
-- FKs). Rebuild the operational half without cross-plane FKs: asset_id and
-- text_track_id are opaque references validated by the repository against the
-- PostgreSQL parent when a validator is wired.
--
-- The rebuild preserves every row and the two canonical indexes.

DROP TABLE IF EXISTS asset_subtitle_artifacts_new;

CREATE TABLE asset_subtitle_artifacts_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    asset_id TEXT NOT NULL,
    text_track_id INTEGER NOT NULL,
    language_code TEXT NOT NULL,
    format TEXT NOT NULL CHECK (format IN ('ass', 'srt', 'vtt')),

    local_path TEXT NOT NULL,
    drive_file_id TEXT NOT NULL DEFAULT '',

    file_hash TEXT NOT NULL,
    text_hash TEXT NOT NULL,
    cues_hash TEXT NOT NULL,
    clip_content_hash TEXT NOT NULL,

    cue_count INTEGER NOT NULL,
    clip_duration_ms INTEGER NOT NULL,
    last_cue_end_ms INTEGER NOT NULL,

    style_version TEXT NOT NULL,
    generator_version TEXT NOT NULL,

    status TEXT NOT NULL
        CHECK (status IN ('PENDING', 'READY', 'FAILED', 'STALE')),

    is_current INTEGER NOT NULL DEFAULT 1,
    validation_error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    drive_url TEXT NOT NULL DEFAULT '',
    content_sha256 TEXT NOT NULL DEFAULT ''
);

INSERT INTO asset_subtitle_artifacts_new (
    id, asset_id, text_track_id, language_code, format,
    local_path, drive_file_id, file_hash, text_hash, cues_hash,
    clip_content_hash, cue_count, clip_duration_ms, last_cue_end_ms,
    style_version, generator_version, status, is_current,
    validation_error, created_at, updated_at, drive_url, content_sha256
)
SELECT
    id, asset_id, text_track_id, language_code, format,
    local_path, drive_file_id, file_hash, text_hash, cues_hash,
    clip_content_hash, cue_count, clip_duration_ms, last_cue_end_ms,
    style_version, generator_version, status, is_current,
    validation_error, created_at, updated_at, drive_url, content_sha256
FROM asset_subtitle_artifacts;

DROP TABLE asset_subtitle_artifacts;

ALTER TABLE asset_subtitle_artifacts_new RENAME TO asset_subtitle_artifacts;

CREATE UNIQUE INDEX IF NOT EXISTS idx_current_clip_ass
ON asset_subtitle_artifacts(asset_id, language_code, format)
WHERE is_current = 1;

CREATE UNIQUE INDEX IF NOT EXISTS idx_subtitle_artifacts_drive_file_unique
ON asset_subtitle_artifacts(drive_file_id)
WHERE is_current = 1 AND drive_file_id <> '';

-- Orphan telemetry cleanup lives in 306_observability_orphan_repair.sql, scoped
-- to the observability plane: a primary database does not necessarily carry
-- the run_* tables (verified by the 194/197 ledger test), so running the
-- DELETE here would abort the whole migration on those databases.
