-- 001_media_schema.sql
-- PostgreSQL + pgvector media-domain SSOT — canonical transactional core.
-- Apply only to the dedicated media database (pipelinegen_media).
--
-- godlike/06 SSOT: this schema mirrors the canonical SQLite media surfaces
-- (media_assets, asset_locations, outbox_events, media_asset_sources,
-- registry_events, asset_text_tracks) COLUMN-FOR-COLUMN so the
-- PostgresMediaCommitter can implement persistence.AssetCommitter with
-- behavioral parity to SQLiteAssetCommitter (parity suite:
-- internal/platform/postgres/media/parity_test.go).
--
-- Schema evolution is deliberately conservative:
--   - `id` remains the media_assets primary key (SQLite parity: the
--     percheck_media_assets_writer_canonical gate matches writes to the
--     `media_assets` table and the canonical writer family SQL stays
--     mirror-identical between engines).
--   - SQLite TEXT timestamps are kept as TEXT (RFC 3339 strings, UTC) so
--     the canonical writer formats time identically on both engines.
--   - SQLite INTEGER booleans stay SMALLINT with CHECK constraints in {0,1}.
--   - Tags stay TEXT JSON ('[]' default) exactly like SQLite tags/tags_norm;
--     the derived TEXT[]/GIN surface ships in 002_media_vector_surfaces.sql.
--
-- Derived / vector surfaces (media_asset_features, media_embeddings) live in
-- 002 so the transactional core is independently verifiable.
--
-- Every statement is idempotent (IF NOT EXISTS): re-applying on a populated
-- database is a no-op, mirroring the fail-closed idempotency of the SQLite
-- migration chain.

CREATE EXTENSION IF NOT EXISTS vector;

