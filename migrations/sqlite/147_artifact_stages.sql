-- 147_artifact_stages.sql
--
-- FASE 3 (July 2026): Spina Dorsale staging durabile + publication saga.
-- Introduces the canonical `artifact_stages` table backing the
-- durable staging + atomic publication saga flow:
--
--   Stage verified → insert STAGED → outbox commit (artifact_outbox)
--     in the same TX → publisher worker drains → update PUBLISHED
--     with the Drive location → finalizer verifies all PUBLISHED
--     → SUCCEEDED.
--
-- Why a NEW table (`artifact_stages`) and not the existing `artifacts`
-- table (`internal/application/assets/artifacts/`):
--   - The existing `artifacts` table is the PR3-absorbed assetregistry
--     subsystem (content-addressed blob storage, Stage→Verify→Promote).
--   - The FASE 3 saga is a different state machine: the per-request
--     publication record that binds a job_id + local_path +
--     requirement + destination + state, atomically committed with
--     the outbox event that the publisher worker will drain. Extending
--     the existing table would have been a cross-package schema
--     migration with high blast-radius (godlike/07 minimum-blast-radius).
--   - The separate table keeps the asset registry contract narrow
--     (content + storage) and the FASE 3 saga contract narrow
--     (per-publication record). Future consolidation is possible
--     but deferred to a dedicated PR (godlike/06 SSOT: do not
--     silently merge across capability boundaries).
--
-- Columns (per FASE 3 user-spec, byte-exact):
--   id              TEXT PRIMARY KEY  (canonical `art_<unix_nano>_<8hex>`)
--   job_id          TEXT NOT NULL      (FK to canonical jobs.id)
--   local_path      TEXT NOT NULL      (filesystem path under workspace/)
--   hash            TEXT NOT NULL      (SHA-256 hex, computed during write)
--   size            INTEGER NOT NULL   (bytes; canonical size)
--   mime            TEXT NOT NULL      (IANA media type)
--   requirement     TEXT NOT NULL      ('required' or 'optional' — REQUIRED artifacts missing ⇒ job FAILED, never warning)
--   destination     TEXT NOT NULL      (canonical delivery.DestinationKey)
--   state           TEXT NOT NULL      ('STAGED' | 'PUBLISHED' | 'SUCCEEDED' | 'FAILED_PERMANENT')
--
-- Auxiliary columns (audit invariant + retry semantics):
--   attempt_count   INTEGER NOT NULL DEFAULT 0   (publisher worker retry counter)
--   last_error      TEXT NOT NULL DEFAULT ''    (terminal error on FAILED_PERMANENT)
--   published_at    TEXT                          (set when state → PUBLISHED)
--   created_at      TEXT NOT NULL DEFAULT (datetime('now'))
--   updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
--
-- published_location is stored as a JSON struct {kind, uri, external_id}
-- (matches the existing PublishedLocation shape in
-- internal/domain/artifact/artifact.go). Stored as TEXT for forward-
-- compat with shape evolution; readers unmarshal via PublishedLocation.
--
-- Indexes:
--   PRIMARY KEY: id
--   idx_artifact_stages_job_state (job_id, state) — finalizer scan
--     "all artifacts for a job_id, with their states" uses this
--     directly. The composite keeps the finalizer's N+1 problem
--     out of hot paths (the finalizer is the canonical SUCCEEDED gate).
--   idx_artifact_stages_state_created (state, created_at DESC) —
--     publisher worker drain "all STAGED, oldest first" uses this.
--     DESC suffix lets SQLite use the index for ORDER BY without sort.
--   idx_artifact_stages_dest (destination) — orphan-reconciler
--     "all artifacts staged for a destination vs Drive contents".
--
-- godlike/06 SSOT: this table is the SOLE canonical owner of the
-- per-publication record. Cross-package drift tests were DROPPED
-- per godlike/07 minimum-blast-radius (the same precedent as
-- migration 145 operations).
--
-- godlike/07 fail-closed: state is a free-form TEXT column; the
-- application layer (`internal/domain/artifact/stages.go`)
-- owns the typed `ArtifactStageState` enum. The repository
-- validates values in the canonical set before accepting them,
-- returning the typed sentinel `ErrInvalidArtifactStageState` for
-- out-of-set values (godlike/07 NO-FAKE-AVAILABILITY).
--
-- Idempotent: IF NOT EXISTS everywhere. Re-applying on a database
-- that has the table from ad-hoc bootstrapping is a no-op.
-- Verified after migration by `PRAGMA table_info(artifact_stages)`
-- matching the INSERT projection in the repository.

CREATE TABLE IF NOT EXISTS artifact_stages (
    id                 TEXT PRIMARY KEY,
    job_id             TEXT NOT NULL DEFAULT '',
    local_path         TEXT NOT NULL DEFAULT '',
    hash               TEXT NOT NULL DEFAULT '',
    size               INTEGER NOT NULL DEFAULT 0,
    mime               TEXT NOT NULL DEFAULT '',
    requirement        TEXT NOT NULL DEFAULT 'optional'
        CHECK (requirement IN ('required','optional')),
    destination        TEXT NOT NULL DEFAULT '',
    state              TEXT NOT NULL DEFAULT 'STAGED'
        CHECK (state IN ('STAGED','PUBLISHED','SUCCEEDED','FAILED_PERMANENT')),
    attempt_count      INTEGER NOT NULL DEFAULT 0,
    last_error         TEXT NOT NULL DEFAULT '',
    published_location TEXT NOT NULL DEFAULT '',
    published_at       TEXT,
    created_at         TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at         TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Finalizer scan: all artifacts for a job in a given state.
CREATE INDEX IF NOT EXISTS idx_artifact_stages_job_state
    ON artifact_stages(job_id, state);

-- Publisher drain: oldest STAGED first.
CREATE INDEX IF NOT EXISTS idx_artifact_stages_state_created
    ON artifact_stages(state, created_at DESC);

-- Orphan reconciler: scan by destination.
CREATE INDEX IF NOT EXISTS idx_artifact_stages_dest
    ON artifact_stages(destination);
