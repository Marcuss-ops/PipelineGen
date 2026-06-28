-- Migration 109 — Add media_assets discovery columns (Wave CONFORMANCE-001 / id-24)
-- See sibling docs/architecture/godlike/migration_109_rationale.md for full rationale.
-- Apply via `sqlite3 data/media/media.db.sqlite < 109_*.sql`; ledger row written below.

ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS external_id         TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS discovered_via      TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS discovered_at       TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS monitored_source_id TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_media_assets_ext_discovered
    ON media_assets(external_id, discovered_via)
    WHERE external_id != '' AND discovered_via != '';

CREATE INDEX IF NOT EXISTS idx_media_assets_discovered_via
    ON media_assets(discovered_via)
    WHERE discovered_via != '';

CREATE INDEX IF NOT EXISTS idx_media_assets_monitored_source_id
    ON media_assets(monitored_source_id)
    WHERE monitored_source_id != '';