-- ── media_assets ────────────────────────────────────────────────────────
-- Mirrors the canonical SQLite media_assets projection written by
-- SQLiteAssetCommitter.CommitTxRaw (internal/platform/sqlite/assets/
-- imagesregistry/asset_committer.go). Columns are the union of the
-- canonical INSERT projection and every ALTER TABLE ADD COLUMN migration.
CREATE TABLE IF NOT EXISTS media_assets (
    id                      TEXT PRIMARY KEY,
    source                  TEXT NOT NULL DEFAULT '',
    name                    TEXT NOT NULL DEFAULT '',
    filename                TEXT NOT NULL DEFAULT '',
    media_type              TEXT NOT NULL DEFAULT '',
    category                TEXT NOT NULL DEFAULT '',
    group_name              TEXT NOT NULL DEFAULT '',
    duration_ms             BIGINT NOT NULL DEFAULT 0,
    tags                    TEXT NOT NULL DEFAULT '[]',
    tags_norm               TEXT NOT NULL DEFAULT '[]',
    -- Identity / delivery projection columns (SQLite parity: drive_file_id,
    -- drive_link, download_link, local_path mirror the primary location and
    -- are deprecated in favour of asset_locations).
    drive_file_id           TEXT NOT NULL DEFAULT '',
    drive_link              TEXT NOT NULL DEFAULT '',
    download_link           TEXT NOT NULL DEFAULT '',
    local_path              TEXT NOT NULL DEFAULT '',
    legacy_file_md5         TEXT NOT NULL DEFAULT '',
    binary_sha256           TEXT NOT NULL DEFAULT '',
    content_sha256          TEXT NOT NULL DEFAULT '',
    folder_id               TEXT NOT NULL DEFAULT '',
    parent_folder_id        TEXT NOT NULL DEFAULT '',
    folder_path             TEXT NOT NULL DEFAULT '',
    lifecycle_state         TEXT NOT NULL DEFAULT 'ACTIVE',
    deleted_at              TEXT NOT NULL DEFAULT '',
    index_state             TEXT NOT NULL DEFAULT 'DISCOVERED',
    index_state_updated_at  TEXT NOT NULL DEFAULT '',
    collection_version      TEXT NOT NULL DEFAULT '',
    metadata_json           TEXT NOT NULL DEFAULT '{}',
    search_text             TEXT NOT NULL DEFAULT '',
    search_terms            TEXT NOT NULL DEFAULT '',
    thumbnail_url           TEXT NOT NULL DEFAULT '',
    thumb_url               TEXT NOT NULL DEFAULT '',
    url                     TEXT NOT NULL DEFAULT '',
    clip_page_url           TEXT NOT NULL DEFAULT '',
    asset_version           TEXT NOT NULL DEFAULT '',
    asset_location          TEXT NOT NULL DEFAULT '',
    rendition               TEXT NOT NULL DEFAULT '',
    source_version          TEXT NOT NULL DEFAULT '',
    source_provider         TEXT NOT NULL DEFAULT '',
    source_video_id         TEXT NOT NULL DEFAULT '',
    source_channel_id       TEXT NOT NULL DEFAULT '',
    source_url              TEXT NOT NULL DEFAULT '',
    start_ms                BIGINT NOT NULL DEFAULT 0,
    end_ms                  BIGINT NOT NULL DEFAULT 0,
    title                   TEXT NOT NULL DEFAULT '',
    origin                  TEXT NOT NULL DEFAULT 'retrieved',
    provider                TEXT NOT NULL DEFAULT '',
    language                TEXT NOT NULL DEFAULT '',
    original_language       TEXT NOT NULL DEFAULT '',
    width                   INTEGER NOT NULL DEFAULT 0,
    height                  INTEGER NOT NULL DEFAULT 0,
    phash                   TEXT NOT NULL DEFAULT '',
    scene_type              TEXT NOT NULL DEFAULT '',
    quality_score           REAL NOT NULL DEFAULT 0.0,
    reuse_count             INTEGER NOT NULL DEFAULT 0,
    last_used_at            TEXT NOT NULL DEFAULT '',
    relative_path           TEXT NOT NULL DEFAULT '',
    -- Enrichment state machine (migration 109/219 parity).
    enrich_state            TEXT NOT NULL DEFAULT 'PENDING',
    enrich_state_updated_at TEXT NOT NULL DEFAULT '',
    -- YouTube discovery projection.
    youtube_video_id        TEXT NOT NULL DEFAULT '',
    youtube_url             TEXT NOT NULL DEFAULT '',
    start_time              TEXT NOT NULL DEFAULT '',
    end_time                TEXT NOT NULL DEFAULT '',
    workspace_id            TEXT NOT NULL DEFAULT '',
    channel_id              TEXT NOT NULL DEFAULT '',
    license                 TEXT NOT NULL DEFAULT '',
    style                   TEXT NOT NULL DEFAULT '',
    external_id             TEXT NOT NULL DEFAULT '',
    discovered_via          TEXT NOT NULL DEFAULT '',
    discovered_at           TEXT NOT NULL DEFAULT '',
    monitored_source_id     TEXT NOT NULL DEFAULT '',
    -- Governance / rights (SQLite parity defaults).
    semantic_hash           TEXT NOT NULL DEFAULT '',
    rights_status           TEXT NOT NULL DEFAULT 'review_required',
    policy_version          TEXT NOT NULL DEFAULT 'v1',
    lifecycle_status        TEXT NOT NULL DEFAULT 'ACTIVE',
    asset_state             TEXT NOT NULL DEFAULT 'DISCOVERED',
    license_basis           TEXT NOT NULL DEFAULT '',
    owner_channel_id        TEXT NOT NULL DEFAULT '',
    allowed_channels        TEXT NOT NULL DEFAULT '[]',
    allowed_regions         TEXT NOT NULL DEFAULT '[]',
    expires_at              TEXT NOT NULL DEFAULT '',
    review_status           TEXT NOT NULL DEFAULT 'none',
    admin_version           INTEGER NOT NULL DEFAULT 0,
    -- Canonical taxonomy dimensions (written in the SAME upsert as the row).
    namespace               TEXT NOT NULL DEFAULT '',
    asset_kind              TEXT NOT NULL DEFAULT '',
    source_type             TEXT NOT NULL DEFAULT '',
    semantic_role           TEXT NOT NULL DEFAULT '',
    -- Embedding JSON channel columns (SQLite parity; pgvector surfaces in 002).
    embedding_json          TEXT NOT NULL DEFAULT '[]',
    transcript_embedding    TEXT NOT NULL DEFAULT '[]',
    visual_embedding        TEXT NOT NULL DEFAULT '[]',
    audio_embedding         TEXT NOT NULL DEFAULT '[]',
    created_at              TEXT NOT NULL DEFAULT '',
    updated_at              TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_media_assets_category
    ON media_assets (category);
CREATE INDEX IF NOT EXISTS idx_media_assets_duration
    ON media_assets (duration_ms);
CREATE INDEX IF NOT EXISTS idx_media_assets_source_provider
    ON media_assets (source_provider);
CREATE INDEX IF NOT EXISTS idx_media_assets_category_duration
    ON media_assets (category, duration_ms);
CREATE INDEX IF NOT EXISTS idx_media_assets_lifecycle_state
    ON media_assets (lifecycle_state);
CREATE INDEX IF NOT EXISTS idx_media_assets_index_state
    ON media_assets (index_state);
CREATE INDEX IF NOT EXISTS idx_media_assets_legacy_file_md5
    ON media_assets (legacy_file_md5);
CREATE INDEX IF NOT EXISTS idx_media_assets_content_sha256
    ON media_assets (content_sha256);

-- ── asset_locations ─────────────────────────────────────────────────────
-- Mirrors the canonical SQLite asset_locations (migration 055 + 061) with
-- the PRIMARY KEY (asset_id, location_kind) conflict target used by the
-- canonical upsert.
CREATE TABLE IF NOT EXISTS asset_locations (
    id              BIGSERIAL PRIMARY KEY,
    asset_id        TEXT NOT NULL REFERENCES media_assets(id) ON DELETE CASCADE,
    location_kind   TEXT NOT NULL
                    CHECK (location_kind IN ('local', 'drive', 'object_storage')),
    uri             TEXT NOT NULL DEFAULT '',
    external_id     TEXT NOT NULL DEFAULT '',
    web_view_link   TEXT NOT NULL DEFAULT '',
    download_url    TEXT NOT NULL DEFAULT '',
    mime_type       TEXT NOT NULL DEFAULT '',
    file_size_bytes BIGINT NOT NULL DEFAULT 0,
    legacy_file_md5 TEXT NOT NULL DEFAULT '',
    is_primary      SMALLINT NOT NULL DEFAULT 0
                    CHECK (is_primary IN (0, 1)),
    created_at      TEXT NOT NULL DEFAULT '',
    updated_at      TEXT NOT NULL DEFAULT '',
    UNIQUE (asset_id, location_kind)
);

CREATE INDEX IF NOT EXISTS idx_asset_locations_asset
    ON asset_locations (asset_id);
CREATE INDEX IF NOT EXISTS idx_asset_locations_primary
    ON asset_locations (asset_id)
    WHERE is_primary = 1;

-- ── outbox_events ───────────────────────────────────────────────────────
-- Mirrors the canonical SQLite outbox_events (migration 092 + 186) so the
-- Postgres outbox adapter keeps the same idempotency key contract
-- (unique non-empty event_key) and priority-aware ClaimNext ordering
-- (priority DESC, next_attempt_at ASC, id ASC).
CREATE TABLE IF NOT EXISTS outbox_events (
    id              BIGSERIAL PRIMARY KEY,
    event_type      TEXT NOT NULL,
    aggregate_id    TEXT NOT NULL DEFAULT '',
    aggregate_type  TEXT NOT NULL DEFAULT '',
    payload_json    TEXT NOT NULL DEFAULT '',
    event_key       TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'pending',
    attempt_count   INTEGER NOT NULL DEFAULT 0,
    max_attempts    INTEGER NOT NULL DEFAULT 10,
    last_error      TEXT NOT NULL DEFAULT '',
    next_attempt_at TEXT,
    worker_id       TEXT NOT NULL DEFAULT '',
    lease_id        TEXT NOT NULL DEFAULT '',
    lease_expiry    TEXT,
    completed_at    TEXT,
    priority        INTEGER NOT NULL DEFAULT 5,
    created_at      TEXT NOT NULL DEFAULT '',
    updated_at      TEXT NOT NULL DEFAULT ''
);

-- Partial unique index (SQLite parity intent): non-empty event_key rows are
-- idempotent-arbitered; empty event_key rows (one-shot inserts) never conflict.
-- The Enqueue conflict target `ON CONFLICT (event_key) WHERE event_key <> ''`
-- matches this partial index exactly (PostgreSQL arbiter-index semantics).
CREATE UNIQUE INDEX IF NOT EXISTS ux_outbox_events_event_key
    ON outbox_events (event_key)
    WHERE event_key <> '';
CREATE INDEX IF NOT EXISTS idx_outbox_events_status_next_attempt
    ON outbox_events (status, next_attempt_at, id);
CREATE INDEX IF NOT EXISTS idx_outbox_events_status_priority
    ON outbox_events (status, priority DESC, next_attempt_at ASC, id ASC);

-- ── media_asset_sources ─────────────────────────────────────────────────
-- Provenance ledger (migration 200 parity): deterministic canonical source
-- id primary key, idempotent upsert, primary-first discovery order.
CREATE TABLE IF NOT EXISTS media_asset_sources (
    source_id      TEXT PRIMARY KEY,
    asset_id       TEXT NOT NULL,
    content_sha256 TEXT NOT NULL DEFAULT '',
    source_type    TEXT NOT NULL DEFAULT '',
    source_uri     TEXT NOT NULL DEFAULT '',
    source_version TEXT NOT NULL DEFAULT '',
    discovered_at  TEXT NOT NULL DEFAULT '',
    is_primary     SMALLINT NOT NULL DEFAULT 0
                   CHECK (is_primary IN (0, 1))
);

CREATE INDEX IF NOT EXISTS idx_media_asset_sources_asset
    ON media_asset_sources (asset_id);

-- ── registry_events ─────────────────────────────────────────────────────
-- Registry event ledger (SQLite parity): AUTOINCREMENT seq becomes
-- BIGSERIAL; deterministic event_id keeps replays idempotent (ON CONFLICT
-- DO NOTHING preserves the original seq).
CREATE TABLE IF NOT EXISTS registry_events (
    seq          BIGSERIAL PRIMARY KEY,
    event_id     TEXT NOT NULL UNIQUE,
    asset_id     TEXT,
    event_type   TEXT NOT NULL,
    run_id       TEXT,
    actor        TEXT NOT NULL DEFAULT '',
    before_hash  TEXT NOT NULL DEFAULT '',
    after_hash   TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL DEFAULT '{}',
    git_sha      TEXT NOT NULL DEFAULT '',
    app_version  TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_registry_events_asset
    ON registry_events (asset_id, created_at);
CREATE INDEX IF NOT EXISTS idx_registry_events_run
    ON registry_events (run_id, created_at);

-- ── asset_text_tracks ───────────────────────────────────────────────────
-- Transcript/text-track surface (migrations 137 + 155 parity): the partial
-- UNIQUE current-row index maps to a partial unique index in PostgreSQL.
CREATE TABLE IF NOT EXISTS asset_text_tracks (
    id                   BIGSERIAL PRIMARY KEY,
    asset_id             TEXT NOT NULL,
    language_code        TEXT NOT NULL,
    text_kind            TEXT NOT NULL,
    text_content         TEXT NOT NULL DEFAULT '',
    source_type          TEXT NOT NULL DEFAULT 'provided',
    source_language_code TEXT NOT NULL DEFAULT '',
    is_original          SMALLINT NOT NULL DEFAULT 0
                         CHECK (is_original IN (0, 1)),
    provider             TEXT NOT NULL DEFAULT '',
    model_name           TEXT NOT NULL DEFAULT '',
    model_version        TEXT NOT NULL DEFAULT '',
    prompt_version       TEXT NOT NULL DEFAULT '',
    text_hash            TEXT NOT NULL DEFAULT '',
    source_version       TEXT NOT NULL DEFAULT '',
    translation_key      TEXT NOT NULL DEFAULT '',
    is_current           SMALLINT NOT NULL DEFAULT 1
                         CHECK (is_current IN (0, 1)),
    source_track_id      INTEGER,
    source_text_hash     TEXT NOT NULL DEFAULT '',
    confidence           REAL,
    status               TEXT NOT NULL DEFAULT 'READY',
    created_at           TEXT NOT NULL DEFAULT '',
    updated_at           TEXT NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_asset_text_tracks_current
    ON asset_text_tracks (asset_id, language_code, text_kind)
    WHERE is_current = 1;
CREATE INDEX IF NOT EXISTS idx_asset_text_tracks_asset
    ON asset_text_tracks (asset_id);

-- ── asset_renditions ─────────────────────────────────────────────────────
-- Technical renditions (migration 141 parity): one row per rendition kind
-- linked to its physical asset_locations row. The UNIQUE (asset_id, kind)
-- constraint is the ON CONFLICT arbiter for the canonical rendition upsert.
CREATE TABLE IF NOT EXISTS asset_renditions (
    id          TEXT PRIMARY KEY,
    asset_id    TEXT NOT NULL,
    location_id BIGINT,
    kind        TEXT NOT NULL DEFAULT 'master',
    container   TEXT,
    codec       TEXT,
    width       INTEGER,
    height      INTEGER,
    fps         REAL,
    bitrate     INTEGER,
    color_space TEXT,
    sha256      TEXT,
    size_bytes  BIGINT NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL DEFAULT '',
    updated_at  TEXT NOT NULL DEFAULT '',
    UNIQUE (asset_id, kind)
);

CREATE INDEX IF NOT EXISTS idx_asset_renditions_asset
    ON asset_renditions (asset_id);
CREATE INDEX IF NOT EXISTS idx_asset_renditions_location
    ON asset_renditions (location_id);
CREATE INDEX IF NOT EXISTS idx_asset_renditions_kind
    ON asset_renditions (kind);
