-- database: observability
-- 296_run_artifact_checkpoints.sql
--
-- Per-artifact checkpoint store for item 9 of the demolition roadmap.
--
-- The runner's existing checkpoint (`run_observability.workflow_payload_json`)
-- is a WHOLE-result snapshot: every per-unit completion rewrites the entire
-- GenerateResult. `job_checkpoints` (216) records that a (job, stage, unit)
-- completed and can verify its artifact bytes, but it does not carry the
-- artifact body needed to rebuild the result. `artifact_stages` (147) is the
-- publication saga, not a resume store.
--
-- This table is the third, deliberately narrow owner:
--
--   one row = one completed artifact of one run
--
-- The row carries only that artifact's typed JSON body plus its identity and
-- input fingerprint. It is NOT a second copy of the whole result and must not
-- become one: `payload_json` is bounded by the application
-- (MaxArtifactCheckpointPayloadBytes) and large media stays behind
-- `artifact_uri` + `artifact_sha256`.
--
-- Row semantics:
--   * existence = READY. There is no status column: an invalidated checkpoint
--     is deleted, exactly like the generic job_checkpoints contract.
--   * (run_id, artifact_kind, artifact_key) is the resume identity.
--   * input_fingerprint + processor_version decide whether a recorded row is
--     still valid for the current inputs. A mismatch invalidates the row.
--   * payload_sha256 is the digest of the exact payload_json bytes stored; a
--     reader verifies it before trusting the body.
--   * artifact_sha256/artifact_uri pin the external bytes for the shared
--     checkpoint.ArtifactVerifier; they are empty for body-only artifacts.
--   * completed_at is the completion observation; created_at/updated_at are
--     adapter-managed write timestamps (RFC3339Nano UTC on every write).
--
-- The primary key is also the only index this table needs: Get is an exact
-- three-column lookup, the resume scan is the (run_id, artifact_kind,
-- artifact_key) prefix in that order, and List orders by the same prefix.
-- Extra indexes would add write cost to a hot per-unit path without serving a
-- query the API exposes.
--
-- ON DELETE CASCADE keeps checkpoints subordinate to their run: retention or
-- explicit run deletion must not leave orphan resume rows. The foreign key is
-- declarative; the production DSN enables `_foreign_keys=on` (same as every
-- other observability-plane child table).

CREATE TABLE IF NOT EXISTS run_artifact_checkpoints (
    run_id             TEXT NOT NULL,
    artifact_kind      TEXT NOT NULL,
    artifact_key       TEXT NOT NULL,

    schema_version     INTEGER NOT NULL
        CHECK (schema_version > 0),
    input_fingerprint  TEXT NOT NULL
        CHECK (input_fingerprint <> ''),
    processor_version  TEXT NOT NULL
        CHECK (processor_version <> ''),

    artifact_id        TEXT NOT NULL DEFAULT '',
    artifact_sha256    TEXT NOT NULL DEFAULT '',
    artifact_uri       TEXT NOT NULL DEFAULT '',
    artifact_size_bytes INTEGER NOT NULL DEFAULT 0
        CHECK (artifact_size_bytes >= 0),
    artifact_duration_us INTEGER NOT NULL DEFAULT 0
        CHECK (artifact_duration_us >= 0),

    payload_json       TEXT NOT NULL DEFAULT '{}',
    payload_sha256     TEXT NOT NULL
        CHECK (payload_sha256 <> ''),

    attempt            INTEGER NOT NULL DEFAULT 0
        CHECK (attempt >= 0),
    completed_at       TEXT NOT NULL,
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL,

    PRIMARY KEY (run_id, artifact_kind, artifact_key),
    FOREIGN KEY (run_id) REFERENCES run_observability(run_id) ON DELETE CASCADE
);
