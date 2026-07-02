-- 118_publication_intents.sql — Spina Dorsale FASE 2c, July 2026
--
-- publication_intents is the canonical 8-state lifecycle ledger for every
-- artifact publication across ALL capabilities (YouTube, stock, Artlist,
-- images, voiceover, sound effects, uploads). Per il Piano d'Azione §4.7,
-- ogni pubblicazione esterna (Drive, S3, etc.) registra un intent qui PRIMA
-- del commit SQLite, così che un crash tra upload e commit sia recuperabile.
--
-- Senza questa tabella, un upload Drive che riesce ma la transazione SQLite
-- che fallisce lascia un file ORFANO su Drive: nessuna traccia in SQLite,
-- nessuna visibilità al reconciler, nessun cleanup automatico.
--
-- Status machine (canonical, single closed enum):
--   PREPARED → UPLOADING → PUBLISHED → COMMITTED
--       ↓           ↓          ↓
--    FAILED      FAILED     ORPHANED → CLEANUP_PENDING → CLEANED
--                                ↓
--                             FAILED
--
-- Stato transizioni (typed):
--   PREPARED        → UPLOADING        (upload iniziato)
--   UPLOADING       → PUBLISHED        (upload riuscito, file su provider remoto)
--   PUBLISHED       → COMMITTED        (asset_location registrata, tx completata)
--   PREPARED|UPLOADING → FAILED        (errore upload, terminale)
--   PUBLISHED       → ORPHANED         (reconciler: upload riuscito ma mai committato)
--   ORPHANED        → CLEANUP_PENDING  (cleanup iniziato)
--   CLEANUP_PENDING → CLEANED          (file remoto rimosso, cleanup completato)
--   CLEANUP_PENDING → FAILED           (cleanup fallito, retry dopo)
--
-- AGENTS.md godlike/06 (one canonical owner per fact): publication_intents
-- è il SINGLE truth sink per il lifecycle degli intent di pubblicazione.
-- Il reconciler (internal/application/jobs/finalizer/reconciler.go) legge
-- SOLO questa tabella. I capability adapter scrivono SOLO questa tabella.
--
-- AGENTS.md godlike/04 (database lock: SQLite + mattn/go-sqlite3).
-- Questa tabella vive nel unified media.db.sqlite.

CREATE TABLE IF NOT EXISTS publication_intents (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id           TEXT    NOT NULL DEFAULT '',
    attempt          INTEGER NOT NULL DEFAULT 0,
    artifact_id      TEXT    NOT NULL DEFAULT '',
    idempotency_key  TEXT    NOT NULL DEFAULT '',
    provider         TEXT    NOT NULL DEFAULT 'drive',
    state            TEXT    NOT NULL DEFAULT 'PREPARED'
        CHECK (state IN (
            'PREPARED', 'UPLOADING', 'PUBLISHED', 'COMMITTED',
            'ORPHANED', 'CLEANUP_PENDING', 'CLEANED', 'FAILED'
        )),
    remote_file_id   TEXT    NOT NULL DEFAULT '',
    last_error       TEXT    NOT NULL DEFAULT '',
    created_at       TEXT    NOT NULL DEFAULT (datetime('now')),
    updated_at       TEXT    NOT NULL DEFAULT (datetime('now')),

    UNIQUE(idempotency_key)
);

-- Sweeper scan key: trova PUBLISHED rows senza corrispondente asset_location.
-- (state, updated_at) is the reconciler's scan key for orphan detection.
CREATE INDEX IF NOT EXISTS idx_publication_intents_state_updated
    ON publication_intents (state, updated_at);

CREATE INDEX IF NOT EXISTS idx_publication_intents_job_id
    ON publication_intents (job_id);

CREATE INDEX IF NOT EXISTS idx_publication_intents_idempotency_key
    ON publication_intents (idempotency_key);

-- Down-migration (manual; godlike/07 explicit narrow contract):
--   DROP INDEX idx_publication_intents_state_updated;
--   DROP INDEX idx_publication_intents_job_id;
--   DROP INDEX idx_publication_intents_idempotency_key;
--   DROP TABLE publication_intents;
