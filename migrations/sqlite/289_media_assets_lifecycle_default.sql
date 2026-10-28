-- database: primary
-- foreign_keys: off
-- Migration 289: unify the media_assets.lifecycle_state default across planes.
--
-- WHY. The SQLite declaration carried DEFAULT 'ready' (lowercase) while the
-- PostgreSQL media SSOT declares DEFAULT 'ACTIVE' — casing and value divergent
-- across planes for the same logical column (docs/migrations/STATE_MATRIX.md
-- "Drift noti"). Worse, 'ready' is NOT in the state_valid trigger vocabulary
-- (trg_media_assets_state_valid_*): any insert omitting lifecycle_state would
-- ABORT at the trigger. The operative vocabulary is the UPPER one
-- (internal/platform/sqlite/canonical.go) and every live row already carries
-- UPPER values ('ACTIVE', 'PUBLISHED').
--
-- WHAT. A schema-preserving table rebuild — SQLite has no ALTER COLUMN — that
-- changes exactly ONE declaration (the default) and carries everything else
-- forward VERBATIM: the column set, the 17 explicit indexes (plus the PK
-- autoindex) and the 5 triggers (the asset_state projection triggers are this
-- schema's generated-column mechanism; their bodies are runtime logic,
-- unchanged). Row content is copied untouched: no state vocabulary is
-- rewritten at migration time.
--
-- WHY `-- foreign_keys: off`. Ten+ child tables declare
-- `REFERENCES media_assets(id) ON DELETE CASCADE`, which makes a parent-table
-- rebuild inexpressible under enforcement:
--   - the implicit DELETE FROM of DROP TABLE fires every ON DELETE CASCADE
--     action immediately (PRAGMA defer_foreign_keys defers violations, NOT
--     actions), destroying all child rows;
--   - ALTER TABLE RENAME rewrites the REFERENCES clauses of every child
--     table onto the renamed-away table, stranding them after the drop.
-- `PRAGMA foreign_keys` is a no-op inside a transaction, so the runner
-- honours this header directive by toggling enforcement around this file's
-- transaction (migrations_apply.go) and restoring it afterwards. The rebuild
-- order keeps every external REFERENCES clause pointing at the literal name
-- `media_assets` throughout: create-scratch -> copy -> drop-original ->
-- rename-scratch-into-place. The schema verifier models this shape
-- (migrations_verify.go: "table rebuild CREATE jobs_new ... RENAME TO jobs").

