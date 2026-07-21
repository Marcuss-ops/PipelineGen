-- 166_mediamemory_candidates.sql — Fase 1.2 media_candidates SSOT.
--
-- godlike/06 SSOT (one canonical owner per fact): this is the
-- SOLE DDL for the media_candidates table. UNIQUE dedup is at
-- (provider, provider_asset_id) — the discovery worker produces
-- the row ONCE per upstream asset, and subsequent discovery
-- passes for the same logical asset update the same row.
--
-- godlike/07 NO-FAKE-AVAILABILITY + architecture doc section 14:
-- rights fields (rights_status, license_basis, allowed_channels,
-- allowed_regions, owner, expiration) carry the FULL envelope
-- required by the rights validator. A candidate with
-- rights_status='unknown' MUST NOT be promoted to Hot by the
-- AcquisitionPlanner (godlike/07 fail-closed at the cache
-- tiering boundary).
--
-- storage: allowed_channels + allowed_regions are JSON TEXT
-- arrays (the canonical Go shape is []string; the application
-- layer marshals). Serializing arrays as JSON keeps the SSOT
-- one canonical shape per attribute while avoiding SQLite
-- table-per-attribute normalization that would force a JOIN to
-- answer any single-candidate query.
--
-- hot/warm/cold: materialization_status is the canonical tier
-- (architecture doc section 8). Cold holds metadata only; Warm
-- has bytes on Drive; Hot has bytes staged locally. The
-- AcquisitionPlanner is the sole owner of the Cold→Warm→Hot
-- transitions; this table is the durable backing store.
--
-- asset_id is nullable: empty until the Linker worker
-- promotes the candidate to a media_assets row via the
-- canonical stockpipeline.

CREATE TABLE IF NOT EXISTS media_candidates (
    id                     TEXT     PRIMARY KEY,
    provider               TEXT     NOT NULL,
    provider_asset_id      TEXT     NOT NULL,
    source_url             TEXT     NOT NULL,
    thumbnail_url          TEXT,
    title                  TEXT,
    description            TEXT,
    duration_ms            INTEGER,
    candidate_score        REAL     NOT NULL DEFAULT 0,
    rights_status          TEXT     NOT NULL,
    license_basis          TEXT,
    allowed_channels       TEXT,
    allowed_regions        TEXT,
    owner                  TEXT,
    expiration             DATETIME,
    discovery_status       TEXT     NOT NULL,
    materialization_status TEXT     NOT NULL,
    asset_id               TEXT,
    created_at             DATETIME NOT NULL,
    updated_at             DATETIME NOT NULL,
    UNIQUE(provider, provider_asset_id)
);

CREATE INDEX IF NOT EXISTS idx_media_candidates_provider
    ON media_candidates(provider);

CREATE INDEX IF NOT EXISTS idx_media_candidates_mater
    ON media_candidates(materialization_status);

CREATE INDEX IF NOT EXISTS idx_media_candidates_rights
    ON media_candidates(rights_status);

CREATE INDEX IF NOT EXISTS idx_media_candidates_score
    ON media_candidates(candidate_score DESC);
