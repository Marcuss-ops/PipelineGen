-- 154_create_script_localizations.sql
--
-- PR-CATALOG-MULTILINGUA step 5 (Italian plan, July 2026): separate
-- the translation of the final SCRIPT (SpecScene.scenes[].text, lives
-- alongside every script generation) from the translation of the
-- clip CATALOG (asset_text_tracks, migration 137, lives once per
-- asset). The first lives in SpecScene.scenes[].text — re-run on
-- every model bump, prompt bump, or source-script-content change.
-- The second lives in asset_text_tracks (transcript / description /
-- visual_summary / short_summary) — sticky per asset.
--
-- godlike/06 SSOT (one canonical owner per fact):
--   - script_localizations.specscene_json is the SOLE canonical owner
--     of the LOCALIZED SpecSceneOutput for a given
--     (script_id, source_script_hash, language_code,
--      model_version, prompt_version) tuple. The original-language
--     SpecSceneOutput remains in scripts.specscene (migration 100).
--   - This table does NOT replace scripts.specscene; it AUGMENTS it
--     with one row per localized variant. The scripts.specscene
--     column is the canonical provenance target (owner of the source
--     baseline; localized rows are downstream derived projections).
--
-- godlike/07 NO-FAKE-AVAILABILITY:
--   - status is a closed enum ('pending','running','ready','failed').
--     The CHECK constraint makes non-canonical values a typed error
--     at write time, not a silent inconsistency.
--   - When status='ready', specscene_json MUST be non-empty (CHECK
--     constraint). Empty specscene + status='ready' would be a
--     fake-availability regression; the producer MUST either set
--     status to 'failed' OR populate specscene_json before flipping
--     to 'ready'.
--   - source_script_hash MUST be non-empty (length > 0) so a
--     non-versioned translation cannot be persisted. The producer
--     computes SHA-256 over the source scripts.specscene
--     (canonical at translation time) and stores verbatim.
--
-- UNIQUE constraint (godlike/06 SSOT one canonical shape per
-- source-script + variant tuple):
--   UNIQUE(script_id, source_script_hash, language_code,
--          model_version, prompt_version)
--   — re-translating the SAME source with the SAME (model, prompt)
--     pair with the SAME target language is a no-op (the INSERT
--     fails on UNIQUE → caller reads the existing row, OR UPDATEs
--     the status from 'pending'/'running' → 'ready'/'failed').
--   — bumping model_version OR prompt_version OR source_script_hash
--     forces a NEW row, preserving the audit trail of every variant
--     ever produced. Operators see exactly which model / prompt
--     produced each translation.
--
-- FK (godlike/06 SSOT cascade-aligned):
--   FOREIGN KEY (script_id) REFERENCES scripts(id) ON DELETE CASCADE
--   matches the existing script_* companion tables (script_sections,
--   script_stock_matches, script_research_sources,
--   script_outline_sections, script_generation_logs,
--   script_versions). Deleting a script cleans up all its
--   localization rows.
--
-- Indexes (drain pattern + lookup):
--   - idx_script_localizations_script_id: per-script lookup; used by
--     the persistence layer to enumerate all variants of a script
--     before resolving the localized SpecSceneOutput to ship to
--     voiceover / cover / downstream Run builds.
--   - idx_script_localizations_language_status: drain pattern;
--     background workers can pick up pending translations per
--     target language without scanning the whole table.
--
-- Consumed by (in follow-up PRs, this step is schema-only):
--   - internal/application/scripts/usecase/translation.go (already
--     produces the in-memory TranslatedSpecScene; persistence layer
--     to be wired in the next step).
--   - internal/platform/sqlite/scripts/localizations.go
--     (future repository; minimal-blast-radius: schema-fixture only
--     this PR).
--
-- Idempotency: CREATE TABLE IF NOT EXISTS + CREATE INDEX IF NOT
-- EXISTS — safe to re-run on a partially-applied DB.

-- ─── 154.1 Table DDL ─────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS script_localizations (
    script_id          INTEGER NOT NULL,

    source_script_hash TEXT NOT NULL
                       CHECK (length(source_script_hash) > 0),

    language_code      TEXT NOT NULL
                       CHECK (length(language_code) >= 2),

    specscene_json     TEXT NOT NULL DEFAULT ''
                       CHECK (status != 'ready' OR length(specscene_json) > 0),

    translation_model  TEXT NOT NULL DEFAULT '',
    model_version      TEXT NOT NULL DEFAULT '',
    prompt_version     TEXT NOT NULL DEFAULT '',

    status             TEXT NOT NULL DEFAULT 'pending'
                       CHECK (status IN ('pending','running','ready','failed')),

    created_at         TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at         TEXT NOT NULL DEFAULT (datetime('now')),

    FOREIGN KEY (script_id) REFERENCES scripts(id) ON DELETE CASCADE,

    UNIQUE(script_id, source_script_hash, language_code, model_version, prompt_version)
);

-- ─── 154.2 Indexes ───────────────────────────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_script_localizations_script_id
    ON script_localizations (script_id);

CREATE INDEX IF NOT EXISTS idx_script_localizations_language_status
    ON script_localizations (language_code, status);

-- ─── 154.3 Audit verification queries (operators run ad-hoc) ────────────
-- PRAGMA table_info(script_localizations);
-- SELECT COUNT(*) FROM script_localizations WHERE status='ready';
-- SELECT language_code, COUNT(*) FROM script_localizations GROUP BY language_code;