CREATE TABLE media_assets_289 (
    id TEXT PRIMARY KEY,
    source TEXT,
    name TEXT,
    tags TEXT,
    tags_norm TEXT,
    duration_ms INTEGER,
    url TEXT,
    media_type TEXT,
    local_path TEXT,
    relative_path TEXT,
    drive_file_id TEXT,
    drive_folder_id TEXT,
    drive_link TEXT,
    download_link TEXT,
    embedding_json TEXT,
    metadata_json TEXT,
    visual_embedding TEXT,
    transcript_embedding TEXT,
    created_at TEXT,
    updated_at TEXT
, lifecycle_state TEXT NOT NULL DEFAULT 'ACTIVE', deleted_at      TEXT NOT NULL DEFAULT '', folder_id       TEXT NOT NULL DEFAULT '', parent_folder_id TEXT NOT NULL DEFAULT '', folder_path     TEXT NOT NULL DEFAULT '', category        TEXT NOT NULL DEFAULT '', filename        TEXT NOT NULL DEFAULT '', error           TEXT NOT NULL DEFAULT '', phash           TEXT NOT NULL DEFAULT '', search_text     TEXT NOT NULL DEFAULT '', scene_type      TEXT NOT NULL DEFAULT '', quality_score   REAL NOT NULL DEFAULT 0.0, reuse_count     INTEGER NOT NULL DEFAULT 0, last_used_at    TEXT NOT NULL DEFAULT '', thumbnail_url   TEXT    NOT NULL DEFAULT '', clip_page_url   TEXT    NOT NULL DEFAULT '', width INTEGER NOT NULL DEFAULT 0, height INTEGER NOT NULL DEFAULT 0, group_name TEXT NOT NULL DEFAULT '', search_terms TEXT NOT NULL DEFAULT '', index_state TEXT NOT NULL DEFAULT 'DISCOVERED', index_state_updated_at TEXT NOT NULL DEFAULT '', collection_version TEXT NOT NULL DEFAULT '', audio_embedding TEXT NOT NULL DEFAULT '[]', language TEXT NOT NULL DEFAULT '', youtube_video_id TEXT NOT NULL DEFAULT '', youtube_url TEXT NOT NULL DEFAULT '', start_time TEXT NOT NULL DEFAULT '', end_time TEXT NOT NULL DEFAULT '', workspace_id TEXT NOT NULL DEFAULT '', channel_id TEXT NOT NULL DEFAULT '', license TEXT NOT NULL DEFAULT '', source_version TEXT NOT NULL DEFAULT '', style TEXT NOT NULL DEFAULT '', external_id         TEXT NOT NULL DEFAULT '', discovered_via      TEXT NOT NULL DEFAULT '', discovered_at       TEXT NOT NULL DEFAULT '', monitored_source_id TEXT NOT NULL DEFAULT '', origin TEXT NOT NULL DEFAULT 'retrieved', provider TEXT NOT NULL DEFAULT '', enrich_state TEXT NOT NULL DEFAULT 'PENDING', enrich_state_updated_at TEXT NOT NULL DEFAULT '', source_provider   TEXT    NOT NULL DEFAULT '', source_video_id   TEXT    NOT NULL DEFAULT '', source_channel_id TEXT    NOT NULL DEFAULT '', source_url        TEXT    NOT NULL DEFAULT '', start_ms          INTEGER NOT NULL DEFAULT 0, end_ms            INTEGER NOT NULL DEFAULT 0, original_language TEXT    NOT NULL DEFAULT '', title             TEXT    NOT NULL DEFAULT '', binary_sha256     TEXT    NOT NULL DEFAULT '', semantic_hash     TEXT    NOT NULL DEFAULT '', rights_status     TEXT    NOT NULL DEFAULT 'review_required', policy_version    TEXT    NOT NULL DEFAULT 'v1', asset_state TEXT NOT NULL DEFAULT 'DISCOVERED', license_basis TEXT NOT NULL DEFAULT '', owner_channel_id TEXT NOT NULL DEFAULT '', allowed_channels TEXT NOT NULL DEFAULT '[]', allowed_regions TEXT NOT NULL DEFAULT '[]', expires_at TEXT NOT NULL DEFAULT '', review_status TEXT NOT NULL DEFAULT 'none', asset_version  TEXT NOT NULL DEFAULT '', asset_location TEXT NOT NULL DEFAULT '', rendition      TEXT NOT NULL DEFAULT '', admin_version INTEGER NOT NULL DEFAULT 0, namespace TEXT NOT NULL DEFAULT '', asset_kind TEXT NOT NULL DEFAULT '', source_type TEXT NOT NULL DEFAULT '', semantic_role TEXT NOT NULL DEFAULT '', content_sha256 TEXT NOT NULL DEFAULT '');

