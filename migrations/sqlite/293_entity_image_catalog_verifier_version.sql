-- database: primary
-- 293_entity_image_catalog_verifier_version.sql
--
-- A verdict is only reusable when it names the policy that produced it. The
-- old catalog stored accepted/rejected as a bare string, so a model or prompt
-- change silently replayed verdicts about different rules. This migration:
--
--   1. adds verifier_version;
--   2. labels every pre-version verdict as legacy, which the current reader
--      refuses (IsCurrentCandidateVerifierVersion fails closed), forcing a
--      fresh visual pass instead of reusing an unversioned decision;
--   3. indexes the version for pool queries.
--
-- content_sha256 already exists on this table after 281; the adapter now reads
-- and writes it, and a changed digest invalidates the verdict in UpsertCandidate.

ALTER TABLE entity_image_catalog_candidates
    ADD COLUMN verifier_version TEXT NOT NULL DEFAULT '';

UPDATE entity_image_catalog_candidates
SET verifier_version = 'legacy-pre-2026-11-14'
WHERE verifier_version = ''
  AND semantic_status IN ('accepted', 'rejected');

CREATE INDEX IF NOT EXISTS idx_entity_image_catalog_candidates_verifier
    ON entity_image_catalog_candidates(canonical_entity_id, semantic_status, verifier_version);

CREATE INDEX IF NOT EXISTS idx_entity_image_catalog_candidates_content
    ON entity_image_catalog_candidates(content_sha256)
    WHERE content_sha256 != '';
