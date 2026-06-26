-- 069_add_media_assets_search_terms.sql
-- Adds the search_terms column referenced by clips_repository.go
-- and the clip create/update paths, but missing from the existing
-- media_assets schema in the local database snapshot.
--
-- Fixes: "table media_assets has no column named search_terms"
--        when POST /api/media/:source/clips persists clip records.

ALTER TABLE media_assets ADD COLUMN search_terms TEXT NOT NULL DEFAULT '';