INSERT INTO media_assets_289 (
    id,
    source,
    name,
    tags,
    tags_norm,
    duration_ms,
    url,
    media_type,
    local_path,
    relative_path,
    drive_file_id,
    drive_folder_id,
    drive_link,
    download_link,
    embedding_json,
    metadata_json,
    visual_embedding,
    transcript_embedding,
    created_at,
    updated_at,
    lifecycle_state,
    deleted_at,
    folder_id,
    parent_folder_id,
    folder_path,
    category,
    filename,
    error,
    phash,
    search_text,
    scene_type,
    quality_score,
    reuse_count,
    last_used_at,
    thumbnail_url,
    clip_page_url,
    width,
    height,
    group_name,
    search_terms,
    index_state,
    index_state_updated_at,
    collection_version,
    audio_embedding,
    language,
    youtube_video_id,
    youtube_url,
    start_time,
    end_time,
    workspace_id,
    channel_id,
    license,
    source_version,
    style,
    external_id,
    discovered_via,
    discovered_at,
    monitored_source_id,
    origin,
    provider,
    enrich_state,
    enrich_state_updated_at,
    source_provider,
    source_video_id,
    source_channel_id,
    source_url,
    start_ms,
    end_ms,
    original_language,
    title,
    binary_sha256,
    semantic_hash,
    rights_status,
    policy_version,
    asset_state,
    license_basis,
    owner_channel_id,
    allowed_channels,
    allowed_regions,
    expires_at,
    review_status,
    asset_version,
    asset_location,
    rendition,
    admin_version,
    namespace,
    asset_kind,
    source_type,
    semantic_role,
    content_sha256
)
SELECT
    id,
    source,
    name,
    tags,
    tags_norm,
    duration_ms,
    url,
    media_type,
    local_path,
    relative_path,
    drive_file_id,
    drive_folder_id,
    drive_link,
    download_link,
    embedding_json,
    metadata_json,
    visual_embedding,
    transcript_embedding,
    created_at,
    updated_at,
    lifecycle_state,
    deleted_at,
    folder_id,
    parent_folder_id,
    folder_path,
    category,
    filename,
    error,
    phash,
    search_text,
    scene_type,
    quality_score,
    reuse_count,
    last_used_at,
    thumbnail_url,
    clip_page_url,
    width,
    height,
    group_name,
    search_terms,
    index_state,
    index_state_updated_at,
    collection_version,
    audio_embedding,
    language,
    youtube_video_id,
    youtube_url,
    start_time,
    end_time,
    workspace_id,
    channel_id,
    license,
    source_version,
    style,
    external_id,
    discovered_via,
    discovered_at,
    monitored_source_id,
    origin,
    provider,
    enrich_state,
    enrich_state_updated_at,
    source_provider,
    source_video_id,
    source_channel_id,
    source_url,
    start_ms,
    end_ms,
    original_language,
    title,
    binary_sha256,
    semantic_hash,
    rights_status,
    policy_version,
    asset_state,
    license_basis,
    owner_channel_id,
    allowed_channels,
    allowed_regions,
    expires_at,
    review_status,
    asset_version,
    asset_location,
    rendition,
    admin_version,
    namespace,
    asset_kind,
    source_type,
    semantic_role,
    content_sha256
FROM media_assets;

DROP TABLE media_assets;

ALTER TABLE media_assets_289 RENAME TO media_assets;

CREATE INDEX IF NOT EXISTS idx_media_assets_asset_state ON media_assets(asset_state);
CREATE INDEX IF NOT EXISTS idx_media_assets_canonical_source
    ON media_assets (source_provider, source_video_id, source_channel_id)
    WHERE source_provider != '';
CREATE INDEX IF NOT EXISTS idx_media_assets_category  ON media_assets(category);
CREATE INDEX IF NOT EXISTS idx_media_assets_collection_version
    ON media_assets(collection_version);
CREATE INDEX IF NOT EXISTS idx_media_assets_content_sha256
    ON media_assets(content_sha256)
    WHERE content_sha256 != '';
CREATE INDEX IF NOT EXISTS idx_media_assets_discovered_via
    ON media_assets(discovered_via)
    WHERE discovered_via != '';
CREATE INDEX IF NOT EXISTS idx_media_assets_enrich_state
    ON media_assets(enrich_state);
CREATE INDEX IF NOT EXISTS idx_media_assets_enrich_state_scrape
    ON media_assets(enrich_state, enrich_state_updated_at)
    WHERE enrich_state IN ('PENDING','FAILED');
