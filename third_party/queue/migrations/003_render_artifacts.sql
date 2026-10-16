-- render_artifacts: a rendered artifact produced for a job.
--
-- Artifacts are first-class entities, not a column on render_jobs. The worker
-- publishes metadata (hash, size, codec profile) that VeloxEditing relies on
-- for copy-only assembly without re-decoding or re-encoding.
CREATE TABLE render_artifacts (
    id                    TEXT PRIMARY KEY,
    job_id                TEXT NOT NULL REFERENCES render_jobs (id) ON DELETE CASCADE,
    kind                  TEXT NOT NULL,
    storage_key           TEXT NOT NULL,
    artifact_url          TEXT,
    sha256                TEXT,
    mime_type             TEXT,
    size_bytes            BIGINT,
    width                 INTEGER,
    height                INTEGER,
    fps_num               INTEGER,
    fps_den               INTEGER,
    frame_count           INTEGER,
    duration_us           BIGINT,
    profile_id            TEXT,
    copy_eligible         BOOLEAN NOT NULL DEFAULT false,
    codec                 TEXT,
    codec_profile         TEXT,
    closed_gop            BOOLEAN,
    first_frame_keyframe  BOOLEAN,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);
