-- Fix deduplication index to not include voice
-- This allows finding existing records regardless of voice

DROP INDEX IF EXISTS idx_voiceovers_dedupe;

CREATE UNIQUE INDEX IF NOT EXISTS idx_voiceovers_dedupe
ON voiceovers(text_hash, language, folder_id);