CREATE UNIQUE INDEX idx_media_assets_ext_discovered
    ON media_assets(external_id, discovered_via)
    WHERE external_id != '' AND discovered_via != '';
CREATE INDEX IF NOT EXISTS idx_media_assets_folder_id ON media_assets(folder_id);
CREATE INDEX IF NOT EXISTS idx_media_assets_index_state
    ON media_assets(index_state);
CREATE INDEX IF NOT EXISTS idx_media_assets_lifecycle ON media_assets(lifecycle_state);
CREATE INDEX IF NOT EXISTS idx_media_assets_lifecycle_state
  ON media_assets(lifecycle_state);
CREATE INDEX IF NOT EXISTS idx_media_assets_monitored_source_id
    ON media_assets(monitored_source_id)
    WHERE monitored_source_id != '';
CREATE INDEX IF NOT EXISTS idx_media_assets_namespace_kind
    ON media_assets(namespace, asset_kind, source_type);
CREATE INDEX IF NOT EXISTS idx_media_assets_origin
    ON media_assets(origin)
    WHERE origin != '';
CREATE INDEX IF NOT EXISTS idx_media_assets_youtube_video_id
  ON media_assets(json_extract(COALESCE(metadata_json, '{}'), '$.youtube_video_id'))
  WHERE json_extract(COALESCE(metadata_json, '{}'), '$.youtube_video_id') IS NOT NULL
    AND json_extract(COALESCE(metadata_json, '{}'), '$.youtube_video_id') != '';

CREATE TRIGGER IF NOT EXISTS trg_media_assets_asset_state_projection_guard
BEFORE UPDATE OF asset_state ON media_assets
WHEN NEW.asset_state != CASE
    WHEN NEW.lifecycle_state IN ('DELETED', 'INDEX_DELETED') OR NEW.index_state = 'DELETED' THEN 'FAILED_PERMANENT'
    WHEN NEW.index_state IN ('EMBEDDING_FAILED', 'INDEXING_FAILED') THEN 'FAILED_RETRYABLE'
    WHEN NEW.lifecycle_state IN ('STAGING', 'PREPARING', 'PROCESSING') THEN 'DISCOVERED'
    WHEN NEW.index_state = 'NOT_INDEXABLE' THEN 'UPLOADED'
    WHEN NEW.index_state = 'DISCOVERED' THEN 'DISCOVERED'
    WHEN NEW.index_state = 'EMBEDDING' THEN 'TRANSLATED'
    WHEN NEW.index_state IN ('EMBEDDED', 'INDEXING') THEN 'INDEX_PENDING'
    WHEN NEW.index_state = 'INDEXED' AND NEW.lifecycle_state = 'ACTIVE' THEN 'READY'
    WHEN NEW.index_state = 'INDEXED' THEN 'INDEXED'
    ELSE 'DISCOVERED'
END
BEGIN
    SELECT RAISE(ABORT, 'media_assets: asset_state is a derived projection');
END;
CREATE TRIGGER IF NOT EXISTS trg_media_assets_asset_state_projection_insert
AFTER INSERT ON media_assets
BEGIN
    UPDATE media_assets
    SET asset_state = CASE
        WHEN NEW.lifecycle_state IN ('DELETED', 'INDEX_DELETED') OR NEW.index_state = 'DELETED' THEN 'FAILED_PERMANENT'
        WHEN NEW.index_state IN ('EMBEDDING_FAILED', 'INDEXING_FAILED') THEN 'FAILED_RETRYABLE'
        WHEN NEW.lifecycle_state IN ('STAGING', 'PREPARING', 'PROCESSING') THEN 'DISCOVERED'
        WHEN NEW.index_state = 'NOT_INDEXABLE' THEN 'UPLOADED'
        WHEN NEW.index_state = 'DISCOVERED' THEN 'DISCOVERED'
        WHEN NEW.index_state = 'EMBEDDING' THEN 'TRANSLATED'
        WHEN NEW.index_state IN ('EMBEDDED', 'INDEXING') THEN 'INDEX_PENDING'
        WHEN NEW.index_state = 'INDEXED' AND NEW.lifecycle_state = 'ACTIVE' THEN 'READY'
        WHEN NEW.index_state = 'INDEXED' THEN 'INDEXED'
        ELSE 'DISCOVERED'
    END
    WHERE id = NEW.id;
