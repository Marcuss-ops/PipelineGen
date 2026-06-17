-- 033_media_assets_youtube_video_id_index.sql
--
-- Adds a partial expression index on `json_extract(COALESCE(metadata_json,'{}'), '$.youtube_video_id')`
-- to speed up the dedup pre-check and the 30-minute background dedup sweeper
-- on the media_assets table.
--
-- Without this index, both queries full-table-scan with json_extract on every
-- row. At 100K+ rows the sweeper's GROUP BY becomes slow and the pre-check
-- in register_from_youtube.go adds tens of ms per request. The index makes
-- both O(log n) on the indexed subset (clips that *have* a youtube_video_id).
--
-- Why a *partial* expression index:
--   * Partial: 99% of clips are artlist/stock/manual uploads that don't have
--     a youtube_video_id. The WHERE clause excludes them from the index
--     entirely, keeping the index small and the writes cheap.
--   * Expression: we index the *extracted* value (json_extract) instead of
--     duplicating the data in a denormalized column. No schema change, no
--     backfill, no risk of column drift.
--   * IF NOT EXISTS: safe to re-run during partial migrations.
--
-- Why the COALESCE matches the dedup queries exactly:
--   findByYouTubeVideoID and runDedupSweep both use
--   `json_extract(COALESCE(metadata_json,'{}'), '$.youtube_video_id')`.
--   The index expression must match the query expression verbatim for
--   SQLite's query planner to use the index.
--
-- Backward compatibility:
--   * DROP INDEX IF EXISTS is included at the bottom as a rollback helper.
--   * The index doesn't change any existing data or query results.

-- Ensure the media_assets table exists before creating the index.
-- The table may not exist during initial bootstrap (e.g. tests). The minimal
-- schema here is a safe no-op if the table already exists (e.g. created by
-- application code or earlier migrations). Columns are minimal because the
-- index only references metadata_json.
CREATE TABLE IF NOT EXISTS media_assets (
    id TEXT PRIMARY KEY,
    source TEXT,
    name TEXT,
    tags TEXT,
    tags_norm TEXT,
    duration_ms INTEGER,
    url TEXT,
    media_type TEXT,
    status TEXT,
    local_path TEXT,
    relative_path TEXT,
    drive_file_id TEXT,
    drive_folder_id TEXT,
    drive_link TEXT,
    download_link TEXT,
    file_hash TEXT,
    embedding_json TEXT,
    metadata_json TEXT,
    visual_embedding TEXT,
    transcript_embedding TEXT,
    created_at TEXT,
    updated_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_media_assets_youtube_video_id
  ON media_assets(json_extract(COALESCE(metadata_json, '{}'), '$.youtube_video_id'))
  WHERE json_extract(COALESCE(metadata_json, '{}'), '$.youtube_video_id') IS NOT NULL
    AND json_extract(COALESCE(metadata_json, '{}'), '$.youtube_video_id') != '';

-- Refresh query planner statistics so the optimizer knows the new index
-- is highly selective. Without ANALYZE, the planner may default to a full
-- table scan for the first few weeks of the index's life. Per-table ANALYZE
-- requires SQLite >= 3.32.0; the bundled SQLite in mattn/go-sqlite3 is
-- well past that.
-- NOTE: The table may not exist during initial bootstrap (e.g. tests).
-- ANALYZE is non-critical query planner guidance; skip if table is absent.
-- (The migration runner is intentionally kept simple — no conditional DDL.)
-- ANALYZE media_assets;

-- Rollback helper (uncomment to revert):
-- DROP INDEX IF EXISTS idx_media_assets_youtube_video_id;
