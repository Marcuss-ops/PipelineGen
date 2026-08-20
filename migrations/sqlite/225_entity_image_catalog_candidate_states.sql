-- database: primary
-- 225_entity_image_catalog_candidate_states.sql
-- Add the explicit fresh state while retaining legacy active rows.

PRAGMA foreign_keys = OFF;

DROP INDEX IF EXISTS idx_entity_image_catalog_candidates_lookup;
DROP INDEX IF EXISTS idx_entity_image_catalog_candidates_provider;
DROP INDEX IF EXISTS idx_entity_image_catalog_materializations_asset;
DROP INDEX IF EXISTS idx_entity_image_catalog_materializations_drive;

ALTER TABLE entity_image_catalog_materializations
    RENAME TO entity_image_catalog_materializations_v224;
ALTER TABLE entity_image_catalog_candidates
    RENAME TO entity_image_catalog_candidates_v224;

CREATE TABLE entity_image_catalog_candidates (
    candidate_id        INTEGER PRIMARY KEY AUTOINCREMENT,
    canonical_entity_id TEXT NOT NULL,
    provider            TEXT NOT NULL,
    rank                INTEGER NOT NULL CHECK (rank >= 1),
    source_url          TEXT NOT NULL,
    thumbnail_url       TEXT NOT NULL DEFAULT '',
    width               INTEGER NOT NULL DEFAULT 0 CHECK (width >= 0),
    height              INTEGER NOT NULL DEFAULT 0 CHECK (height >= 0),
    status              TEXT NOT NULL DEFAULT 'fresh'
        CHECK (status IN ('fresh', 'active', 'stale', 'broken', 'retired')),
    first_seen_at       TEXT NOT NULL DEFAULT (datetime('now')),
    last_seen_at        TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at          TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE (canonical_entity_id, provider, source_url),
    FOREIGN KEY (canonical_entity_id)
        REFERENCES entity_image_catalog_entities(canonical_entity_id)
        ON DELETE CASCADE
);

INSERT INTO entity_image_catalog_candidates (
    candidate_id, canonical_entity_id, provider, rank, source_url,
    thumbnail_url, width, height, status, first_seen_at, last_seen_at, updated_at
)
SELECT candidate_id, canonical_entity_id, provider, rank, source_url,
       thumbnail_url, width, height,
       CASE WHEN status = 'active' THEN 'fresh' ELSE status END,
       first_seen_at, last_seen_at, updated_at
FROM entity_image_catalog_candidates_v224;

CREATE TABLE entity_image_catalog_materializations (
    candidate_id     INTEGER PRIMARY KEY,
    asset_id         TEXT NOT NULL DEFAULT '',
    file_hash        TEXT NOT NULL DEFAULT '',
    drive_file_id    TEXT NOT NULL DEFAULT '',
    drive_link       TEXT NOT NULL DEFAULT '',
    local_path       TEXT NOT NULL DEFAULT '',
    status           TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'materialized', 'failed')),
    materialized_at  TEXT NOT NULL DEFAULT '',
    last_verified_at TEXT NOT NULL DEFAULT '',
    last_error       TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at       TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (candidate_id)
        REFERENCES entity_image_catalog_candidates(candidate_id)
        ON DELETE CASCADE
);

INSERT INTO entity_image_catalog_materializations (
    candidate_id, asset_id, file_hash, drive_file_id, drive_link, local_path,
    status, materialized_at, last_verified_at, last_error, created_at, updated_at
)
SELECT candidate_id, asset_id, file_hash, drive_file_id, drive_link, local_path,
       status, materialized_at, last_verified_at, last_error, created_at, updated_at
FROM entity_image_catalog_materializations_v224;

DROP TABLE entity_image_catalog_materializations_v224;
DROP TABLE entity_image_catalog_candidates_v224;

CREATE INDEX IF NOT EXISTS idx_entity_image_catalog_candidates_lookup
    ON entity_image_catalog_candidates(canonical_entity_id, status, rank, candidate_id);
CREATE INDEX IF NOT EXISTS idx_entity_image_catalog_candidates_provider
    ON entity_image_catalog_candidates(provider, source_url);
CREATE INDEX IF NOT EXISTS idx_entity_image_catalog_materializations_asset
    ON entity_image_catalog_materializations(asset_id)
    WHERE asset_id != '';
CREATE INDEX IF NOT EXISTS idx_entity_image_catalog_materializations_drive
    ON entity_image_catalog_materializations(drive_file_id)
    WHERE drive_file_id != '';

PRAGMA foreign_keys = ON;
