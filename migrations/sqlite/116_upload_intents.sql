-- 116_upload_intents.sql — audit P0 #4 commit A/2 (Blocco 3.1, July 2026)
--
-- upload_intents is the canonical 5-state lifecycle ledger for every
-- Drive upload attempted by the voiceover pipeline. Without it, a Drive
-- upload that succeeded (file on Drive) but failed to finalize
-- locally (DB error after the upload) leaves an ORPHANED Drive file:
-- no SQLite knowledge of it, no MW worker reaper visibility, no
-- cleanup path. The orphan sweeper (commit B/2) consumes this table
-- to compensate: drive.FileDelete + MarkFailed on stale 'uploaded'
-- rows older than uploadedTTL.
--
-- Status machine (canonical, single closed enum):
--   pending    → uploaded → finalized → completed
--                    ↓
--                 failed
--
-- Status transitions (typed):
--   pending    → uploaded    (after Drive.Upload succeeded)
--   uploaded   → finalized   (after local SQLite finalize succeeded)
--   finalized  → completed   (after outbox commit succeeded)
--   pending|uploaded → failed (sweep on orphan OR explicit error)
--
-- Rationale for separate `uploaded` (Drive done, finalize NOT done)
-- state: this is the SURVIVABLE state on a partial-failure crash
-- window. The orphan sweeper uses it to detect Drive-side orphans
-- (Drive has the file, SQLite doesn't have the row, intent has been
-- `uploaded` > uploadedTTL → compensate).
--
-- Pre-flight collision check (gated on commit body): the cited commit
-- `fix(voiceover): persist project IDs in upload flow` lives at SHA
-- 32d3c55a / scope=project_ids persistence. The cite lives entirely
-- inside the voiceovers table's existing column-set (no new table,
-- no new status enum). This 116_upload_intents migration creates a
-- NEW table with disjoint columns + disjoint status enum. NO
-- semantic collision.
--
-- AGENTS.md godlike/06 (one canonical owner per fact): upload_intents
-- is the SINGLE truth sink for orphan-inent lifecycle. The orphan
-- sweeper (commit B/2) reads ONLY this table. The voiceover pipeline
-- (commit 4/3 production wiring) writes ONLY this table. MarkFailed
-- is emitted from both sides, idempotent on row conflict (re-issuing
-- the same status with the same reason collapses to a no-op).
--
-- AGENTS.md godlike/04 (database lock: SQLite + mattn/go-sqlite3).
-- This table lives in the unified media.db.sqlite per March 2026
-- consolidation. NO cross-DB write surface.

CREATE TABLE IF NOT EXISTS upload_intents (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    voiceover_id    TEXT    NOT NULL,
    drive_file_id   TEXT    NOT NULL DEFAULT '',
    status          TEXT    NOT NULL
        CHECK (status IN ('pending', 'uploaded', 'finalized', 'completed', 'failed')),
    reason          TEXT    NOT NULL DEFAULT '',
    attempts        INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,

    -- Index aids:
    -- voiceover_id is the natural lookup key (one intent per voiceover ID).
    -- (status, updated_at) is the sweeper's scan key for orphan detection.
    -- driver_file_id is unused (NULL is fine) so no explicit index.
    UNIQUE(voiceover_id)
);

CREATE INDEX IF NOT EXISTS idx_upload_intents_status_updated_at
    ON upload_intents (status, updated_at);

CREATE INDEX IF NOT EXISTS idx_upload_intents_voiceover_id
    ON upload_intents (voiceover_id);

-- Down-migration (manual; godlike/07 explicit narrow contract):
--   DROP TABLE upload_intents;
--   DROP INDEX idx_upload_intents_status_updated_at;
--   DROP INDEX idx_upload_intents_voiceover_id;
