# Per-artifact checkpoint — schema and repository API (roadmap item 9)

**Status:** PHASE 5 — schema, typed port, SQLite adapter, `localized_render`
dual-write, per-artifact loader and cutover exist behind separate flags. The
cutover for `localized_render` is done and live-verified; other artifact kinds
remain future expansion, not a pending step of this rollout.
**Created:** 2026-11-16.
**Owner:** `refactored` (`internal/capabilities/scripts`,
`internal/platform/sqlite/scripts`, `migrations/sqlite`).
**Roadmap item:** item 9, “checkpoint per singolo artefatto” (`localized_render`
first kind).

## 1. Problem and boundary

`RunRepository.SavePartialResult` serializes the entire `GenerateResult` into
`run_observability.workflow_payload_json` on every checkpoint opportunity. The
per-unit debounce limits how often that happens, but the cost and the crash
window are still proportional to the whole run: one completed voiceover or
localized render rewrites every scene, plan and receipt already produced.

The repository already owns three related facts. None of them is a per-artifact
body store:

| Owner | Fact | Not its job |
|---|---|---|
| `run_observability.workflow_payload_json` | last complete whole-result recovery envelope | per-unit granularity |
| `job_checkpoints` (`capabilities/checkpoint`) | “(job, stage, unit) completed”, input fingerprint, artifact SHA/URI, resume predicate | store the artifact body |
| `artifact_stages` | publication saga from STAGED to SUCCEEDED | resume/result reconstruction |
| **`run_artifact_checkpoints`** (new) | **one completed artifact's typed JSON body + identity + fingerprint** | whole-result snapshots, publication state |

The new table is the narrow third owner. A row is written **after** a unit
certifies its artifact and **before** the next unit needs the result in
memory. On resume, the loader reads the rows and rebuilds the result; a row is
reused only when its input fingerprint, processor version and artifact bytes
still match.

## 2. Schema

Migration: `migrations/sqlite/296_run_artifact_checkpoints.sql`
(scope: `observability`, because `run_observability` and the scripts run
repository live on that plane).

```sql
CREATE TABLE IF NOT EXISTS run_artifact_checkpoints (
    run_id             TEXT NOT NULL,
    artifact_kind      TEXT NOT NULL,
    artifact_key       TEXT NOT NULL,

    schema_version     INTEGER NOT NULL CHECK (schema_version > 0),
    input_fingerprint  TEXT NOT NULL CHECK (input_fingerprint <> ''),
    processor_version  TEXT NOT NULL CHECK (processor_version <> ''),

    artifact_id        TEXT NOT NULL DEFAULT '',
    artifact_sha256    TEXT NOT NULL DEFAULT '',
    artifact_uri       TEXT NOT NULL DEFAULT '',
    artifact_size_bytes INTEGER NOT NULL DEFAULT 0 CHECK (artifact_size_bytes >= 0),
    artifact_duration_us INTEGER NOT NULL DEFAULT 0 CHECK (artifact_duration_us >= 0),

    payload_json       TEXT NOT NULL DEFAULT '{}',
    payload_sha256     TEXT NOT NULL CHECK (payload_sha256 <> ''),

    attempt            INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    completed_at       TEXT NOT NULL,
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL,

    PRIMARY KEY (run_id, artifact_kind, artifact_key),
    FOREIGN KEY (run_id) REFERENCES run_observability(run_id) ON DELETE CASCADE
);
```

Design decisions:

- **No status column.** Row existence means READY. `Invalidate` deletes the
  row, exactly like `job_checkpoints`. A status column would create a second
  truth that resume has to interpret; a failed artifact is simply absent and
  the unit is recomputed.
- **Composite primary key is the resume identity and the only index.** `Get`
  is an exact three-column lookup; `List(run_id)` and the resume scan use the
  `(run_id, artifact_kind, artifact_key)` prefix in that order. An extra index
  would add write cost to a hot per-unit path with no query behind it.
- **Payload is bounded** by `MaxArtifactCheckpointPayloadBytes` (8 MiB). Large
  media is referenced by `artifact_uri` + `artifact_sha256`; a checkpoint that
  grows past the limit is a whole-result snapshot in disguise and is rejected
  before the database.
- **`payload_sha256` covers the exact stored bytes.** A reader verifies it and
  returns `ErrArtifactCheckpointCorrupt` on mismatch; it never decodes or
  reuses a tampered/truncated body.
- **`ON DELETE CASCADE`** keeps checkpoints subordinate to the run. Retention
  or explicit run deletion cannot leave orphan resume rows.
