-- 096_add_media_assets_collection_version.sql
--
-- PR5 (QDRANT-005 followup commit): promote the legacy
-- metadata_json.$.collection_version sidecar to a first-class
-- media_assets.collection_version column. Migration 094's header
-- doc-comment explicitly deferred this promotion:
--   "Promoting collection_version is a planned QDRANT-005 followup;
--    it is touched here by the backfill so the column-shaped
--    equivalent does not diverge from the sidecar."
-- This migration closes that followup.
--
-- Why a column instead of metadata_json:
--   1. Dashboard & IndexHealth queries stop paying the
--      `json_extract(metadata_json, '$.collection_version')` cost
--      on every media_assets scan (mirrors the rationale from 094).
--   2. Qdrant collection alias bindings can be enforced via CHECK
--      constraint at the column level, not at the JSON path.
--   3. Reindex-by-version tooling can scan the column at the
--      `media_assets` level (QDRANT-005 housekeeping), instead of
--      full-table scan of metadata_json.
--
-- Companion code lands in this same PR5 commit body (NOT as part
-- of this migration file — keep migration narrowly-scoped):
--   internal/domain/asset/index_state.go
--     Already includes the canonical 7-state IndexState enum
--     (DISCOVERED/INDEX_PENDING/INDEXING/INDEXED/INDEX_FAILED/
--      DELETE_PENDING/DELETED) shipped with migration 094. The
--     collection_version promotion does NOT widen that enum — it's
--     an orthogonal dimension recorded in its own column.
--   internal/platform/sqlite/assets/clips_repository.go
--     The PR6-era SetIndexStateTx method writes both
--     index_state + index_state_updated_at. PR5 adds a thin
--     SetCollectionVersion method to mirror the pattern, but only
--     if application needs demand it (kept out of scope for the
--     migration-only commit).
--
-- Backfill behaviour:
--   - ALTER TABLE adds the column with DEFAULT '' (empty string).
--     Every existing row immediately holds `collection_version = ''`
--     (i.e. "version unknown / pre-versioning" sentinel).
--   - The UPDATE copies any legacy metadata_json.$.collection_version
--     value into the column for rows still holding the sentinel.
--   - WHERE clause restricts the write to rows still at the sentinel
--     so re-applying the migration on a fresh snapshot restores the
--     sidecar values idempotently.
--   - For most production rows, metadata_json.$.collection_version
--     is missing/empty (the sidecar was only emitted by recent
--     QDRANT-003 writers), so the UPDATE is effectively a no-op for
--     those — they keep `''`. Authors of operational tooling that
--     reorder Qdrant collection aliases must read this column
--     alongside media_assets.embedded_at / indexed_content_hash.
--
-- Idempotency: on a re-applied migration the WHERE clause
-- (`collection_version = ''`) plus the
-- `json_extract(COALESCE(metadata_json,'{}'), '$.collection_version')`
-- copy guard prevents the sidecar from being clobbered by a
-- subsequent sweep. The CREATE INDEX is IF NOT EXISTS.
--
-- Column ordering rationale: collection_version sits AT THE END of
-- media_assets to keep the ALTER idempotent on databases where the
-- column was once inlined mid-block; SQLite ALTER TABLE only
-- appends new columns, so placing this at the tail matches the
-- natural on-disk layout.

ALTER TABLE media_assets ADD COLUMN collection_version TEXT NOT NULL DEFAULT '';

-- Backfill: copy sidecar metadata_json.$.collection_version into
-- the new column for rows still at the empty-string sentinel.
-- COALESCE guards against rows that have NULL metadata_json (the
-- canonical schema stores 'NULL' as the literal string 'NULL' but
-- the legacy migrations pre-073 used real NULL — be defensive).
UPDATE media_assets
SET collection_version = COALESCE(
        json_extract(COALESCE(metadata_json, '{}'), '$.collection_version'),
        ''
    )
WHERE collection_version = '';

-- Index for query-by-version housekeeping tools (QDRANT-005
-- reconciliation). Conditional so reapplying is a no-op.
CREATE INDEX IF NOT EXISTS idx_media_assets_collection_version
    ON media_assets(collection_version);
