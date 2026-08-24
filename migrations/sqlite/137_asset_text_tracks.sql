-- 137_asset_text_tracks.sql
--
-- Localized, versioned text tracks per media asset. Each row stores
-- one text resource (transcript, description, summary, title, or
-- keywords) in a specific language for a specific asset. Replaces
-- the ad-hoc TranscriptPath / CleanTranscript / EmbeddingText fields
-- on CanonicalClipMetadata with a proper relational structure.
--
-- Design decisions:
--   - UNIQUE(asset_id, language_code, text_kind) enforces at most one
--     text track per (asset, language, kind) combination. Translations
--     and re-transcriptions upsert into the same row.
--   - text_hash is SHA-256(normalized_text + language_code + text_kind).
--     Used by source_version computation so Qdrant re-indexes when a
--     translation is added or corrected.
--   - source_type records provenance: provided (user payload),
--     youtube_subtitle, whisper, translation, manual.
--   - status follows the READY / PENDING / FAILED tri-state pattern
--     so the resolver can distinguish "not yet transcribed" from
--     "transcription failed" and decide whether to retry Whisper.
--   - provider / model_name / model_version record which AI model
--     produced the text (e.g. Whisper tiny, qwen 2.5, google-translate).
--   - is_original flags the source language text so resolvers can
--     distinguish originals from translations.
--   - confidence stores the provider's confidence score (0.0–1.0)
--     when available (e.g. Whisper average_logprob mapped to [0,1]).
--
-- Consumed by:
--   - internal/domain/asset/text_track.go (domain types)
--   - internal/platform/sqlite/assets/text_track_repository.go
--   - internal/application/.../text_track_resolver.go
--   - internal/platform/sqlite/assets/source_version.go
--       (text_hash inclusion in source_version fingerprint)

CREATE TABLE IF NOT EXISTS asset_text_tracks (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,

    asset_id            TEXT NOT NULL,
    language_code       TEXT NOT NULL,
    text_kind           TEXT NOT NULL,

    text_content        TEXT NOT NULL DEFAULT '',

    source_type         TEXT NOT NULL DEFAULT 'provided',
    source_language_code TEXT NOT NULL DEFAULT '',
    is_original         INTEGER NOT NULL DEFAULT 0,

    provider            TEXT NOT NULL DEFAULT '',
    model_name          TEXT NOT NULL DEFAULT '',
    model_version       TEXT NOT NULL DEFAULT '',

    text_hash           TEXT NOT NULL DEFAULT '',
    source_version      TEXT NOT NULL DEFAULT '',

    confidence          REAL,  -- nullable: NULL means provider did not report confidence
    status              TEXT NOT NULL DEFAULT 'READY'
                        CHECK (status IN ('READY', 'PENDING', 'FAILED')),

    created_at          TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at          TEXT NOT NULL DEFAULT (datetime('now')),

    FOREIGN KEY (asset_id) REFERENCES media_assets(id) ON DELETE CASCADE,

    UNIQUE(asset_id, language_code, text_kind)
);

-- Fast lookup by asset: used by ListByAsset and the resolver's
-- "is there already a READY transcript for this clip?" check.
CREATE INDEX IF NOT EXISTS idx_asset_text_tracks_asset
    ON asset_text_tracks (asset_id);

-- Lookup by language + kind: used by the SearchTextBuilder to fetch
-- all transcripts/descriptions in configured index_languages.
CREATE INDEX IF NOT EXISTS idx_asset_text_tracks_language
    ON asset_text_tracks (language_code, text_kind);

-- Dedup / change-detection by content hash: used by source_version
-- computation and by the "skip Whisper if identical text already
-- exists" fast path.
CREATE INDEX IF NOT EXISTS idx_asset_text_tracks_hash
    ON asset_text_tracks (text_hash);
