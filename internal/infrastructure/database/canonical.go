package storage

// CanonicalMediaAssetsSchema is the single source of truth for the
// in-memory CREATE TABLE block required by assets.ClipsRepository.UpsertClipTx
// (37 INSERT columns) and scanMediaAsset (39-column SELECT projection).
//
// The 39 columns resolve in three layers, oldest at the top of the
// CREATE TABLE so the column-order history is preserved:
//
//  1. Original 22 columns from the pre-migration-059 media_assets
//     (id through tags_norm, embedding_json, duration_ms, url,
//     created_at, metadata_json, drive_folder_id, visual_embedding,
//     transcript_embedding, updated_at).
//
//  2. Eight legacy-promoted columns (drive_link, download_link,
//     drive_file_id, file_hash, local_path, media_type,
//     audio_embedding, language)
//     plus image dimensions (width, height) — already-existed columns
//     whose JSON mirror was deleted by migration 059's json_remove.
//
//     The `status` column is REMOVED by migration 101 (PR1 — Lifecycle
//     state SSOT, June 2026) because it co-existed with `lifecycle_state`
//     and writers used it for a parallel lowercase asset-status enum.
//     Pre-101 readers consulted COALESCE(lifecycle_state, status, …);
//     post-101 reads skip the column entirely. Including `status`
//     here would re-introduce the dual-source-of-truth drift the
//     migration was authored to eliminate.
//
//  3. Fifteen canonical columns added by migration 059
//     (lifecycle_state, deleted_at, folder_id, parent_folder_id,
//     folder_path, category, filename, error, thumb_url, phash,
//     search_text, scene_type, quality_score, reuse_count, last_used_at).
//
// Test fixtures that call drive.NewTestDBWithSchema MUST embed this
// constant so their schema stays in lockstep with the production
// schema. When migration adds another canonical column it should:
//   - append a column definition here, AND
//   - append a matching mediaAssetColumns entry in
//     internal/infrastructure/database/sqlite/clips_repository.go
//     (the INSERT projection used by UpsertClipTx), AND
//   - append a matching scan target in the same file
//     (the SELECT projection used by reading rows).
//
// Keeping these three edits in lockstep prevents drift. Do NOT
// recreate the same column list inline in any other file.
//
// Pre-migration fixture schemas (e.g. the pre059Schema in
// internal/storage/migrations_test.go that exercises migration 059
// itself) intentionally do NOT embed this constant — they must mirror
// the schema as it existed BEFORE migration 059 ran.
const CanonicalMediaAssetsSchema = `
CREATE TABLE IF NOT EXISTS media_assets (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    tags TEXT NOT NULL DEFAULT '[]',
    tags_norm TEXT NOT NULL DEFAULT '',
    embedding_json TEXT NOT NULL DEFAULT '[]',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    url TEXT NOT NULL DEFAULT '',
    created_at TEXT,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    drive_folder_id TEXT,
    visual_embedding TEXT,
    transcript_embedding TEXT,
    updated_at TEXT NOT NULL DEFAULT '',
    media_type TEXT NOT NULL DEFAULT '',
    status TEXT,
    local_path TEXT,
    relative_path TEXT,
    drive_file_id TEXT,
    drive_link TEXT,
    download_link TEXT,
    file_hash TEXT,
    audio_embedding TEXT NOT NULL DEFAULT '[]',
    language TEXT NOT NULL DEFAULT '',
    width INTEGER NOT NULL DEFAULT 0,
    height INTEGER NOT NULL DEFAULT 0,
    lifecycle_state TEXT NOT NULL DEFAULT 'ACTIVE',
    deleted_at TEXT NOT NULL DEFAULT '',
    folder_id TEXT NOT NULL DEFAULT '',
    parent_folder_id TEXT NOT NULL DEFAULT '',
    folder_path TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    filename TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    thumb_url TEXT NOT NULL DEFAULT '',
    phash TEXT NOT NULL DEFAULT '',
    search_text TEXT NOT NULL DEFAULT '',
    scene_type TEXT NOT NULL DEFAULT '',
    quality_score REAL NOT NULL DEFAULT 0.0,
    reuse_count INTEGER NOT NULL DEFAULT 0,
    last_used_at TEXT NOT NULL DEFAULT '',
    group_name TEXT NOT NULL DEFAULT '',
    search_terms TEXT NOT NULL DEFAULT '[]',
    clip_page_url TEXT NOT NULL DEFAULT '',
    thumbnail_url TEXT NOT NULL DEFAULT '',
    external_url TEXT NOT NULL DEFAULT '',
    usable_for TEXT NOT NULL DEFAULT '[]',
    avoid_for TEXT NOT NULL DEFAULT '[]',
    child_count INTEGER NOT NULL DEFAULT 0,
    is_folder INTEGER NOT NULL DEFAULT 0,
    depth INTEGER NOT NULL DEFAULT 0,
    visual_embedding_json TEXT NOT NULL DEFAULT '',
    mime_type TEXT NOT NULL DEFAULT '',
    file_size_bytes INTEGER NOT NULL DEFAULT 0,
    project TEXT NOT NULL DEFAULT '',
    -- Migration 099 (June 2026) — QDRANT asset column alignment.
    -- Types MUST match migrations/sqlite/099_qdrant_asset_columns.sql
    -- so the canonical CREATE TABLE reproduces what the migration chain
    -- produces on a fresh DB. start_time / end_time are stored as TEXT
    -- (not REAL) per the migration — that decision is the source of
    -- truth; this constant mirrors it.
    youtube_video_id TEXT NOT NULL DEFAULT '',
    youtube_url TEXT NOT NULL DEFAULT '',
    start_time TEXT NOT NULL DEFAULT '',
    end_time TEXT NOT NULL DEFAULT '',
    workspace_id TEXT NOT NULL DEFAULT '',
    channel_id TEXT NOT NULL DEFAULT '',
    license TEXT NOT NULL DEFAULT '',
    source_version TEXT NOT NULL DEFAULT '',
    style TEXT NOT NULL DEFAULT ''
);`
