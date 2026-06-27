-- 098_qdrant_asset_columns.sql
-- Canonical Qdrant asset-column alignment for fresh installs and legacy DBs.
--
-- Adds the fields required by qdrant.SQLiteAssetStore.FetchAsset and the
-- reindex/audit tooling. The migration runner is idempotent at the file level;
-- duplicate ADD COLUMN statements are soft-skipped by the runner when the
-- column already exists on a legacy database.

ALTER TABLE media_assets ADD COLUMN audio_embedding TEXT NOT NULL DEFAULT '[]';
ALTER TABLE media_assets ADD COLUMN language TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN youtube_video_id TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN youtube_url TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN start_time TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN end_time TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN workspace_id TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN channel_id TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN license TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN source_version TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN style TEXT NOT NULL DEFAULT '';

-- Backfill from legacy metadata_json mirrors when present. The updates are
-- safe on empty tables and idempotent on rows that already have explicit values.
UPDATE media_assets SET
    audio_embedding = COALESCE(NULLIF(audio_embedding, ''), '[]'),
    language = COALESCE(NULLIF(language, ''), json_extract(metadata_json, '$.language'), ''),
    youtube_video_id = COALESCE(NULLIF(youtube_video_id, ''), json_extract(metadata_json, '$.youtube_video_id'), ''),
    youtube_url = COALESCE(NULLIF(youtube_url, ''), json_extract(metadata_json, '$.youtube_url'), ''),
    start_time = COALESCE(NULLIF(start_time, ''), json_extract(metadata_json, '$.start_time'), ''),
    end_time = COALESCE(NULLIF(end_time, ''), json_extract(metadata_json, '$.end_time'), ''),
    workspace_id = COALESCE(NULLIF(workspace_id, ''), json_extract(metadata_json, '$.workspace_id'), ''),
    channel_id = COALESCE(NULLIF(channel_id, ''), json_extract(metadata_json, '$.channel_id'), ''),
    license = COALESCE(NULLIF(license, ''), json_extract(metadata_json, '$.license'), ''),
    source_version = COALESCE(NULLIF(source_version, ''), json_extract(metadata_json, '$.source_version'), ''),
    style = COALESCE(NULLIF(style, ''), json_extract(metadata_json, '$.style'), '')
WHERE
    audio_embedding IS NULL OR audio_embedding = ''
    OR youtube_video_id IS NULL OR youtube_video_id = ''
    OR youtube_url IS NULL OR youtube_url = ''
    OR start_time IS NULL OR start_time = ''
    OR end_time IS NULL OR end_time = ''
    OR workspace_id IS NULL OR workspace_id = ''
    OR channel_id IS NULL OR channel_id = ''
    OR license IS NULL OR license = ''
    OR source_version IS NULL OR source_version = ''
    OR style IS NULL OR style = '';