- **Timestamps are explicit RFC3339Nano UTC writes.** `completed_at` is the
  producer's completion observation; `created_at`/`updated_at` are adapter
  stamps. No SQLite `datetime('now')` default hides the actual clock.

## 3. Artifact kinds and keys

The SQL column is free-form so a new kind does not require a migration, but
the Go API accepts only the current vocabulary:

| Kind | Key segments | Body carried |
|---|---|---|
| `scene_text` | `sceneID` | generated scene text |
| `translation` | `sceneID/language` | translated text |
| `voiceover` | `sceneID/language` | audio reference + timing artifact |
| `overlay_plan` | `language` (or `source`) | sealed overlay plan |
| `overlay_render` | `language/planDigest` | render reference + manifest |
| `localized_render` | `sceneID/clipID/language` | certified localized render |
| `final_audio` | `language` | final-audio reference |
| `final_job_plan` | `attempt` | compiled render plan |
| `final_job_receipt` | `attempt` or remote job id | remote receipt |

Keys are joined by `ArtifactCheckpointKey`; a segment containing `/`, or with
leading/trailing whitespace, is rejected so a key always splits back into its
parts unambiguously.

## 4. Repository API

Go contract: `internal/capabilities/scripts/artifact_checkpoint.go`.

```go
type ArtifactCheckpointRepository interface {
    SaveArtifactCheckpoint(ctx context.Context, checkpoint ArtifactCheckpoint) error
    GetArtifactCheckpoint(ctx context.Context, runID string, kind ArtifactCheckpointKind, key string) (*ArtifactCheckpoint, error)
    ListArtifactCheckpoints(ctx context.Context, runID string) ([]ArtifactCheckpoint, error)
    InvalidateArtifactCheckpoint(ctx context.Context, runID string, kind ArtifactCheckpointKind, key string) error
}
```

Semantics:

- `Save` validates the checkpoint, computes `PayloadSHA256`, stamps
  `CreatedAt`/`UpdatedAt`, and upserts on the composite primary key. Re-saving
  the same identity converges on one row; it never appends a second version.
- `Get` returns `(nil, nil)` on no row. A row whose payload digest fails is an
  error wrapping `ErrArtifactCheckpointCorrupt`, never a miss.
- `List` returns every row of one run ordered by `(artifact_kind,
  artifact_key)`. It is the resume scan; consumers that need one artifact use
  `Get`.
- `Invalidate` deletes one row. Deleting a missing row is a no-op, so cleanup
  is idempotent.
- All methods fail closed on empty/invalid identity before touching SQL.

Validation (`ArtifactCheckpoint.Validate`) requires:

- non-empty, unpadded run id, key, input fingerprint and processor version;
- a known kind and exactly `ArtifactCheckpointSchemaVersion` (1);
- valid JSON payload at most 8 MiB, with `{}` as the canonical empty body;
- a canonical SHA-256 when `artifact_sha256` or `payload_sha256` is present;
- non-negative size/duration/attempt and a non-zero `completed_at`.

`ReusableFor(inputFingerprint, processorVersion)` is the pure resume half: it
rejects a changed schema, input fingerprint or processor version with a
reason. It deliberately does **not** claim the bytes still exist;
`checkpoint.ArtifactVerifier` remains the authority for that.

## 5. Write path

For each completed unit:

```text
artifact = the unit's typed sub-document (scene text, translation, voiceover,
           overlay plan, render reference, final audio, receipt, ...)
cp = ArtifactCheckpoint{
    RunID:             runID,
    Kind:              kind,
    Key:               ArtifactCheckpointKey(segments...),
    SchemaVersion:     1,
    InputFingerprint:  fingerprint(unit inputs),
    ProcessorVersion:  processorVersion,
    ArtifactID/SHA/URI/Size/Duration: the certified identity,
    Payload:           json.Marshal(artifact),
    Attempt:           attempt,
    CompletedAt:       time.Now().UTC(),
}
SaveArtifactCheckpoint(ctx, cp)
```

The fingerprint is computed from the exact inputs that determine the artifact
(prompt/source text, language, clip identity and window, plan digest, audio
window). It is stable across retries and changes when any input changes. The
writer must not save a checkpoint before the artifact is certified; a failed
render leaves no row.

Dual-write during rollout: after `SaveArtifactCheckpoint` succeeds, the
existing `SavePartialResult` may still write the whole envelope. The
per-artifact row is authoritative for the artifact; the envelope remains the
safety net until every kind has a loader. Once the cutover flag is enabled for
a kind, that kind must never depend on the envelope for resume.

