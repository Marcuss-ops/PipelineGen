-- 187_extend_research_cache_provenance.sql
-- Persist bounded research provenance and metrics without storing raw pages.

ALTER TABLE research_cache ADD COLUMN source_text_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE research_cache ADD COLUMN research_report_json TEXT NOT NULL DEFAULT '';
ALTER TABLE research_cache ADD COLUMN sources_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE research_cache ADD COLUMN claims_verified INTEGER NOT NULL DEFAULT 0;
ALTER TABLE research_cache ADD COLUMN claims_rejected INTEGER NOT NULL DEFAULT 0;
ALTER TABLE research_cache ADD COLUMN search_query_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE research_cache ADD COLUMN pages_fetched INTEGER NOT NULL DEFAULT 0;