END;
CREATE TRIGGER IF NOT EXISTS trg_media_assets_asset_state_projection_update
AFTER UPDATE OF lifecycle_state, index_state ON media_assets
BEGIN
    UPDATE media_assets
    SET asset_state = CASE
        WHEN NEW.lifecycle_state IN ('DELETED', 'INDEX_DELETED') OR NEW.index_state = 'DELETED' THEN 'FAILED_PERMANENT'
        WHEN NEW.index_state IN ('EMBEDDING_FAILED', 'INDEXING_FAILED') THEN 'FAILED_RETRYABLE'
        WHEN NEW.lifecycle_state IN ('STAGING', 'PREPARING', 'PROCESSING') THEN 'DISCOVERED'
        WHEN NEW.index_state = 'NOT_INDEXABLE' THEN 'UPLOADED'
        WHEN NEW.index_state = 'DISCOVERED' THEN 'DISCOVERED'
        WHEN NEW.index_state = 'EMBEDDING' THEN 'TRANSLATED'
        WHEN NEW.index_state IN ('EMBEDDED', 'INDEXING') THEN 'INDEX_PENDING'
        WHEN NEW.index_state = 'INDEXED' AND NEW.lifecycle_state = 'ACTIVE' THEN 'READY'
        WHEN NEW.index_state = 'INDEXED' THEN 'INDEXED'
        ELSE 'DISCOVERED'
    END
    WHERE id = NEW.id;
END;
CREATE TRIGGER IF NOT EXISTS trg_media_assets_state_valid_insert
BEFORE INSERT ON media_assets
WHEN NEW.lifecycle_state NOT IN (
    'PREPARING','PUBLISHED','STAGING','PROCESSING','ACTIVE',
    'DELETE_PENDING','DELETE_REQUESTED','DRIVE_DELETE_PENDING',
    'DRIVE_DELETED','INDEX_DELETE_PENDING','INDEX_DELETED','DELETED','ERROR'
)
OR NEW.index_state NOT IN (
    'NOT_INDEXABLE','DISCOVERED','EMBEDDING','EMBEDDED','INDEXING','INDEXED',
    'EMBEDDING_FAILED','INDEXING_FAILED','INDEXING_SKIPPED_NO_INDEXER',
    'DELETE_PENDING','DELETED'
)
BEGIN
    SELECT RAISE(ABORT, 'media_assets: invalid lifecycle_state or index_state');
END;
CREATE TRIGGER IF NOT EXISTS trg_media_assets_state_valid_update
BEFORE UPDATE OF lifecycle_state, index_state ON media_assets
WHEN NEW.lifecycle_state NOT IN (
    'PREPARING','PUBLISHED','STAGING','PROCESSING','ACTIVE',
    'DELETE_PENDING','DELETE_REQUESTED','DRIVE_DELETE_PENDING',
    'DRIVE_DELETED','INDEX_DELETE_PENDING','INDEX_DELETED','DELETED','ERROR'
)
OR NEW.index_state NOT IN (
    'NOT_INDEXABLE','DISCOVERED','EMBEDDING','EMBEDDED','INDEXING','INDEXED',
    'EMBEDDING_FAILED','INDEXING_FAILED','INDEXING_SKIPPED_NO_INDEXER',
    'DELETE_PENDING','DELETED'
)
BEGIN
    SELECT RAISE(ABORT, 'media_assets: invalid lifecycle_state or index_state');
END;
