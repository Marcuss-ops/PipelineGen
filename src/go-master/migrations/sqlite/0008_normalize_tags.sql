-- 0005_normalize_tags.sql
-- Normalize tags by creating a separate table for many-to-many relationship

-- Create the clip_tags table for normalized tag storage
CREATE TABLE IF NOT EXISTS clip_tags (
    clip_id TEXT NOT NULL REFERENCES clips(id) ON DELETE CASCADE,
    tag TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (clip_id, tag)
);

-- Index for efficient tag-based queries
CREATE INDEX IF NOT EXISTS idx_clip_tags_tag ON clip_tags (tag);
CREATE INDEX IF NOT EXISTS idx_clip_tags_clip_id ON clip_tags (clip_id);

-- Note: The existing tags JSON column in clips table is kept for backward compatibility
-- New code should use the clip_tags table for tag operations
