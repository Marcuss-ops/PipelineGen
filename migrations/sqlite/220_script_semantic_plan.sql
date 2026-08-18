-- database: primary
-- Migration 220: durable script semantic plan (semantic index + visual bindings).
--
-- The semantic-index pipeline produces two persistent artifacts per script:
--
--   1. script_semantic_items  — the grounded semantic index: one row per
--      SemanticItem (WHAT was said + WHERE, as a rune span, and WHEN, as an
--      integer-microsecond span). This is the "Script → Semantic Index"
--      stage of the pipeline, made durable and queryable.
--   2. script_visual_bindings — the compiled visual decisions: one row per
--      resolved visual event, recording preset_family, the sampled
--      preset_id, the resolved asset_id, the animation triple, the timing,
--      and the resolver/sampler versions that produced it.
--
-- Together they make every visual choice fully reconstructible: from a second
-- in a render you can answer "why did that image with that animation appear?"
-- by joining script_visual_bindings back to script_semantic_items on
-- semantic_id.
--
-- SSOT (godlike/06): scripts.specscene remains the canonical owner of the
-- script text; these two tables are DERIVED projections of the semantic-index
-- pipeline, keyed by script_id and cascade-deleted with the script. The
-- UNIQUE(script_id, semantic_id) / UNIQUE(script_id, visual_event_id) keys make
-- re-running the same pipeline on the same script an upsert (no duplicates),
-- while a changed resolver/sampler version produces a new row via the updated
-- version columns (audit trail).
--
-- Determinism: all timing is integer microseconds (never floats); confidence
-- is REAL in [0,1] enforced by CHECK. subtype/metadata_json carry extractor
-- refinements and are defaulted (empty / '{}') so older producers that do not
-- emit them still write valid rows.
--
-- Idempotency: CREATE TABLE IF NOT EXISTS + CREATE INDEX IF NOT EXISTS — safe
-- to re-run on a partially-applied DB.

-- ─── 220.1 Semantic index ────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS script_semantic_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,

    script_id INTEGER NOT NULL,
    semantic_id TEXT NOT NULL,
    scene_id TEXT NOT NULL,

    type TEXT NOT NULL,
    subtype TEXT NOT NULL DEFAULT '',

    text TEXT NOT NULL,
    normalized_text TEXT NOT NULL,
    canonical_entity_id TEXT NOT NULL DEFAULT '',

    start_char INTEGER NOT NULL,
    end_char INTEGER NOT NULL,
    start_us INTEGER NOT NULL,
    end_us INTEGER NOT NULL,

    confidence REAL NOT NULL DEFAULT 1.0
        CHECK (confidence >= 0 AND confidence <= 1),

    metadata_json TEXT NOT NULL DEFAULT '{}',

    created_at TEXT NOT NULL DEFAULT (datetime('now')),

    FOREIGN KEY (script_id) REFERENCES scripts(id) ON DELETE CASCADE,
    UNIQUE(script_id, semantic_id)
);

CREATE INDEX IF NOT EXISTS idx_script_semantic_items_script_scene
    ON script_semantic_items (script_id, scene_id);

CREATE INDEX IF NOT EXISTS idx_script_semantic_items_entity
    ON script_semantic_items (canonical_entity_id)
    WHERE canonical_entity_id != '';

-- ─── 220.2 Visual bindings ────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS script_visual_bindings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,

    script_id INTEGER NOT NULL,
    semantic_id TEXT NOT NULL,
    visual_event_id TEXT NOT NULL,

    preset_family TEXT NOT NULL,
    preset_id TEXT NOT NULL DEFAULT '',

    asset_id TEXT NOT NULL DEFAULT '',

    animation_in TEXT NOT NULL DEFAULT '',
    animation_idle TEXT NOT NULL DEFAULT '',
    animation_out TEXT NOT NULL DEFAULT '',

    start_us INTEGER NOT NULL,
    duration_us INTEGER NOT NULL,

    resolver_version TEXT NOT NULL DEFAULT '',
    sampler_version TEXT NOT NULL DEFAULT '',

    created_at TEXT NOT NULL DEFAULT (datetime('now')),

    FOREIGN KEY (script_id) REFERENCES scripts(id) ON DELETE CASCADE,
    UNIQUE(script_id, visual_event_id)
);

CREATE INDEX IF NOT EXISTS idx_script_visual_bindings_script_scene
    ON script_visual_bindings (script_id, start_us);

CREATE INDEX IF NOT EXISTS idx_script_visual_bindings_semantic
    ON script_visual_bindings (script_id, semantic_id);
