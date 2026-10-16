-- Persist the media facts observed by ffprobe so a later queue read retains
-- the complete certification contract.
ALTER TABLE render_artifacts
    ADD COLUMN IF NOT EXISTS container TEXT,
    ADD COLUMN IF NOT EXISTS pixel_format TEXT,
    ADD COLUMN IF NOT EXISTS audio_streams INTEGER;
