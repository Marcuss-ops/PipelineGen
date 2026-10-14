-- database: primary
-- Keep the editorial page that provides durable PERSON identity provenance.
ALTER TABLE entity_image_catalog_candidates
    ADD COLUMN source_page_url TEXT NOT NULL DEFAULT '';