For `localized_render` the two flags are deliberately separate:

- `SCRIPT_ARTIFACT_CHECKPOINT_DUAL_WRITE` records the rows;
- `SCRIPT_ARTIFACT_CHECKPOINT_RESUME` trusts them for recovery;
- `SCRIPT_ARTIFACT_CHECKPOINT_CUTOVER` removes the per-unit envelope write and
  leaves the envelope to stage boundaries.

All three accept `1`/`true`/`on`/`yes`/`localized_render`; empty/off values keep
the legacy path; any other value is a startup error. Cutover is refused at boot
unless dual-write, resume and the media-SSOT artifact verifier are all wired.
The writer receives the
durable **run id** (`run_observability.run_id`) explicitly. It must never use
the broker job id (`exec.JobID`) as `run_id`: the table's foreign key points at
`run_observability`, and the first live run failed with `FOREIGN KEY
constraint failed` when the two identities were conflated
(`TestLocalizedRenderDualWriteUsesDurableRunIDNotJobID`).

## 6. Resume path

```text
rows = ListArtifactCheckpoints(runID)
for each row:
    if row.Validate() fails            → invalidate, recompute
    if row.PayloadSHA256 != digest     → ErrArtifactCheckpointCorrupt, recompute
    if !row.ReusableFor(current fingerprint, processor version)
                                       → invalidate, recompute
    if row.ArtifactSHA256 != "":
        status = ArtifactVerifier.VerifyArtifact(row.ArtifactSHA256, row.ArtifactURI)
        if !status.Exists || !status.SHA256Matches
                                       → invalidate, recompute
    merge row.Payload into the result under (Kind, Key)
recompute derived projections (canonical timeline, editing timeline, handoff)
```

Rules:

- Only rows for the **current run** are considered; the checkpoint is
  job-specific, not a cross-run cache.
- A mismatch is definitive staleness: invalidate the row and recompute. A
  verifier infrastructure error is not staleness: return EXECUTE without
  invalidating, the same distinction `checkpoint.Resolver.Decide` already
  makes.
- The loader must reject duplicate `(Kind, Key)` rows; the primary key makes
  this impossible in SQL, and the API does not expose a batch merge that could
  synthesize one.
- Derived documents (`CanonicalTimeline`, `OverlayPlan`, `EditingTimeline`,
  final-job payload) are recomputed from the merged artifacts, never loaded as
  a second source.

## 7. Adapter (implemented)

The SQLite adapter is
`internal/platform/sqlite/scripts/artifact_checkpoint_repository.go` and
receives the observability `*sql.DB` (the same handle `SQLiteRunRepository`
uses). It validates and computes `payload_sha256` before SQL, preserves
`created_at` on upsert, verifies the stored digest and JSON before returning a
body, and treats a timestamp/digest/JSON failure as
`ErrArtifactCheckpointCorrupt`. Its tests use the real observability migrations
and cover round-trip, upsert/one-row convergence, ordering, corruption and the
`run_id` cascade. These are the query shapes it uses:

```sql
-- Save (validate + compute PayloadSHA256 in Go before this statement)
INSERT INTO run_artifact_checkpoints (
    run_id, artifact_kind, artifact_key, schema_version,
    input_fingerprint, processor_version,
    artifact_id, artifact_sha256, artifact_uri,
    artifact_size_bytes, artifact_duration_us,
    payload_json, payload_sha256, attempt,
    completed_at, created_at, updated_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(run_id, artifact_kind, artifact_key) DO UPDATE SET
    schema_version = excluded.schema_version,
    input_fingerprint = excluded.input_fingerprint,
    processor_version = excluded.processor_version,
    artifact_id = excluded.artifact_id,
    artifact_sha256 = excluded.artifact_sha256,
    artifact_uri = excluded.artifact_uri,
    artifact_size_bytes = excluded.artifact_size_bytes,
    artifact_duration_us = excluded.artifact_duration_us,
    payload_json = excluded.payload_json,
    payload_sha256 = excluded.payload_sha256,
    attempt = excluded.attempt,
    completed_at = excluded.completed_at,
    updated_at = excluded.updated_at;

-- Get
SELECT ... FROM run_artifact_checkpoints
WHERE run_id = ? AND artifact_kind = ? AND artifact_key = ?;

-- List
SELECT ... FROM run_artifact_checkpoints
WHERE run_id = ? ORDER BY artifact_kind, artifact_key;

-- Invalidate
DELETE FROM run_artifact_checkpoints
WHERE run_id = ? AND artifact_kind = ? AND artifact_key = ?;
```

