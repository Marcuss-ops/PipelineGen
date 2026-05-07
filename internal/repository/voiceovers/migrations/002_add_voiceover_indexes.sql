-- 002_add_voiceover_indexes.sql
-- Additional performance indexes for voiceovers table

-- Additional indexes for voiceovers
CREATE INDEX IF NOT EXISTS idx_voiceovers_request
ON voiceovers(request_id)
WHERE request_id != '';

CREATE INDEX IF NOT EXISTS idx_voiceovers_voice
ON voiceovers(voice)
WHERE voice != '';

CREATE INDEX IF NOT EXISTS idx_voiceovers_status_updated
ON voiceovers(status, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_voiceovers_duration
ON voiceovers(duration_seconds)
WHERE duration_seconds > 0;

CREATE INDEX IF NOT EXISTS idx_voiceovers_strategy
ON voiceovers(strategy)
WHERE strategy != '';

CREATE INDEX IF NOT EXISTS idx_voiceovers_updated_at
ON voiceovers(updated_at DESC);

-- Unique index on file_hash for voiceovers
CREATE UNIQUE INDEX IF NOT EXISTS ux_voiceovers_file_hash
ON voiceovers(file_hash)
WHERE file_hash IS NOT NULL AND file_hash != '';
