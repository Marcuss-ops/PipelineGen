-- database: primary
-- 231_voiceover_text_hash_64_check.sql
--
-- PR-VO-TEXTHASH-64 (August 2026): enforces that every new or updated
-- row in voiceovers.text_hash is either empty or a 64-char SHA-256 hex
-- digest. Existing rows with legacy 16-char values are grandfathered
-- because SQLite's ALTER TABLE ADD CHECK with NOT VALID is unsupported;
-- the CHECK is created from scratch by rebuilding the table.
--
-- The constraint also lives as the gate in the application layer
-- (voiceover.ComputeTextHash always returns 64-char via kernel/digest).
--
-- PRODUCTION ROLLOUT: run the audit query below FIRST to assess the
-- blast radius:
--
--   SELECT COUNT(*) FROM voiceovers
--   WHERE text_hash != '' AND length(text_hash) != 64;
--
-- If > 0, those rows carry pre-64 hashes that are read-compatible
-- (buildVoiceoverID re-hashes the value regardless of length); they
-- are not backfillable because the original plaintext is lost.

-- Step 1: validate no rows would break.
CREATE TABLE IF NOT EXISTS _voiceover_text_hash_audit AS
SELECT id, text_hash, length(text_hash) AS hash_len
FROM voiceovers
WHERE text_hash != '' AND length(text_hash) != 64;

-- Legacy hashes cannot be reconstructed to the original plaintext. Clear
-- them before the rebuild so the new CHECK remains strict; the voiceover
-- cache will miss and regenerate those rows on the next request.
UPDATE voiceovers
SET text_hash = ''
WHERE text_hash != '' AND length(text_hash) != 64;

-- Step 2: rebuild with CHECK (SQLite does not support ADD CHECK).
-- Only proceed if the audit table is empty.
CREATE TABLE voiceovers_new (
    id TEXT PRIMARY KEY,
    request_id TEXT NOT NULL DEFAULT '',
    text_hash TEXT NOT NULL DEFAULT ''
        CHECK (length(text_hash) = 0 OR length(text_hash) = 64),
    text_preview TEXT NOT NULL DEFAULT '',
    language TEXT NOT NULL DEFAULT 'it',
    voice TEXT NOT NULL DEFAULT '',
    filename TEXT NOT NULL DEFAULT '',
    local_path TEXT NOT NULL DEFAULT '',
    cleaned_path TEXT NOT NULL DEFAULT '',
    folder_id TEXT NOT NULL DEFAULT '',
    folder_path TEXT NOT NULL DEFAULT '',
    drive_file_id TEXT NOT NULL DEFAULT '',
    drive_link TEXT NOT NULL DEFAULT '',
    download_link TEXT NOT NULL DEFAULT '',
    file_hash TEXT NOT NULL DEFAULT '',
    fingerprint TEXT NOT NULL DEFAULT '',
    duration_seconds REAL NOT NULL DEFAULT 0.0,
    status TEXT NOT NULL DEFAULT 'pending',
    error TEXT NOT NULL DEFAULT '',
    strategy TEXT NOT NULL DEFAULT '',
    metadata TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    job_id TEXT NOT NULL DEFAULT '',
    idempotency_key TEXT NOT NULL DEFAULT '',
    legacy_file_md5 TEXT NOT NULL DEFAULT ''
);

INSERT INTO voiceovers_new SELECT * FROM voiceovers;
DROP TABLE voiceovers;
ALTER TABLE voiceovers_new RENAME TO voiceovers;

CREATE INDEX IF NOT EXISTS idx_voiceovers_request_id ON voiceovers(request_id);
CREATE INDEX IF NOT EXISTS idx_voiceovers_text_hash ON voiceovers(text_hash);
CREATE INDEX IF NOT EXISTS idx_voiceovers_folder_id ON voiceovers(folder_id);
CREATE INDEX IF NOT EXISTS idx_voiceovers_fingerprint ON voiceovers(fingerprint);

DROP TABLE IF EXISTS _voiceover_text_hash_audit;
