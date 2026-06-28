-- database: primary
-- TODO-8-SCOPE-FLAG-RECONCILE-109
-- Migration 109 — Add media_assets discovery columns (Wave CONFORMANCE-001 / id-24)
-- See sibling docs/architecture/godlike/migration_109_rationale.md for full rationale.
-- Apply via `sqlite3 data/media/media.db.sqlite < 109_*.sql`; ledger row written below.
--
-- TODO #8 (June 2026): the TODO-8-SCOPE-FLAG-RECONCILE-109 marker above is
-- REQUIRED for the one-time checksum shim to fire. The shim (in
-- migrateAll, gated on `m.version == 109 && targetDB == "primary"`)
-- rewrites the recorded SHA-256 in the schema_migrations ledger when
-- the file's new content carries this marker. Without the marker, an
-- unexpected modify of migration 109 surfaces as a hard error
-- ("migrations must never be modified") so the SHA-256 ledger
-- invariant is preserved.
--
-- Note on SQLite compatibility: `ALTER TABLE … ADD COLUMN` does NOT
-- support `IF NOT EXISTS` in SQLite. The statements below have the
-- token dropped; the runner's `isDuplicateColumnError` soft-skip
-- handles the reapply case (a re-applied 109 hits "duplicate column
-- name" on the already-existing columns and is silently skipped).

ALTER TABLE media_assets ADD COLUMN external_id         TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN discovered_via      TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN discovered_at       TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN monitored_source_id TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_media_assets_ext_discovered
    ON media_assets(external_id, discovered_via)
    WHERE external_id != '' AND discovered_via != '';

CREATE INDEX IF NOT EXISTS idx_media_assets_discovered_via
    ON media_assets(discovered_via)
    WHERE discovered_via != '';

CREATE INDEX IF NOT EXISTS idx_media_assets_monitored_source_id
    ON media_assets(monitored_source_id)
    WHERE monitored_source_id != '';
