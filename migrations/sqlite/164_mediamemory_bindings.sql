-- 164_mediamemory_bindings.sql — Fase 1.2 media_bindings SSOT.
--
-- godlike/06 SSOT (one canonical owner per fact): this is the
-- SOLE DDL for the media_bindings table. UNIQUE binding dedup
-- is enforced at the (concept_id, asset_id, slot_kind) tuple
-- (architecture doc, section 2: "non associare una frase a una
-- sola clip ... ogni relazione deve avere slot visivo").
--
-- godlike/07 NO-FAKE-AVAILABILITY: AssetID is the canonical
-- media_assets.id reference. The LocalPath / DriveLink fields
-- are NOT carried here — AssetDeliveryService owns delivery
-- URLs (sister to clipresolve.AssetMapping per godlike/06).
--
-- composition: the binding row is created by the dashboard's
-- manual binding editor (Section 9) OR by the Phase-3 linker
-- worker. Origin column distinguishes the two paths (OriginManual
-- vs OriginAutoLink / OriginPhraseEq / OriginSemantic).
--
-- scores: 4 separate real-scored columns so the ranker hot path
-- can `ORDER BY semantic_score DESC` without joining a separate
-- audit table. updated_at maintained by the repository on every
-- Upsert.

CREATE TABLE IF NOT EXISTS media_bindings (
    id                TEXT     PRIMARY KEY,
    concept_id        TEXT     NOT NULL,
    asset_id          TEXT     NOT NULL,
    start_ms          INTEGER,
    end_ms            INTEGER,
    slot_kind         TEXT     NOT NULL,
    origin            TEXT     NOT NULL,
    approval_status   TEXT     NOT NULL,
    manual_score      REAL     NOT NULL DEFAULT 0,
    semantic_score    REAL     NOT NULL DEFAULT 0,
    quality_score     REAL     NOT NULL DEFAULT 0,
    success_score     REAL     NOT NULL DEFAULT 0,
    usage_count       INTEGER  NOT NULL DEFAULT 0,
    last_used_at      DATETIME,
    created_at        DATETIME NOT NULL,
    updated_at        DATETIME NOT NULL,
    UNIQUE(concept_id, asset_id, slot_kind),
    FOREIGN KEY(concept_id) REFERENCES media_concepts(id)
);

CREATE INDEX IF NOT EXISTS idx_media_bindings_concept_id
    ON media_bindings(concept_id);

CREATE INDEX IF NOT EXISTS idx_media_bindings_asset_id
    ON media_bindings(asset_id);

CREATE INDEX IF NOT EXISTS idx_media_bindings_approved_slot
    ON media_bindings(approval_status, concept_id, slot_kind);

CREATE INDEX IF NOT EXISTS idx_media_bindings_success
    ON media_bindings(success_score DESC);
