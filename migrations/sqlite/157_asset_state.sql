-- Migration 157 — Adds the AssetState explicit-state-machine column
-- to media_assets (PR-CATALOG-MULTILINGUA step 7, July 2026).
--
-- godlike/06 SSOT: this migration is the SOLE canonical owner of
-- the media_assets.asset_state column shape. The matching typed
-- enum is at internal/domain/asset/asset_state.go::AssetState
-- (14 UPPERCASE values: DISCOVERED, DOWNLOADED, NORMALIZED, HASHED,
-- UPLOADED, TRANSCRIBED, ENRICHED, TRANSLATED, INDEX_PENDING,
-- INDEXED, READY, READY_MULTILINGUAL, FAILED_RETRYABLE,
-- FAILED_PERMANENT). The 14 strings here must equal the column
-- default + the alphabet tested at TestAssetState_StringLiteralValues.
--
-- godlike/06 SSOT invariant: a future PR adding a 15th state MUST
-- update the column default, the IsValidTransition matrix, the
-- helper-methods test, AND the percheck_asset_state_canonical_14
-- archcheck pin. Drift in this alphabet silently breaks every
-- WHERE clause that compares against the database-stored column.
--
-- Forward-prevention: NO CHECK constraint at this stage. SQLite
-- ALTER TABLE ADD COLUMN does not support inline CHECK, and a
-- table recreation would block on existing rows that may carry
-- the upgrade window's default values. The CHECK constraint is
-- added by migration 158 after the backfill window has closed.
-- percheck_asset_state_canonical_14 enforces the 14-value
-- inventory at the type level; the DB CHECK is a belt-and-suspenders
-- defence.

ALTER TABLE media_assets ADD COLUMN asset_state TEXT NOT NULL DEFAULT 'DISCOVERED';

CREATE INDEX IF NOT EXISTS idx_media_assets_asset_state ON media_assets(asset_state);
