CREATE TABLE IF NOT EXISTS asset_subtitle_artifacts (
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

    FOREIGN KEY (asset_id) REFERENCES media_assets(id) ON DELETE CASCADE,
    FOREIGN KEY (text_track_id) REFERENCES asset_text_tracks(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_current_clip_ass
ON asset_subtitle_artifacts(asset_id, language_code, format)
WHERE is_current = 1;
