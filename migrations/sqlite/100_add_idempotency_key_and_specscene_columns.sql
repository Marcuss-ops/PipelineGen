-- 100_add_idempotency_key_and_specscene_columns.sql (June 2026, PR 6)
--
-- PipelineGen Unified Script Output plan: introduce dedicated `idempotency_key
-- TEXT` and `specscene TEXT` columns on the `scripts` table, and migrate
-- PersistenceProcessor off the dual-purpose `template` / `timeline_json` slots.
--
-- Why this migration:
--   Pre-PR-6 the scripts table stored the persistence-layer idempotency key
--   on the existing `template` column (originally intended for semantic
--   template names like "book", "lesson"). SpecSceneOutput JSON was written
--   into `timeline_json` (originally intended for scene timing). Both slots
--   were dual-purposed and ambiguous. Dedicated columns eliminate the
--   ambiguity without dropping the existing slots (semantic-history
--   preservation: pre-PR-6 `template` strings remain readable for ListScripts
--   filters that use `WHERE template = ?`).
--
-- Backfill strategy:
--   1. idempotency_key: SQL-only backfill from `template` where the slot
--      currently holds a 16-lowercase-hex string (the pre-PR-6 shape
--      emitted by PersistenceProcessor). Detection uses SQLite's native GLOB
--      operator (single-character glob, no regex dependency). Slots that
--      are NOT a 16-hex idem key are left untouched. `template` is preserved
--      for downstream ListScripts filters that still use `WHERE template = ?`
--      on semantic values like "book" or "lesson".
--   2. specscene: opportunistic backfill from `timeline_json` where the
--      payload starts with the canonical SpecSceneOutput envelope
--      ("{\"version\":"). Pre-PR-6 PersistenceProcessor marshalled the
--      SpecScene into timeline_json using json.Marshal, so the discriminator
--      is unambiguous. Rows whose timeline_json does not match the shape
--      are left untouched (they may carry legacy timeline data from before
--      PR 5, which used a different schema).
--
-- Index strategy:
--   Non-unique composite index on (idempotency_key, language). Non-unique
--   on purpose: replays may legitimately produce repeated idem rows, and
--   the canonical "last write wins" rule is enforced by the
--   `ORDER BY id DESC LIMIT 1` query in scripts.go::FindByIdempotencyKey.
--   A UNIQUE constraint would force premature user-visible 409s for manual
--   replay paths.
--
-- Idempotency: ADD COLUMN is idempotent in SQLite only when guarded with
-- IF NOT EXISTS-style safeguards; SQLite does not support `ADD COLUMN IF
-- NOT EXISTS` for ALTER TABLE, so we accept the migration is single-use
-- (consistent with 028_scripts_add_columns.sql, 054_asset_registry.sql,
-- etc.).

-- ─── Column additions ──────────────────────────────────────────────────
ALTER TABLE scripts ADD COLUMN idempotency_key TEXT NOT NULL DEFAULT '';
ALTER TABLE scripts ADD COLUMN specscene TEXT NOT NULL DEFAULT '';

-- ─── Index for FindByIdempotencyKey ────────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_scripts_idempotency_key
    ON scripts(idempotency_key, language);

-- ─── Backfill: idempotency_key from template ──────────────────────────
-- Detection rule: 16-char lowercase hex (the pre-PR-6 PersistenceProcessor
-- shape: SHA-256 prefix). Update only when idempotency_key is currently
-- empty so the migration is safely re-runnable on partially-backfilled
-- databases (e.g. dev environments restored from older snapshots).
UPDATE scripts
SET idempotency_key = template
WHERE length(template) = 16
  AND template GLOB '[0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]'
  AND idempotency_key = '';

-- ─── Backfill: specscene from timeline_json ────────────────────────────
-- Detection rule: timeline_json starts with `{"version":` (the canonical
-- SpecSceneOutput envelope shape). Skip rows whose specscene is already
-- populated to keep the migration re-runnable.
UPDATE scripts
SET specscene = timeline_json
WHERE specscene = ''
  AND timeline_json LIKE '{"version":%';

-- ─── Audit verification queries (operators run ad-hoc, not migration) ──
-- PRAGMA table_info(scripts);
-- SELECT COUNT(*) FROM scripts WHERE idempotency_key != '';
-- SELECT COUNT(*) FROM scripts WHERE specscene != '';
