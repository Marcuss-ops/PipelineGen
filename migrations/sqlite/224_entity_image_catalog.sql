-- database: primary
-- 224_entity_image_catalog.sql
-- Durable Entity Image Catalog for canonical PERSON identities.
--
-- This catalog is intentionally separate from vidrush_provider_cache:
-- provider_cache is a temporary 48-hour response cache, while these rows
-- remain usable when stale and preserve the entity-to-candidate relationship.
-- Materialization metadata is a separate table so a remote candidate remains
-- stable even when its local/Drive representation changes.

CREATE TABLE IF NOT EXISTS entity_image_catalog_entities (
    canonical_entity_id TEXT PRIMARY KEY
        CHECK (canonical_entity_id LIKE 'person:%'),
    entity_type         TEXT NOT NULL DEFAULT 'PERSON'
        CHECK (entity_type = 'PERSON'),
    canonical_name      TEXT NOT NULL,
    first_seen_at       TEXT NOT NULL DEFAULT (datetime('now')),
    last_seen_at        TEXT NOT NULL DEFAULT (datetime('now')),
    last_refresh_at     TEXT NOT NULL DEFAULT '',
    refresh_status      TEXT NOT NULL DEFAULT 'never'
        CHECK (refresh_status IN ('never', 'running', 'succeeded', 'failed')),
    last_error          TEXT NOT NULL DEFAULT '',
    created_at          TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at          TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_entity_image_catalog_entities_refresh
    ON entity_image_catalog_entities(refresh_status, last_refresh_at);

CREATE TABLE IF NOT EXISTS entity_image_catalog_candidates (
    candidate_id        INTEGER PRIMARY KEY AUTOINCREMENT,
    canonical_entity_id TEXT NOT NULL,
    provider            TEXT NOT NULL,
    rank                INTEGER NOT NULL CHECK (rank >= 1),
    source_url          TEXT NOT NULL,
    thumbnail_url       TEXT NOT NULL DEFAULT '',
    width               INTEGER NOT NULL DEFAULT 0 CHECK (width >= 0),
    height              INTEGER NOT NULL DEFAULT 0 CHECK (height >= 0),
    status              TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'stale', 'broken', 'retired')),
    first_seen_at       TEXT NOT NULL DEFAULT (datetime('now')),
    last_seen_at        TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at          TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE (canonical_entity_id, provider, source_url),
    FOREIGN KEY (canonical_entity_id)
        REFERENCES entity_image_catalog_entities(canonical_entity_id)
        ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_entity_image_catalog_candidates_lookup
    ON entity_image_catalog_candidates(canonical_entity_id, status, rank, candidate_id);

CREATE INDEX IF NOT EXISTS idx_entity_image_catalog_candidates_provider
    ON entity_image_catalog_candidates(provider, source_url);

CREATE TABLE IF NOT EXISTS entity_image_catalog_materializations (
    candidate_id    INTEGER PRIMARY KEY,
    asset_id        TEXT NOT NULL DEFAULT '',
    file_hash       TEXT NOT NULL DEFAULT '',
    drive_file_id   TEXT NOT NULL DEFAULT '',
    drive_link      TEXT NOT NULL DEFAULT '',
    local_path      TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'materialized', 'failed')),
    materialized_at TEXT NOT NULL DEFAULT '',
    last_verified_at TEXT NOT NULL DEFAULT '',
    last_error      TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (candidate_id)
        REFERENCES entity_image_catalog_candidates(candidate_id)
        ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_entity_image_catalog_materializations_asset
    ON entity_image_catalog_materializations(asset_id)
    WHERE asset_id != '';

CREATE INDEX IF NOT EXISTS idx_entity_image_catalog_materializations_drive
    ON entity_image_catalog_materializations(drive_file_id)
    WHERE drive_file_id != '';
