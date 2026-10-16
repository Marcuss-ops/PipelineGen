-- render_artifacts: record the render backend and Chronon3d version that
-- produced the artifact, so a completed job carries full provenance
-- (artifact hash, url, content type, size, backend, chronon version).
ALTER TABLE render_artifacts
    ADD COLUMN backend TEXT,
    ADD COLUMN chronon_version TEXT;
