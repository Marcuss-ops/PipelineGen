-- migrations/sqlite/111_qdrant_collections.sql
--
-- TODO #8 (June 2026) — renumbered from 103_qdrant_collections.sql to
-- avoid collision with 103_create_voiceovers_table.sql. The original
-- 103 plan was renumbered out of the way; this file landed at 111.
-- The header now reflects the CURRENT filename so a future grep or
-- git blame matches reality.
-- PR 9 (June 2026) — feat/qdrant-operational-readiness.
-- Verdict Qdrant section #14: the previous retention semantics were a
-- binary switch on RetentionDays (>0 = drop everything old, ≤0 = no-op).
-- This migration introduces the qdrant_collections lifecycle table that
-- anchors real per-collection timestamps so RetentionDays can become a
-- REAL duration gate (time.Since(promoted_at) > RetentionDays).
--
-- Schema intent:
--   - One row per physical Qdrant collection that PipelineGen has ever
--     created (active + retired).
--   - status reflects the lifecycle phase: created -> indexed -> verified
--     -> promoted (active alias target) -> retired (dropped by retention).
--   - verification_hash pins the post-reindex SwitchReport so a future
--     reconcile can detect verifier regressions on the SAME collection.
--   - point_count is snapshot at the most recent verify/promote event;
--     drift detection uses this vs. the live Qdrant CountPoints read.
--   - promoted_at is the timestamp the alias LAST switched to this
--     collection — RetentionDays gates against this column specifically,
--     so an active target (promoted_at = recent) is NEVER eligible while
--     an old promoted-but-retired target IS eligible after the gate.
--
-- Indexing:
--   - collection_name UNIQUE: the canonical lookup key for the AgingTable
--     port (Reconciler/CleanupWithConfig reads it).
--   - status: queries by lifecycle state (retention skips 'active'/'reindexing').
--   - promoted_at: retention sweep range scans.
--
-- Migration is additive only — no renames, no column drops. Down-migrations
-- (if ever needed) should drop the table; no other migration references
-- these columns today.

CREATE TABLE IF NOT EXISTS qdrant_collections (
    -- collection_name: canonical physical collection name (e.g.
    --   "media_assets_v3_e5_768_siglip_768__ts_20260628_120314").
    -- UNIQUE so the AgingTable port can issue a single INSERT...ON CONFLICT
    -- pattern from concurrent reconciler runs without race conditions.
    collection_name        TEXT PRIMARY KEY,

    -- schema_version: the IndexSchema.Version this collection was built under
    -- (e.g. "v3"). Lets the retention sweep skip collections whose schema
    -- version does NOT match the current (different version = different
    -- physical-name prefix; not eligible under the schema prefix gate).
    schema_version         TEXT NOT NULL DEFAULT 'v3',

    -- created_at: when the physical collection was created (ISO8601 UTC).
    -- Set on first INSERT, never updated afterwards.
    created_at             TEXT NOT NULL,

    -- indexed_at: when the reindex write phase completed for this collection.
    -- Used by the aging gate as one of the alternative reference points:
    -- a collection that was indexed but never promoted (e.g. failed
    -- reindex verification) has indexed_at set but promoted_at NULL.
    indexed_at             TEXT,

    -- verified_at: when the post-reindex SwitchReport.Ready flipped true.
    -- The verifier stamps this via the reconciliation flow.
    verified_at            TEXT,

    -- promoted_at: when the runtime alias LAST switched to this collection.
    -- RetentionDays is computed against this column:
    --   eligible if time.Since(promoted_at) > RetentionDays*24h
    -- Nullable: a freshly-created collection that has NOT yet been
    -- promoted has promoted_at=NULL and is NEVER eligible (different
    -- gate than the time-since gate; explicit).
    promoted_at            TEXT,

    -- retired_at: when retention dropped this collection. Null while live.
    retired_at             TEXT,

    -- point_count: number of points observed at the most recent
    -- verify/promote event. The Reconciler compares this against the
    -- live Qdrant CountPoints; drift > tolerance flags the collection
    -- for repair rather than retention drop.
    point_count            INTEGER NOT NULL DEFAULT 0,

    -- verification_hash: SHA-256 of the canonical SwitchReport body at
    -- the verify event. Stored so a future reconcile can detect
    -- "verifier regressed" on the SAME collection (compare hashes;
    -- mismatch = the verifier's gate logic changed since the last
    -- verify, which is an operator-visible signal).
    verification_hash      TEXT,

    -- status: lifecycle phase. Bounded values:
    --   'created'    — collection created, no reindex attempted yet
    --   'indexing'   — reindex write phase in flight
    --   'verified'   — SwitchReport.Ready true; ready for promotion
    --   'active'     — runtime alias currently points here (NEVER eligible)
    --   'reindexing' — superseded; a new collection is being built to replace
    --   'in_use'     — protected rollback target or keep_last_n tail (NEVER eligible)
    --   'retired'    — dropped by retention sweep (terminal)
    -- Retention sweep consults status first; only 'reindexing'/'retired'
    -- classifications are eligible after the duration gate.
    status                 TEXT NOT NULL DEFAULT 'created'
        CHECK (status IN ('created','indexing','verified','active','reindexing','in_use','retired')),

    -- updated_at: last mutation timestamp; the reconciler bumps this
    -- on every event regardless of which lifecycle field changes.
    updated_at             TEXT NOT NULL
);

-- Retention sweep range scan (CleanupWithConfig reads the eligible
-- set without a full table scan). The composite (status, promoted_at)
-- covers the "active + recent" suppress path AND the "old promoted
-- after duration gate" inclusion path.
CREATE INDEX IF NOT EXISTS idx_qdrant_collections_status_promoted
    ON qdrant_collections (status, promoted_at);

-- Reconciliation pass (Reconciler.reconcile() reads the live set vs
-- Qdrant). The schema_version index covers the per-version scan that
-- pins retention to the current schema.
CREATE INDEX IF NOT EXISTS idx_qdrant_collections_schema_status
    ON qdrant_collections (schema_version, status);

-- View for the admin CLI / runbook (cheap operator readout). Live +
-- recent protected + old-but-not-retired = at-a-glance fleet state.
CREATE VIEW IF NOT EXISTS qdrant_collections_status_v1 AS
    SELECT
        collection_name,
        schema_version,
        status,
        promoted_at,
        verified_at,
        point_count,
        CAST(
            (julianday('now') - julianday(promoted_at)) AS INTEGER
        ) AS days_since_promoted
    FROM qdrant_collections
    WHERE status NOT IN ('retired');
