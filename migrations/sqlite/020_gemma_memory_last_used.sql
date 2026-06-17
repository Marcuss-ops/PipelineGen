-- Add last_used_at column to gemma_memory_entries so the eviction sweeper can
-- cheaply identify stale rows. Backfill with created_at so existing rows are
-- treated as "old" until they get a hit that updates the column.
--
-- SQLite's ALTER TABLE ADD COLUMN only allows constant defaults, so we add the
-- column empty and populate it in a follow-up UPDATE. New rows will get a real
-- value via SaveMemory which writes last_used_at explicitly.
ALTER TABLE gemma_memory_entries ADD COLUMN last_used_at TEXT NOT NULL DEFAULT '';
UPDATE gemma_memory_entries SET last_used_at = created_at WHERE last_used_at = '' OR last_used_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_gemma_memory_last_used ON gemma_memory_entries(last_used_at);
