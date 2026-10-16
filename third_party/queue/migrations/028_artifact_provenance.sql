-- Preserve non-Chronon artifact origin and independent verification evidence.
-- NULL is retained for legacy artifacts whose provenance predates this field.
ALTER TABLE render_artifacts
    ADD COLUMN IF NOT EXISTS provenance JSONB;
