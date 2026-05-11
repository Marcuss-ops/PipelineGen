-- Migration: 002_clips_advanced_search
-- Description: Add phash and visual embedding columns for advanced search and deduplication.

-- Add phash column
ALTER TABLE clips ADD COLUMN phash TEXT DEFAULT '';

-- Add visual embedding column (for CLIP embeddings)
ALTER TABLE clips ADD COLUMN visual_embedding_json TEXT DEFAULT '[]';

-- Add index for phash to speed up deduplication checks
CREATE INDEX IF NOT EXISTS idx_clips_phash ON clips(phash) WHERE phash != '';
