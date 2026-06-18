-- Migration 059: canonicalize media_assets dual-read/write (Blocco 3 / Task 9)
--
-- Goals:
--   1. Add typed columns for fields previously stored only in metadata_json.
--      These are the "canonical" fields tracked by columns; reads should use
--      columns, not json_extract(metadata_json, '$.<key>').
--   2. Backfill new columns from the existing metadata_json one-time.
--   3. Strip the migrated keys from metadata_json so the column is the
--      single source of truth going forward.
--   4. Add indexes for the columns frequently hit in WHERE clauses.
--
-- Fields kept in metadata_json (NOT migrated to columns): clipindexer
-- state machine (index_state, indexed_at, indexed_content_hash, embedding_*),
-- transcript-derived search fields (clean_title, clip_summary, hook, topics,
-- speakers, mentioned_people, people, clip_tags, search_keywords,
-- embedding_text, clean_transcript) — these are too transient or too
-- source-specific to justify a typed column. The catalogue layer reads
-- them via json_extract on metadata_json and will continue to do so.

-- IMPORTANT: do NOT open BEGIN/COMMIT — storage.RunMigrations already wraps
-- each migration in an outer transaction. A nested BEGIN inside the runner
-- tx would fail with "cannot start a transaction within a transaction".

-- 1. Add canonical columns
ALTER TABLE media_assets ADD COLUMN lifecycle_state TEXT NOT NULL DEFAULT 'ready';
ALTER TABLE media_assets ADD COLUMN deleted_at      TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN folder_id       TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN parent_folder_id TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN folder_path     TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN category        TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN filename        TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN error           TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN thumb_url       TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN phash           TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN search_text     TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN scene_type      TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN quality_score   REAL NOT NULL DEFAULT 0.0;
ALTER TABLE media_assets ADD COLUMN reuse_count     INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media_assets ADD COLUMN last_used_at    TEXT NOT NULL DEFAULT '';

-- 2. Backfill from existing metadata_json — safe on empty tables too,
--    because COALESCE on a NULL json_extract returns the column default.
UPDATE media_assets SET
    lifecycle_state  = COALESCE(NULLIF(json_extract(metadata_json, '$.lifecycle_state'), ''), 'ready'),
    deleted_at       = COALESCE(json_extract(metadata_json, '$.deleted_at'), ''),
    folder_id        = COALESCE(json_extract(metadata_json, '$.folder_id'), ''),
    parent_folder_id = COALESCE(json_extract(metadata_json, '$.parent_folder_id'), ''),
    folder_path      = COALESCE(json_extract(metadata_json, '$.folder_path'), ''),
    category         = COALESCE(json_extract(metadata_json, '$.category'), ''),
    filename         = COALESCE(json_extract(metadata_json, '$.filename'), ''),
    error            = COALESCE(json_extract(metadata_json, '$.error'), ''),
    thumb_url        = COALESCE(json_extract(metadata_json, '$.thumb_url'), ''),
    phash            = COALESCE(json_extract(metadata_json, '$.phash'), ''),
    search_text      = COALESCE(json_extract(metadata_json, '$.search_text'), ''),
    scene_type       = COALESCE(json_extract(metadata_json, '$.scene_type'), ''),
    quality_score    = COALESCE(CAST(json_extract(metadata_json, '$.quality_score') AS REAL), 0.0),
    reuse_count      = COALESCE(CAST(json_extract(metadata_json, '$.reuse_count') AS INTEGER), 0),
    last_used_at     = COALESCE(json_extract(metadata_json, '$.last_used_at'), '')
;

-- 3. Strip the migrated keys from metadata_json. The column is canonical
--    now; metadata_json keeps only non-canonical keys (transcript-derivable
--    search fields, clipindexer state machine, provider_raw_metadata, etc.).
--
--    The 7 legacy keys at the bottom (drive_link, download_link,
--    drive_file_id, file_hash, local_path, status, media_type) used to be
--    written into metadata_json by the now-removed populateAssetMetadata
--    helper. They were duplicates of the existing typed columns, so this
--    purge reclaims storage and keeps the canonical-column invariant.
UPDATE media_assets
SET metadata_json = json_remove(
    metadata_json,
    '$.deleted_at',
    '$.folder_id',
    '$.parent_folder_id',
    '$.folder_path',
    '$.category',
    '$.filename',
    '$.error',
    '$.thumb_url',
    '$.phash',
    '$.search_text',
    '$.scene_type',
    '$.quality_score',
    '$.reuse_count',
    '$.last_used_at',
    '$.drive_link',
    '$.download_link',
    '$.drive_file_id',
    '$.file_hash',
    '$.local_path',
    '$.status',
    '$.media_type'
)
WHERE metadata_json IS NOT NULL AND metadata_json != '{}';

-- 4. Indexes — only for columns used in high-frequency WHERE clauses.
--    tags_norm already has idx_media_tags from migration 033.
CREATE INDEX IF NOT EXISTS idx_media_assets_lifecycle ON media_assets(lifecycle_state);
CREATE INDEX IF NOT EXISTS idx_media_assets_category  ON media_assets(category);
CREATE INDEX IF NOT EXISTS idx_media_assets_folder_id ON media_assets(folder_id);
