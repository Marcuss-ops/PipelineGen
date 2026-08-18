-- migrations/sqlite/219_asset_render_variants.sql
-- Per-language rendered-clip variant ledger (multilingua subtitle burn-in).
--
-- One row per (source_clip_id, language_code). The fingerprint is the
-- deterministic identity of the rendered output:
--
--   SHA-256(source_clip_sha256 + transcript_sha256 + target_language
--          + translation_version + subtitle_style_version + render_profile_version)
--
-- A re-run with the same fingerprint is a no-op (the READY row + Drive link
-- are reused); a changed transcript, style, or render profile bumps the
-- fingerprint and produces a new is_current row, keeping the audit trail.
CREATE TABLE IF NOT EXISTS asset_render_variants (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_clip_id TEXT NOT NULL,
    language_code TEXT NOT NULL,

    fingerprint TEXT NOT NULL,
    source_clip_sha256 TEXT NOT NULL,
    transcript_sha256 TEXT NOT NULL,
    translation_version TEXT NOT NULL DEFAULT '',
    subtitle_style_version TEXT NOT NULL DEFAULT '',
    render_profile_version TEXT NOT NULL DEFAULT '',

    subtitle_hash TEXT NOT NULL DEFAULT '',
    output_hash TEXT NOT NULL DEFAULT '',

    drive_file_id TEXT NOT NULL DEFAULT '',
    drive_link TEXT NOT NULL DEFAULT '',

    duration_ms INTEGER NOT NULL DEFAULT 0,
    size_bytes INTEGER NOT NULL DEFAULT 0,

    status TEXT NOT NULL
        CHECK (status IN ('PENDING', 'READY', 'FAILED')),
    validation_error TEXT NOT NULL DEFAULT '',

    is_current INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,

    FOREIGN KEY (source_clip_id) REFERENCES media_assets(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_render_variants_source_lang
ON asset_render_variants(source_clip_id, language_code);

-- At most one "current" variant per (source_clip_id, language_code).
CREATE UNIQUE INDEX IF NOT EXISTS idx_render_variants_current
ON asset_render_variants(source_clip_id, language_code)
WHERE is_current = 1;

CREATE INDEX IF NOT EXISTS idx_render_variants_fingerprint
ON asset_render_variants(source_clip_id, language_code, fingerprint);
