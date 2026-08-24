-- 155_asset_text_tracks_translation_fingerprint.sql
--
-- PR-CATALOG-MULTILINGUA step 4 (Italian plan, July 2026):
-- introduce the deterministic translation fingerprint. The translation
-- key is the SHA-256 of
--   (source_text_hash, target_language, translation_model,
--    model_version, prompt_version)
-- and the canonical "is there already a covered translation for this
-- context?" lookup filters on translation_key + is_current=1.
--
-- This migration drops the original UNIQUE(asset_id, language_code,
-- text_kind) constraint (which silently overwrote rows on UPSERT) and
-- replaces it with a partial UNIQUE INDEX WHERE is_current=1, so:
--
--   - multiple rows for the same (asset, language, kind) may coexist
--     (audit trail of prior translation attempts).
--   - at most one of them may carry is_current=1 (no split-brain).
--   - prior rows are FLIPPED to is_current=0 by the application's
--     InsertTranslationWithAuditPredecessor (a sibling repository
--     method) within a single transaction; this migration only
--     relaxes the schema, it does NOT introduce application logic.
--
-- Schema evolution strategy (godlike/06: expand, backfill, cutover,
-- contract — backward-compatible additive migration):
--
--   - Migration 137 (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.a) created
--     asset_text_tracks with UNIQUE(asset_id, language_code,
--     text_kind) ON CONFLICT DO UPDATE semantics — the original
--     UPSERT contract that every Fetch/UpsertBatch call relied on.
--   - PR-CATALOG-MULTILINGUA step 1 (migration 137) extended tracks
--     to carry multilingual metadata; step 4 needs the same surface
--     to carry an audit trail per (asset, lang, kind) translation
--     variant.
--   - SQLite lacks ALTER TABLE DROP CONSTRAINT and ALTER TABLE ADD
--     CONSTRAINT, so the canonical 12-step table-recreation pattern
--     (preserved by the existing codebase at migrations/sqlite/069,
--     114, 150) is used to swap the constraint + add columns
--     atomically.
--
-- Columns added (per godlike/06 SSOT discipline, every column has
-- an owner + comment):
--
--   prompt_version   — the prompt-template version string that produced
--                      this row. EMPTY when the provider does not
--                      expose a template taxonomy (matching the
--                      existing model_version convention).
--   is_current       — 1 when this row is the live translation for
--                      the (asset, lang, kind) context; 0 when this
--                      row is an audit predecessor. DEFAULT 1 makes
--                      every pre-migration row continue to act as
--                      "current" until a new translation attempt
--                      flips it (preserves existing behaviour for
--                      non-translation callers, e.g. Whisper acquire).
--   translation_key  — the deterministic fingerprint persisted in
--                      the row for fast indexed lookup. The canonical
--                      formula lives in
--                      internal/domain/asset/text_track_hashes.go
--                      (TranslationKey helper); the column mirrors
--                      the formula. DEFAULT '' (legacy rows have no
--                      fingerprint; the application layer treats
--                      translation_key == '' as "always re-translate
--                      on next opportunity", which is the right
--                      backward-compat behaviour for legacy acquire).
--
-- Indexes preserved (the 3 indexes from migration 137 are
-- recreated to keep the resolver's lookup hot path unchanged).
-- The new partial UNIQUE INDEX WHERE is_current = 1 replaces the
-- original UNIQUE(asset_id, language_code, text_kind) constraint
-- and enforces the "at most one current row per context" invariant
-- without preventing historical accumulation.
--
-- ─── Pre-migration data migration ──────────────────────────────────────
-- Existing rows are copied verbatim. Their prompt_version is '',
-- translation_key is '', is_current is 1 — so they keep acting as
-- the live row for their (asset, lang, kind) until a new
-- translation attempt flips+inserts. The application layer's
-- flip-and-insert method is documented in
-- internal/platform/sqlite/assets/
--   text_track_repository.go::InsertTranslationWithAuditPredecessor.
--
-- ─── Future-deprecation note (NOT a forward-pointer contract yet) ────
-- The original UNIQUE(asset_id, language_code, text_kind) constraint
-- is REMOVED by this migration — the partial UNIQUE INDEX is the
-- only "single current row per context" gate. Future agents
-- finding this migration must NOT add UNIQUE(asset_id,
-- language_code, text_kind) back to the table (it would re-introduce
-- the silent-overwrite regression). The code-reviewer-minimax-m3
-- fleet is the canonical detector for that misstep; the
-- archcheck forward-prevention scanner (forward-pointer) is the
-- future SSOT.

-- PRAGMA foreign_keys = OFF;
--
-- Note (godlike/07 honest lock): per SQLite docs,
-- `PRAGMA foreign_keys = OFF` is a NO-OP inside an open
-- transaction. The migration runner wraps each .sql file in its
-- own transaction, so this toggle has zero runtime effect. It is
-- kept here as a documentary marker of the FK posture the
-- migration was authored under (i.e. "we don't depend on FK
-- enforcement mid-migration; we depend on the atomic
-- BEGIN/COMMIT semantics the runner provides"). The toggles do
-- not affect correctness because:
--   (a) the INSERT INTO asset_text_tracks_new SELECT FROM
--       asset_text_tracks copies rows verbatim — every asset_id
--       references an existing media_assets row by construction;
--   (b) DROP TABLE on the old asset_text_tracks is FK-neutral;
--   (c) ALTER TABLE ... RENAME is FK-neutral.

CREATE TABLE IF NOT EXISTS asset_text_tracks_new (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,

    asset_id            TEXT NOT NULL,
    language_code       TEXT NOT NULL,
    text_kind           TEXT NOT NULL,

    text_content        TEXT NOT NULL DEFAULT '',

    source_type         TEXT NOT NULL DEFAULT 'provided',
    source_language_code TEXT NOT NULL DEFAULT '',
    is_original         INTEGER NOT NULL DEFAULT 0,

    provider            TEXT NOT NULL DEFAULT '',
    model_name          TEXT NOT NULL DEFAULT '',
    model_version       TEXT NOT NULL DEFAULT '',
    prompt_version      TEXT NOT NULL DEFAULT '',

    text_hash           TEXT NOT NULL DEFAULT '',
    source_version      TEXT NOT NULL DEFAULT '',
    translation_key     TEXT NOT NULL DEFAULT '',
    is_current          INTEGER NOT NULL DEFAULT 1,

    confidence          REAL,  -- nullable: NULL means provider did not report confidence
    status              TEXT NOT NULL DEFAULT 'READY'
                        CHECK (status IN ('READY', 'PENDING', 'FAILED')),

    created_at          TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at          TEXT NOT NULL DEFAULT (datetime('now')),

    FOREIGN KEY (asset_id) REFERENCES media_assets(id) ON DELETE CASCADE
);

-- PR-CATALOG-MULTILINGUA step 4: the original
-- UNIQUE(asset_id, language_code, text_kind) is intentionally OMITTED
-- to permit historical accumulation. The partial UNIQUE INDEX
-- idx_asset_text_tracks_current (declared below) enforces the
-- "at most one current row per context" invariant without blocking
-- audit predecessors.

INSERT INTO asset_text_tracks_new (
    id, asset_id, language_code, text_kind, text_content,
    source_type, source_language_code, is_original,
    provider, model_name, model_version,
    text_hash, source_version,
    confidence, status,
    created_at, updated_at,
    prompt_version, translation_key, is_current
)
SELECT
    id, asset_id, language_code, text_kind, text_content,
    source_type, source_language_code, is_original,
    provider, model_name, model_version,
    text_hash, source_version,
    confidence, status,
    created_at, updated_at,
    '' AS prompt_version,
    '' AS translation_key,
    1  AS is_current
FROM asset_text_tracks;

DROP TABLE asset_text_tracks;

ALTER TABLE asset_text_tracks_new RENAME TO asset_text_tracks;

-- ─── Indexes (matches migration 137 byte-for-byte) ──────────────────────
-- Fast lookup by asset: used by ListByAsset and the resolver's
-- "is there already a READY transcript for this clip?" check.
CREATE INDEX IF NOT EXISTS idx_asset_text_tracks_asset
    ON asset_text_tracks (asset_id);

-- Lookup by language + kind: used by the SearchTextBuilder to fetch
-- all transcripts/descriptions in configured index_languages.
CREATE INDEX IF NOT EXISTS idx_asset_text_tracks_language
    ON asset_text_tracks (language_code, text_kind);

-- Dedup / change-detection by content hash: used by source_version
-- computation and by the "skip Whisper if identical text already
-- exists" fast path.
CREATE INDEX IF NOT EXISTS idx_asset_text_tracks_hash
    ON asset_text_tracks (text_hash);

-- ─── PR-CATALOG-MULTILINGUA step 4 new index ────────────────────────────
-- Partial UNIQUE INDEX on (asset_id, language_code, text_kind)
-- WHERE is_current = 1 — the canonical "at most one live row per
-- translation context" gate. Replaces the original inline
-- UNIQUE(asset_id, language_code, text_kind) constraint.
-- Historical audit rows (is_current = 0) bypass the constraint,
-- so prior translations stay readable for audit. The application's
-- InsertTranslationWithAuditPredecessor method flips the prior
-- is_current = 1 row to 0 within the same transaction that
-- inserts the new row — atomically preventing split-brain.
CREATE UNIQUE INDEX IF NOT EXISTS idx_asset_text_tracks_current
    ON asset_text_tracks (asset_id, language_code, text_kind)
    WHERE is_current = 1;

-- PRAGMA foreign_keys = ON;  -- no-op inside the runner's
-- transaction (paired comment to the OFF marker above; see
-- header note for the rationale).
