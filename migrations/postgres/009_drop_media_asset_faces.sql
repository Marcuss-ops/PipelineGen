-- 009_drop_media_asset_faces.sql
-- PostgreSQL media domain — retire the face descriptors from
-- media_asset_features.
-- Apply after 002_media_vector_surfaces.sql, only to the dedicated media
-- database.
--
-- WHY (2026-09-16): the features leg of the canonical enrichment pipeline
-- could not run at all. media_asset_features declared has_faces /
-- face_count / largest_face_ratio, and the analyzer refused to write the
-- row unless a FaceDetector produced them; the only production detector
-- (SidecarFaceDetector) called POST /detect_faces, an endpoint that NO
-- service in this deployment serves. Live evidence: the sidecar answers
-- 404 for that route while the Go side and the Python request model both
-- declare it, so the endpoint was declared twice and served zero times.
-- The result was a derived surface that stayed permanently empty for
-- every asset — the face dimension was blocking the two dimensions that
-- ARE measurable (dominant colour, motion score).
--
-- The face dimension is RETIRED, not defaulted. Godlike/07 forbids the
-- alternative: writing has_faces = 0 for an unanalyzed asset would be a
-- fabricated fact indistinguishable from a real "no faces detected"
-- observation, and every downstream consumer filtering on has_faces would
-- then be reading a lie.
--
-- Statements are idempotent (IF EXISTS / IF NOT EXISTS): re-applying on a
-- converged database is a no-op, matching the rest of this migration
-- family. The CREATE INDEX mirrors the DDL of 002 so a database that only
-- ever runs 002 and a database migrated through 009 converge to the same
-- shape.

ALTER TABLE media_asset_features
    DROP COLUMN IF EXISTS has_faces;
ALTER TABLE media_asset_features
    DROP COLUMN IF EXISTS face_count;
ALTER TABLE media_asset_features
    DROP COLUMN IF EXISTS largest_face_ratio;

-- The composite index existed only to serve has_faces filters. It is
-- dropped with its leading column; the remaining measurable dimension
-- keeps an index of its own.
DROP INDEX IF EXISTS idx_features_faces_motion;
CREATE INDEX IF NOT EXISTS idx_features_motion
    ON media_asset_features (motion_score);
