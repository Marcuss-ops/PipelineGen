-- Add correlation_id to jobs so an end-to-end trace (HTTP request → job
-- row → worker → Ollama call → Python script) can be reconstructed from
-- journalctl. Backfilled to '' so existing rows remain valid.
ALTER TABLE jobs ADD COLUMN correlation_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_jobs_correlation ON jobs(correlation_id);