Timestamps are parsed with `time.RFC3339Nano`; `payload_sha256` is verified
against the raw `payload_json` bytes **before** JSON decoding. A matching
digest with malformed JSON is still corrupt and is never returned.

## 8. Rollout and acceptance

Phases:

1. **Schema + API — done.** Migration applies on a fresh observability DB;
   the port compiles; validation/key/resume tests pass.
2. **Adapter — done.** Four methods implemented; round-trip, upsert, ordering,
   corruption and cascade tests use the real migrations.
3. **Dual-write — done for `localized_render`.** At both certified stages
   (`RENDERED` and `UPLOADED`) the runner writes the checkpoint before the
   envelope; the test proves the checkpoint body is byte-identical to the
   envelope artifact and that the published write upserts the staged row. The
   flag is `SCRIPT_ARTIFACT_CHECKPOINT_DUAL_WRITE` (`1`/`true`/
   `localized_render`; empty/off keeps envelope-only). An unknown value is a
   startup error. Flag-off behaviour is covered by
   `TestLocalizedRenderDualWriteIsOffByDefault`.
4. **Load-on-resume — done for `localized_render`.** The loader
   (`artifact_checkpoint_resume.go`) is gated by
   `SCRIPT_ARTIFACT_CHECKPOINT_RESUME`. It reads the exact
   `(run_id, localized_render, scene/clip/language)` row only when the
   in-memory envelope has no staged render; an envelope value wins. `RENDERED`
   requires the local bytes to still hash to the recorded SHA; `UPLOADED`
   requires `mediaCheckpointArtifactVerifier`. A verifier miss or changed
   fingerprint/processor invalidates the row; a verifier infrastructure error
   returns EXECUTE without deleting it. `UploadRendered` recognizes a
   checkpoint already `UPLOADED` and projects it without a second upload.
   `ListArtifactCheckpoints` remains the future batch/resume-scan API; this
   first loader uses `Get` per unit.
5. **Cutover — done for `localized_render`.** `SCRIPT_ARTIFACT_CHECKPOINT_CUTOVER`
   skips the per-unit whole-result envelope write; the per-artifact row is the
   authority for the unit. `SavePartialResult` remains at stage boundaries and
   for unknown/legacy kinds, and the final completion path still writes the
   envelope. Other kinds are not migrated; the cutover is intentionally
   per-kind, not a blanket switch.

Acceptance tests:

- migration 296 creates the table, rejects a duplicate composite identity, and
  cascades on run deletion (`migrations_296_test.go`);
- every declared column exists after a fresh observability apply (generic
  schema-contract test);
- Save→Get round-trip preserves identity, payload bytes, digest and
  timestamps;
- Save twice on the same triple leaves one row with the second body;
- List is ordered by kind/key and scoped to one run;
- corrupt/truncated payload returns `ErrArtifactCheckpointCorrupt`;
- invalid/padded/unknown identities are rejected before SQL;
- oversized payload is rejected without a write;
- a changed fingerprint/processor invalidates instead of reusing;
- a verifier miss invalidates, a verifier error returns EXECUTE without
  deleting;
- disabling the flag restores the old whole-envelope behaviour exactly
  (`TestLocalizedRenderDualWriteIsOffByDefault`);
- the dual-write body equals the envelope body at `RENDERED` and `UPLOADED`,
  and the second write converges on one row
  (`TestLocalizedRenderDualWriteMatchesEnvelopeAtBothStages`);
- the checkpoint `run_id` is the durable run, never the broker job id
  (`TestLocalizedRenderDualWriteUsesDurableRunIDNotJobID`), and the real SQLite
  adapter rejects a job id with `FOREIGN KEY constraint failed`
  (`TestArtifactCheckpointRepositoryForeignKeysOnRunLedger`);
- a staged `RENDERED` checkpoint whose local bytes still match is uploaded
  without a fresh render, and a `UPLOADED` checkpoint is reused only after the
  media-SSOT verifier confirms it (`artifact_checkpoint_resume_test.go`);
- changed inputs/processor, changed local bytes, verifier miss and resume-off
  all fall back to EXECUTE; verifier error keeps the row; a new Runner can
  resume a crash-written checkpoint (`artifact_checkpoint_resume_test.go`);
- with all cutover prerequisites wired, `RENDERED`, `UPLOADED` and final-job
  fixed media do not write the envelope per unit, while a stage-boundary
  checkpoint still persists the full result
  (`TestLocalizedRenderCutoverUsesCheckpointInsteadOfPerUnitEnvelope`);
