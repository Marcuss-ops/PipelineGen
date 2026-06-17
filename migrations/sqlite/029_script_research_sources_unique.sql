-- 029_script_research_sources_unique.sql
-- Enforces uniqueness of research sources for a given (script, url, query)
-- combination so that the SaveResearchSources insert can use ON CONFLICT
-- to dedupe across retries, parallel chapters, and source splits that
-- resolve to the same URL.
--
-- Migration safety: existing duplicates are NOT removed. We use CREATE
-- UNIQUE INDEX IF NOT EXISTS, which is a no-op if the index already
-- exists but raises an error if duplicates are present. A pre-flight
-- dedup step in the application handles the common case (see
-- scripts.ScriptRepository.SaveResearchSources).

CREATE UNIQUE INDEX IF NOT EXISTS idx_script_research_unique
    ON script_research_sources(script_id, url, query)
    WHERE url != '' AND url IS NOT NULL;
