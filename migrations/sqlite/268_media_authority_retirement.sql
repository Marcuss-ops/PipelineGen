-- database: primary
-- 268_media_authority_retirement.sql — SQLite media authority retirement (P2-9, September 2026).
--
-- POSTGRES-MEDIA-CUTOVER: PostgreSQL is the SOLE durable authority for the
-- media domain (media_assets, asset_locations, asset_text_tracks,
-- media_asset_features, media_embeddings, registry_events, media outbox).
-- SQLite retains ONLY operational domains (jobs, delivery_log, scripts,
-- cache, idempotency, artifacts/staging, observability).
--
-- This migration does NOT drop historical media tables on already-migrated
-- databases — old rows remain for audit/recovery and for the incremental
-- migration path 001..267. It marks the cutover so that:
--   1. Fresh installs bootstrapped from the consolidated baseline understand
--      that SQLite media tables are legacy/quarantined (the authoritative
--      media schema lives in PostgreSQL migrations 001_media_schema.sql+).
--   2. Future SQLite migrations MUST NOT create or restore media-authoritative
--      tables (media_assets, asset_locations as media SSOT, asset_text_tracks
--      as media SSOT). New media state belongs in PostgreSQL.
--   3. A lightweight marker table lets runtime and CI gates assert that the
--      retirement has been applied (percheck_media_assets_writer_canonical).
--
-- Historical tables are intentionally left in place under their original
-- names on databases that already contain them; renaming them to
-- legacy_media_assets would break degraded-mode reads and the emergency
-- recover-registry-from-qdrant tool. The baseline regeneration (cmd/gen_baseline)
-- for fresh installs produced AFTER this cutover MUST be regenerated without
-- media-authoritative tables and bumped to 000_baseline_268.sql (see
-- BASELINE_PLAN.md Phase 2). Until that regeneration lands, this marker is
-- the fresh-install guard.
CREATE TABLE IF NOT EXISTS _media_authority_retirement (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    retired_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    note TEXT NOT NULL DEFAULT 'SQLite media authority retired — PostgreSQL media SSOT (P2-9, September 2026)'
);
INSERT OR IGNORE INTO _media_authority_retirement (id, note) VALUES (1, 'SQLite media authority retired — PostgreSQL media SSOT (P2-9, September 2026)');