- cutting over with resume, verifier or dual-write missing keeps writing the
  envelope instead of deleting the only recovery copy
  (`TestLocalizedRenderCutoverRequiresTrustedCheckpoint`).

## 9. Non-goals

- Replacing `artifact_stages` (publication lifecycle) or `job_checkpoints`
  (stage/unit completion + artifact verification).
- Storing media bytes in SQLite. `artifact_uri` + `artifact_sha256` are the
  external identity; the body is JSON metadata only.
- Cross-run artifact caching. This table answers “has **this run** completed
  **this artifact**”, not “does this result exist somewhere”.
- A batch/transactional multi-artifact save API. The first cut is one artifact
  per write; a batch API can be added only when a caller needs atomic
  multi-artifact commit and has a measured reason.

## 10. Live resume status (2026-11-16)

Executed on an isolated `127.0.0.1:8010` instance with all three flags enabled
and `VELOX_DATA_DIR=<refactored>/data`; the production `pipelinegen.service`
was stopped only for the duration of the test and restored afterwards.

Observed:

- **Dual-write live:** `job_1794836891914366761_3f9b250d`
  (`run_19224c9c-8832-4eb4-9c31-435455c07317`) reached SUCCEEDED and wrote one
  `localized_render` row, `UPLOADED`, under the durable run id. This is the
  first live proof that the FK/run-id fix is correct.
- **Crash/restart live:** `job_1794837241000883985_5c8440a5`
  (`run_f40765c9-2608-487b-a8ff-3373cf937aff`) was killed after the artifact
  was checkpointed as `UPLOADED` but while the job was still RUNNING. After
  restart the job resumed at `COMPILING_AUDIO` and reached SUCCEEDED without a
  new render. The resume came from the envelope, which already carried the
  artifact, so this run does **not** prove loader reuse.
- **Loader reuse live — PASS:** `job_1794842782077565024_b0250964`
  (`run_1ea9dd43-713f-4798-92ab-65c0d969f70f`). The voiceover run had already
  produced the `UPLOADED` checkpoint (SHA `d611…`, Drive id `1qpY…`). I removed
  `localized_renders`/`localized_render_staged` from the envelope while keeping
  the scene text and `Voiceover` reference — the exact crash window after the
  checkpoint and before the artifact envelope write — set the run to
  `GENERATING_VOICEOVERS`, and retried. Attempt 3 logged
  `localized render checkpoint reused` with the same SHA and `UPLOADED` status;
  `GENERATING_VOICEOVERS` completed in 6 ms; the job reached **SUCCEEDED**. The
  whole log has zero `localized render staged` lines and the checkpoint URI
  stayed `1qpY…`, so no fresh render or upload was paid.
- **Why the voiceover path:** an earlier no-audio forced retry was correctly
  rejected with `input fingerprint changed` because the retry re-ran scene-text
  generation and produced different caption text. That is the loader working
  as designed, not a bypass; the voiceover path preserves the checkpoint's
  text/voiceover inputs. The fixed-media intro probe produced no checkpoint
  (native fixed clip, not a localized-render unit).

- **Cutover live — PASS (same run, attempt 4):** the isolated server booted
  with `dual_write=true`, `resume=true`, `cutover=true`. After the envelope
  render was removed again, the retry reused the `UPLOADED` checkpoint and
  logged `localized render envelope write skipped (cutover)` in the same unit;
  `GENERATING_VOICEOVERS` completed in 6 ms and the job reached **SUCCEEDED**.
  The checkpoint stayed `UPLOADED` (same SHA/URI), no `localized render staged`
  line appeared, and the stage boundary persisted the full result afterwards.

Both live criteria are therefore met: loader reuse and per-unit envelope
cutover. The hermetic tests cover the staged/published reuse, fingerprint and
processor changes, local byte tampering, verifier miss/error, flag off, crash
plus a new Runner, and the cutover/prerequisite matrix. Cutover is not claimed
for artifact kinds other than `localized_render`.

### Live FK defect fixed

First live run failed before this fix:

```text
localized render: artifact checkpoint save: ...
  job_1794836554134768295_1fabc0ae/localized_render/clip-0/.../it:
  FOREIGN KEY constraint failed
```

The writer had used `exec.JobID` (broker job) as the checkpoint `run_id`.
All `localized_render` writers now take the durable run id explicitly; the
envelope partial write uses the same id. The regression test uses a run and job
with different identities.
