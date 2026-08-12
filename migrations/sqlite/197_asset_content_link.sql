-- database: primary
-- Migration 197: link logical assets to physical CAS content.
--
-- CAS design (August 2026): the SHA-256 of the bytes is the PRIMARY KEY of
-- every immutable content object (content_objects, migration 194). Logical
-- media_assets reference the physical bytes through content_sha256; many
-- logical assets can share ONE content object (global byte deduplication).
--
-- media_asset_sources records PROVENANCE: the same bytes can be discovered
-- via multiple independent sources (Drive file, YouTube URL, Artlist asset,
-- manual upload). Provenance NEVER establishes identity — SHA-256 does —
-- but it must not be lost when bytes are deduplicated (the registry keeps
-- the full audit trail of where content came from).
--
-- Table naming note: the legacy artifact-registry provenance table
-- `asset_sources` already exists (migration 054, replaced by
-- artifact_sources in 035 but never dropped). This registry is scoped to
-- media_assets, so it uses the unambiguous name media_asset_sources.
--
-- Invariants:
--   * media_assets.content_sha256 references content_objects.sha256;
--     enforcement is application-layer (repo convention, see migration 149).
--   * media_asset_sources.source_id is deterministic (sha256 over asset_id +
--     source_type + source_uri + source_version) so re-registering the same
--     source is an idempotent upsert (source_identity_registry contract).
--   * a source row may carry content_sha256 even before the owning asset is
--     linked (early discovery during ingest).
--
-- Idempotency: ALTER TABLE ADD COLUMN is one-shot DDL (migrations run once,
-- versioned); tables and indexes use IF NOT EXISTS.

ALTER TABLE media_assets ADD COLUMN content_sha256 TEXT NOT NULL DEFAULT '';

-- Partial index: only rows that actually link content need index space.
CREATE INDEX IF NOT EXISTS idx_media_assets_content_sha256
    ON media_assets(content_sha256)
    WHERE content_sha256 != '';

CREATE TABLE IF NOT EXISTS media_asset_sources (
    source_id      TEXT PRIMARY KEY,
    asset_id       TEXT NOT NULL,
    content_sha256 TEXT NOT NULL DEFAULT '',
    source_type    TEXT NOT NULL,
    source_uri     TEXT NOT NULL,
    source_version TEXT NOT NULL DEFAULT '',
    discovered_at  TEXT NOT NULL,
    is_primary     INTEGER NOT NULL DEFAULT 0
);

-- Read pattern: "all provenance for this asset" (SourcesForAsset).
CREATE INDEX IF NOT EXISTS idx_media_asset_sources_asset_id
    ON media_asset_sources(asset_id);

-- Read pattern: "which sources map to this content object" (reverse lookup
-- for the CAS integrity scanner / provenance audit).
CREATE INDEX IF NOT EXISTS idx_media_asset_sources_content_sha256
    ON media_asset_sources(content_sha256)
    WHERE content_sha256 != '';
