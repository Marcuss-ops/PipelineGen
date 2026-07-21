-- 167_mediamemory_usage_events.sql — Fase 1.2 media_usage_events SSOT.
--
-- godlike/06 SSOT (one canonical owner per fact): this is the
-- SOLE DDL for the media_usage_events table. FeedbackService is
-- the canonical writer (architecture doc section 11); the
-- ranker reads these rows to promote success_score on the
-- binding (SuccessScore increment on RenderCompleted && !Rejected
-- per the architecture document's anti-anti-pattern rules).
--
-- godlike/07 NO-FAKE-AVAILABILITY: the table appends ONLY
-- (no UPDATE) so the ranker can replay the exact sequence of
-- accept/reject/replace events and recompute SuccessScore
-- deterministically on warm-up. Adding rows is a free op;
-- meaning is locked in at append time.
--
-- booleans: SQLite has no BOOLEAN type, so the 4 booleans are
-- INTEGER NOT NULL DEFAULT 0 (1 = true). The repository Go code
-- converts ↔ bool at the row boundary (godlike/06 SSOT:
-- canonical bool type lives in the application layer; SQLite
-- stores the wire-level integer).
--
-- this table has NO FKs to media_bindings / media_concepts
-- because UsageEvents are append-only audit events; they MUST
-- survive even if the underlying binding is later reindexed
-- or deleted (compliance + replay).

CREATE TABLE IF NOT EXISTS media_usage_events (
    id                TEXT     PRIMARY KEY,
    project_id        TEXT     NOT NULL,
    scene_id          TEXT     NOT NULL,
    concept_id        TEXT     NOT NULL,
    asset_id          TEXT     NOT NULL,
    binding_id        TEXT     NOT NULL,
    slot_kind         TEXT     NOT NULL,
    selected          INTEGER  NOT NULL DEFAULT 0,
    manually_selected INTEGER  NOT NULL DEFAULT 0,
    rejected          INTEGER  NOT NULL DEFAULT 0,
    render_completed  INTEGER  NOT NULL DEFAULT 0,
    created_at        DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_media_usage_events_concept
    ON media_usage_events(concept_id);

CREATE INDEX IF NOT EXISTS idx_media_usage_events_asset
    ON media_usage_events(asset_id);

CREATE INDEX IF NOT EXISTS idx_media_usage_events_project_scene
    ON media_usage_events(project_id, scene_id);
