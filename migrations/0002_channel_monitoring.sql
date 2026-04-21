-- 0002_channel_monitoring.sql
-- Schema per il tracciamento dei canali e dei video tramite YouTube Data API v3

CREATE TABLE IF NOT EXISTS monitored_channels (
    channel_id TEXT PRIMARY KEY,
    title TEXT,
    uploads_playlist_id TEXT NOT NULL,
    last_checked_at TIMESTAMPTZ,
    config JSONB DEFAULT '{}', -- Per keyword, categorie, ecc.
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS video_metadata (
    video_id TEXT PRIMARY KEY,
    channel_id TEXT REFERENCES monitored_channels(channel_id),
    title TEXT NOT NULL,
    description TEXT,
    published_at TIMESTAMPTZ NOT NULL,
    duration_sec INTEGER,
    view_count BIGINT DEFAULT 0,
    like_count BIGINT DEFAULT 0,
    comment_count BIGINT DEFAULT 0,
    category_id TEXT,
    tags TEXT[], -- Array di tag
    language TEXT,
    status TEXT DEFAULT 'discovered', -- discovered, queued, downloaded, processed, failed
    gemma_classification JSONB, -- Risultato della classificazione Gemma
    drive_folder_id TEXT,
    drive_file_id TEXT,
    last_synced_at TIMESTAMPTZ DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_video_metadata_channel_published
    ON video_metadata (channel_id, published_at DESC);

CREATE INDEX IF NOT EXISTS idx_video_metadata_status
    ON video_metadata (status);

-- Estensione per storicizzare le visualizzazioni nel tempo (Point 3)
CREATE TABLE IF NOT EXISTS video_stats_history (
    id BIGSERIAL PRIMARY KEY,
    video_id TEXT NOT NULL REFERENCES video_metadata(video_id) ON DELETE CASCADE,
    view_count BIGINT NOT NULL,
    like_count BIGINT,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_video_stats_history_video_recorded
    ON video_stats_history (video_id, recorded_at DESC);
