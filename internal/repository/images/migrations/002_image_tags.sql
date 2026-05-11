-- 002_image_tags.sql
-- Add support for tags on images

CREATE TABLE IF NOT EXISTS image_tags (
    image_id TEXT NOT NULL,
    tag TEXT NOT NULL,
    PRIMARY KEY (image_id, tag),
    FOREIGN KEY (image_id) REFERENCES images(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_image_tags_tag ON image_tags(tag);
