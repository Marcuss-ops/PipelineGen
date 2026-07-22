-- 174_extend_research_cache.sql
-- Extend research_cache with versioning, fingerprinting, TTL, and hit accounting.
--
-- The cache key remains the PRIMARY KEY but is now a SHA256 digest of
-- topic_fingerprint + language + research_version + source_fingerprint + max_steps.
-- The additional columns support:
--   - concept_id          -> canonical media_concepts row that produced this source text
--   - topic_fingerprint   -> stable topic identity (e.g., "civilizzazione maya")
--   - source_fingerprint  -> stable identity of the source material (URL, asset, document)
--   - resolver_version    -> version of the source resolver that produced source_text
--   - research_version    -> version of the research strategy / prompt used
--   - hit_count           -> how many times the cached source text has been reused
--   - expires_at          -> absolute TTL after which the row is a miss

ALTER TABLE research_cache ADD COLUMN concept_id TEXT;
ALTER TABLE research_cache ADD COLUMN topic_fingerprint TEXT;
ALTER TABLE research_cache ADD COLUMN source_fingerprint TEXT;
ALTER TABLE research_cache ADD COLUMN resolver_version TEXT;
ALTER TABLE research_cache ADD COLUMN research_version TEXT;
ALTER TABLE research_cache ADD COLUMN hit_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE research_cache ADD COLUMN expires_at DATETIME;
ALTER TABLE research_cache ADD COLUMN updated_at TEXT NOT NULL DEFAULT (datetime('now'));

CREATE INDEX IF NOT EXISTS idx_research_cache_topic_fingerprint
    ON research_cache(topic_fingerprint);

CREATE INDEX IF NOT EXISTS idx_research_cache_source_fingerprint
    ON research_cache(source_fingerprint);

CREATE INDEX IF NOT EXISTS idx_research_cache_expires_at
    ON research_cache(expires_at);
