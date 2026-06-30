# PipelineGen — CHANGELOG

Per godlike/07 (docs/architecture/godlike/07_ZERO_LEGACY_POLICY.md), this
CHANGELOG records every user-visible API and behavior change. Each entry
cross-references the architecture/deprecations.yaml record (if any) and
the canonical ARCHITECTURE.md section that owns the change.

---

## Unreleased

### Added

**[FASE 9, DRIVE-005, June 2026]** Drive canonical `Admin` + `Reader`
port abstractions (Pattern 0) — the composition root's readiness barrier
now consumes a typed port instead of leaking the raw `*gdrive.Service`
into the lifecycle wiring.

- **New port interfaces** declared at
  `internal/infrastructure/drive/ports.go` (`Admin` for folder management
  + file lifecycle + uploads + liveness probe via `Ping`; `Reader` for
  download + metadata + listing + existence checks). Compile-time
  assertions at the bottom of the file: `var _ Admin = (*Uploader)(nil)`
  and `var _ Reader = (*Uploader)(nil)` — a signature drift between the
  port surface and the concrete `*drive.Uploader` is a build failure per
  AGENTS.md Pattern 0.

- **New `Uploader.Ping(ctx) error` method** in
  `internal/infrastructure/drive/uploader_ops.go` (line ~295) — calls
  `u.Service.About.Get().Fields("user").Context(ctx).Do()`. Nil-service
  guard: returns `fmt.Errorf("drive service not configured")` so the
  readiness barrier fails closed on misconfigured Drive.

- **Composition-layer wiring** (the canonical consumer) — the
  `internal/app/wire_services.go` `driveProbe` closure now reads
  `root.Drive.Admin.Ping(ctx)` instead of
  `root.Drive.DriveClient.About.Get().Fields("user")`. Bit-for-bit
  behavioural parity; the only difference is the consumer surface
  (typed port vs raw SDK).

- **Typed-nil-safe guard** at
  `internal/app/build_bundles_drive.go::BuildDriveBundle` — uses the
  canonical `var admin drive.Admin` + `if driveUploader != nil { admin =
  driveUploader }` pattern so the interface value stays true-nil when
  the uploader is nil. Without this guard, `Admin: driveUploader` would
  produce a non-nil interface holding a typed-nil pointer — the classic
  Go interface-nilness trap that would silently panic the readiness
  barrier on a Drive-feature-disabled deployment.

### Deprecated

**[DRIVE-005, June 2026]** `internal/app.DriveBundle.DriveClient` and
`internal/app.DriveBundle.DriveUploader` (the raw `*gdrive.Service` and
`*drive.Uploader` handles on the composition-root bundle) are
deprecated in favour of the typed-pattern ports
(`internal/app.DriveBundle.Admin` + `internal/app.DriveBundle.Reader`).

- **Canonical replacement**: `Admin` (liveness probe `Ping(ctx)`,
  folder management, file lifecycle, raw uploads) and `Reader`
  (download, metadata, listing, existence checks). All new code MUST
  consume via the typed ports per Pattern 0 + godlike/06 "one owner
  per fact".

- **Back-compat path**: the deprecated fields are retained for Wave 14+
  back-compat across the existing ~86 legacy callsites (`cmd/admin/*`,
  `internal/app/{build_bundles_*,lifecycle,module_*,registry_*}`,
  `internal/application/assets/ingest/sync/*`, the storage_wiring_test,
  and many others). The fields alias the SAME `*drive.Uploader`
  instance so **zero split-brain** — godlike/06 holds.

- **Deprecation record**: `architecture/deprecations.yaml#DRIVE-005-FIELDS`
  per godlike/07. Removal target: 2026-Q3 (aligned with the Wave 14
  mega-package split gate which is the natural deletion boundary).
  Status: EXPAND / in_progress as of FASE 9 landing.

- **Audit gate**: `rg 'root\.Drive\.DriveClient|root\.Drive\.DriveUploader'
  internal/` returns the current ~86 readsite count — the
  EXPAND-phase usage_metric baseline for `DRIVE-005-FIELDS`.

**[QDRANT-005A, June 2026]** Canonical `qdrant:` block in `config.yaml` /
`config.example.yaml` matching the yaml tags of the
QdrantConfig struct in `internal/platform/config/types.go` (lines 144-157).

- **Canonical keys**: `enabled` (default `false`), `base_url` (default
  `http://127.0.0.1:6333`), `api_key` (default `""`). The first 3 keys are
  documented in `internal/platform/config/types.go::QdrantConfig` with
  matching `yaml:` tags — the config file format is now byte-aligned with
  the Go struct; no dual-init conflict (the yaml unmarshaller binds keys
  to the struct independently of any sibling block).

- **Secrets policy**: `api_key:` is an empty string placeholder. Operators
  MUST supply the production key via the `VELOX_QDRANT_API_KEY` env var
  per AGENTS.md secrets handling (precedent: `VELOX_ADMIN_TOKEN`). The
  inline literal is never committed; the field is omitted in
  checked-in diffs and set at deployment time.

- **Legacy `vector_search:` block**: preserved below the canonical block
  with a drift comment. The legacy block uses `url:` (not `base_url:`)
  and was unclaimed by any Go callsite (verified via
  `rg 'VectorSearch|vector_search\.' --type go` returns 0 hits — the
  block is configuration drift rather than a live codepath). Removal
  is tracked separately as a follow-up deprecation cycle that will
  collapse both blocks into the single canonical entry. See
  `architecture/deprecations.yaml#QDRANT-005A-VECDRIFT` (status:
  EXPAND / in_progress, removal target: Wave 14 mega-package split
  gate, Q3 2026) for the canonical godlike/07 audit record of the
  drift + the triple-defence compatibility test footprint.

- **CI Check 15 verified**: `bash scripts/ci-architectural-checks.sh`
  asserts that every `qdrant.NewClient(&qdrant.Config{...})` call propagates
  `APIKey: cfg.Qdrant.APIKey` from `QdrantConfig.APIKey` (QDRANT-005A
  hardening). The new canonical block does not alter Go code; Check 15
  remains green. The 5 cmd/admin callers (reconcile_qdrant.go,
  reindex_qdrant.go, dr_qdrant.go, qdrant_maintenance.go,
  qdrant_readiness.go) + the zero-legacy fixture under
  `tests/fixtures/zero_legacy/check_15_qdrant_config_apikey.go` already
  propagate the canonical field correctly.



### Fixed

**[Issue 8, ApplyPreset closure]** `fix(script)` — `ApplyPreset` now
implements all 5 documented presets per
`docs/architecture/godlike/14_UNIFIED_SCRIPT_GENERATION.md` §6 "Required
preset semantics". The Phase 1b stub
`(item, preset) { _ = item; _ = preset }` is now the canonical entry point
for every preset flow.

  - **5-preset semantics** (commit `4a5006c9`,
    `internal/application/scripts/adapters/generation_normalizer.go::ApplyPreset`):
    - `custom` — pass-through (caller fills every flag explicitly; preset
      is a convenience for producing an explicit canonical request).
    - `with_images` — `Output.GenerateSceneImages = true`;
      `ScriptParams.SentencesPerImage = 8` and `ScriptParams.ImagesPerScene
      = 2` only if caller left them at zero (caller precedence).
      Does NOT touch voiceover / document / entities / metadata.
    - `full_media` — `Output.GenerateSceneImages = true` AND
      `Output.GenerateVoiceover = true`, independently, ONLY if the
      caller left the corresponding field at zero (per-field caller
      precedence).
      **Semantic shift**: switched from atomic-gate (only when
      BOTH are off, enable both) to per-field (each field toggled
      independently) when the TDD test
      `TestApplyPresetFullMedia_OverridesOnlyZeroValues` surfaced
      the mismatch with doc §6 row 3, which says
      "images and voiceover enabled explicitly" and is consistent with
      the per-field semantics used by all other presets.
      Test naming (`OverridesOnlyZeroValues`) pinned this.
    - `catalog` / `search` — pass-through (handler binds `source.kind`
      upstream; preset must not touch Source).
    - `batch` / empty / unknown preset literal — pass-through (defensive
      default; no warning).
  - **Duplicate collapse** (commit `24ad4ffa`):
    `internal/application/scripts/usecase/preset_resolver.go::ApplyPreset`
    was a duplicate of the canonical implementation that only handled
    `with_images` (PR-8 narrowed). Refactored into a thin wrapper that
    delegates to `adapters.ApplyPreset(item, preset)` per AGENTS.md
    Pattern 8 ("API package: thin transport only" — `adapters/` is the
    dependency boundary). Import direction is uni-directional
    `usecase → adapters`; verified non-cyclic
    (`adapters/generation_normalizer.go` does NOT import `usecase`).
    Tests in `internal/application/scripts/adapters/normalizer_plan_tests_test.go`
    that called `scripts.ApplyPreset(...)` continue to pass via the
    wrapper (forwarding ensures identical observable behaviour).
  - **TDD coverage** (commit `5c3b1faf`,
    `internal/application/scripts/adapters/normalizer_plan_tests_test.go`):
    5 new tests appended after `TestApplyPresetNilItem`:
    - `TestApplyPresetFullMedia_DoesNothingWhenExplicit` — caller sets
      BOTH → preset is a no-op (caller wins).
    - `TestApplyPresetFullMedia_EnablesBothByDefault` — caller leaves
      BOTH at zero → preset enables both.
    - `TestApplyPresetFullMedia_OverridesOnlyZeroValues` — per-field
      precedence: scenario A `images=true, voiceover=false` → enable
      voiceover only; scenario B symmetric.
    - `TestApplyPresetCatalog_PassThrough` — empty item remains
      untouched on Source/Output/Source.Kind even if zero clips.
    - `TestApplyPresetSearch_PassThrough` — same pattern.
    Field-by-field assertions are used for Catalog/Search because
    `SourceSpec` and `OutputSpec` contain `[]string` (not
    `==`-comparable in Go); existing `reflect.DeepEqual` style retained
    where structure allows.
  - **Legacy handler audit** (`internal/api/script/handler_legacy_adapters.go`,
    lines 219 / 428 / 504): all 3 `domainScript.PresetCustom` bindings
    verified as **semantically correct** — caller fill
    `Source.Kind` + flags + content explicitly for every endpoint, so
    `custom` (pass-through) is appropriate:
    - Line 219 — `LegacyGenerateFromClips` →
      `POST /api/script/generate-from-clips` (SourceClips from
      `ClipIDs` / `Clips` body field).
    - Line 428 — `LegacyGenerateBatch` → multi-item text (SourceText
      per topic).
    - Line 504 — `LegacyCurate` → `POST /api/script/curate`
      (SourceCurate from Query / Filters).

  **Caveat (closure, tracked follow-up):** the canonical `go build ./...`
  for `internal/app/module_media.go:334:3` is broken on `main` by a
  pre-existing regression (obsolete `MutationsDispatcher` literal in
  the `clips.Deps` struct — the field was removed in a recent commit
  but not propagated to the wire composition literal). This regression
  is OUT OF SCOPE for Issue 8 (the fix is owned by a separate ticket
  and does not block the impl + test + wrapper commits from being
  canonical). The Issue 8 closure is recorded in AGENTS.md
  `Active Concerns #10` (CLOSED) and in this CHANGELOG entry.

**[Step 6, b612ae9b]** Qdrant 1.18.2 compatibility — 4 deploy-time bugs fixed:
  - Collection verification tolerance for newly-created collections
    (race between `EnsureSchema` and `GetCollection`).
  - Sparse vector BM25 index rebuild stability (idempotent re-creation
    after collection drop/recreate).
  - Point-level upsert error propagation (previously swallowed).
  - Scroll pagination boundary (NextOffset handling).

**[Step 7]** Script.generate fail-fast composition wiring (Issue 7 / P1):
  - `internal/app/wire_script.go::wireScriptFlow` — server startup now
    fails closed when jobs broker is missing or `script.generate`
    registration fails (previously logged a warning and came up
    without the handler, causing runtime "no handler for
    script.generate" on first enqueue).
  - `internal/app/wire_script.go::validateScriptGenerateWiring` —
    post-registration composition gate enforcing 3 invariants:
    (a) job-type registered in `appjobs.Compose()` registry,
    (b) broker has handler, (c) at least one cluster worker
    configured for `script.generate`.
  - `internal/application/jobs/service.go::HasHandler` — nil-tolerant
    query method on the broker port.
  - `internal/application/scripts/jobs/generation_job.go::RegisterJobs`
    — returns typed error when broker is nil instead of silently
    returning nil.

**[Step 8, 4e1f8e78]** Auth middleware `compareTokens` whitespace injection:
  - `internal/api/middleware/middleware_middleware.go::compareTokens`
    now trims whitespace on both `provided` and `expected` tokens.
    Systemd `Environment=` directive can inject trailing whitespace
    into the token value; the byte-exact comparison previously rejected
    every request. Mirrors the `TrimSpace` already applied by
    `RequireAdminToken`.

**[Step 9, e4698e39]** Qdrant payload mapper nil-logger guard:
  - `internal/infrastructure/qdrant/payload_mapper.go::IndexDocumentToPoint`
    added nil-check on `m.log` before `Debug()` call in the sparse-vector
    path. Prevents panic when logger is nil (common in test fixtures).

**[FASE 3, 8c2d372f]** Script-output stability: prose-fallback heuristic in
`internal/application/scripts/adapters/processor_clip_bindings.go` —
when the LLM (gemma2:2b, gemma4:e4b, and other small local models)
emits valid prose narration but does NOT also emit a structured
`SpecScene.scenes` array (a recurring failure mode for small models
that optimise for prose quality over JSON-mode conformance), the
binder previously returned an empty `specscene.scenes` payload despite
having the clip evidence — silently degrading the generated script
to zero scenes and propagating the empty array to downstream
persistence + document layers. The prose-fallback heuristic now
synthesises N scenes from `input.Text` (word-level balanced
distribution via `strings.Fields`; N = `len(ClipEvidence.ClipIDs)`)
and binds them 1:1 with the existing 1:1 binding loop running
on the synthesised scenes. Kind distribution: `intro@0`,
`clip@0<i<N-1`, `outro@N-1` for N≥3; `all-clip` for N<3 (avoid
intro/outro bleed on a single-clip or two-clip request). New
canonical postprocess counterfield `PostProcessResult.SynthesizedScenes`
makes the engagement observable from the test layer
(`IsEmpty()` honours the synthesised scenes) and from the worker
log stream (`pipeline_stage_completed` warnings include "clip_bindings
fell back to prose (N scenes synthesised)"). New regression
`TestClipBindingsProcessor_ProseFallback` in `package adapters_test`
covers 5 axes: N=1 single-clip collapse, N=2 no intro/outro bleed,
N=10 intro/clip×8/outro distribution, empty-prose skip, clip-evidence
nil skip.

  - **Caveat (FASE 3, tracked follow-up):** `ProcessInput` is pass-by-value
    in the postprocessor chain, so persistence + document builder
    processors downstream of `clip_bindings` continue to see the
    original empty `SpecScene.Scenes` array. The synthesised scenes
    materialise only in the binder's own `result.SynthesizedScenes`
    and in `PipelineResult.SynthesizedScenes`, not on the per-processor
    `input.SpecScene`. Propagation to downstream layers requires a
    paired architecture decision: swap `SpecScene` to a `*SpecSceneOutput`
    pointer, or add a merge-side in `mergePostProcessResult` that
    promotes synthesised scenes back into a downstream-side
    `input.SpecScene`. Tracked separately, NOT in this entry.

**[FASE 4, 38cafe0f]** Script-output stability: `MediaAssetColumns`
38 → 40 column re-alignment with `scan_helpers.go::ScanMediaAsset`'s
scan signature, in `internal/infrastructure/database/sqlite/assets/clips_repository.go`.
The drifted 38-column projection had been silent since migration 059
promoted six canonical columns (media_type, status, drive_folder_id,
drive_link, download_link, group_name) out of metadata_json and
migration 101 reshuffled the lifecycle enum — until now, the projection
was missing all six, had three ghost columns not consumed by
`ScanMediaAsset` (web_view_link removed by 059's `json_remove`,
is_folder and depth present in canonical schema but not scanned),
one misnamed column (`download_url` → `drive_link`), and one
misordered column (`folder_id` repositioned to after `lifecycle_state
/ deleted_at`). Every `SELECT MediaAssetColumns FROM media_assets`
path that hit `AssetStoreSQLite.Get` / `List` / `Search` /
`ResolveBy*` silently failed with `sql: expected 40 destination
arguments in Scan, not 38` — except the failure was never surfaced
because no regression test exercised the full projection against a
schema with all forty columns. New 40-column canonical version
round-trips with `ScanMediaAsset`'s scan signature. New regression
`TestAssetStoreSQLiteGet_AlignsWithScan` in
`internal/infrastructure/database/sqlite/assets/clips_crud_test.go`
pins the contract via four layers: (1) `rows.Columns()` count +
canonical positional order against `SELECT MediaAssetColumns FROM
media_assets LIMIT 0` on a real in-memory `mattn/go-sqlite3` schema;
(2) alias-substring sanity (token-aware) catching future accidental
deletions; (3) end-to-end `Get()` round-trip including the
`drive_folder_id → folder_id` legacy fallback inside `ScanMediaAsset`;
(4) `SoftDeleteFilter` exclusion of `lifecycle_state = 'DELETED'`
rows. Doc-comment on `MediaAssetColumns` and the test's
`canonicalMediaAssetColumns` document the lockstep rule:
"changes to MediaAssetColumns MUST come with paired edits to
`scan_helpers.go::ScanMediaAsset` AND
`clips_crud_test.go::canonicalMediaAssetColumns`."

### Deprecated

**[Deprecation, PR-VO-C1]** Unified `/api/voiceover/generate-with-group`
into the canonical `/api/voiceover/generate` endpoint. New callers MUST
send `destination: {kind: "group", group: "<topic>"}` on `/generate`;
the legacy `/generate-with-group` endpoint is preserved for 90 days as
a deprecated forwarder.

  - **Sunset date:** `Sat, 26 Sep 2026 00:00:00 GMT` (RFC 8594 IMF-fixdate)
  - **Deprecation header:** `Deprecation: true` (RFC 9745 draft standard)
  - **Successor pointer:** `Link: <...>; rel="successor-version"` (RFC 8288)
  - **Migration:** see docs/voiceover/p0-bundle-A1-A6.md §"Deprecation
    contract (90-day Sunset, RFC 8594)"
  - **Deprecation record:** `architecture/deprecations.yaml#PR-VO-C1`
  - **Body unchanged:** the legacy endpoint returns a 100% identical
    payload during the deprecation window — existing clients are
    unimpacted other than the new response headers.

  **Old call (legacy, kept alive until 2026-09-26):**
  ```bash
  curl -X POST http://127.0.0.1:8080/api/voiceover/generate-with-group \
       -d '{"text":"hello world","language":"en","voiceover_group":"boxe"}'
  # 200 + payload + Deprecation/Sunset/Link headers
  ```

  **New call (canonical, recommended):**
  ```bash
  curl -X POST http://127.0.0.1:8080/api/voiceover/generate \
       -d '{"text":"hello world","language":"en",
            "destination":{"kind":"group","group":"boxe"}}'
  # 200 + same payload; no deprecation headers
  ```

### Added

**[PR-VO-C1]** New `DestinationRequest.Kind` field (string; values: `""`,
`"group"`, `"explicit"`). Drives routing strategy at handler boundary:

  - `kind: "group"` — GroupsResolver dispatches `Group → FolderID`
    at request time, stamp the resolved folder back onto the
    destination so downstream service code sees only populated
    `FolderID`.
  - `kind: "explicit"` — caller-supplied `FolderID` is used verbatim
    (no resolver call).
  - `kind: ""` (default) — legacy auto-detect: `FolderID > Group >
    config-level voiceover folder`.

  The handler at `internal/api/assets/voiceover/handler.go::Generate`
  enforces fail-fast semantics on `kind: "group"` + empty `Group`
  (hard 400 at handler boundary) per godlike/07 §"No fake availability".

### Changed

**[P0.2, Wave]** Voiceover single-endpoint canonical wire shape. POST
`/api/media/voiceover/generate` now binds the typed
`GenerateVoiceoversRequest` (`internal/api/assets/voiceover/types.go`)
instead of binding the internal `*GenerateVoiceoversCommand` flat. The
previous flat binding violated AGENTS.md Pattern 6 (wire-shape/payload
split) — a future rename of the internal Command field set would have
silently leaked to the wire format. The wire-shape split keeps the API
contract independent of internal refactors.

Canonical wire shape (JSON, snake_case):

```json
{
  "request_id": "video-123",
  "items": [
    {"text": "Testo da leggere", "language": "it-IT", "voice": "it-IT-DiegoNeural", "filename": "intro-it.mp3"}
  ],
  "destination": {"kind": "group", "group": "Promozionali"},
  "options": {"remove_silence": true, "strategy": "replace", "parallelism": 2}
}
```

Translation contract (the request → canonical Command mapper):

- `items[]` collapses into the canonical `GenerateVoiceoversCommand`
  shape (1 `Text` + `Languages []string` +
  `VoiceOverrides map[string]string` keyed by language).
- All items in `items[]` MUST share the same text in P0.2 — mixed-text
  requests return 400 with `items[N].text: differs from items[0].text`
  in the error body. Per-item multi-text fan-out (one child job per
  item) is **P0.3 scope** (parent + child jobs per the Wave 21 plan).
- `request_id` propagates to `jobservice.EnqueueRequest.CorrelationID`
  so the worker-side log stream and the dispatcher audit can trace the
  original caller across the async boundary end-to-end.
- Unknown `options.strategy` values are normalised via
  `asset.NormalizeStrategy(force=false)` (unknown ⇒ `"verify"`,
  never failing closed on the wire).
- `destination.kind="group"` + empty `group` is a hard 400 per
  PR-VO-C1 / godlike/07 no-fake-availability invariant.
- Response (canonical 202 Accepted):
  ```json
  {"ok": true, "job_id": "...", "request_id": "...", "status": "queued", "total_outputs": N}
  ```

Files touched:

- `internal/api/assets/voiceover/types.go` — new; wires the request,
  `VoiceoverItem`, `VoiceoverOptions`, `Validate`, `ToCommand`,
  `ToEnqueueRequest`. The mapper locks last-wins semantics for
  duplicate-language items (VoiceOverrides map keyed by language)
  and last-non-empty-wins for duplicate FilenameTemplate sources —
  both edges locked with explicit tests for P0.3 inheritance.
- `internal/api/assets/voiceover/handler.go` — `BindJSON` target
  switched from `*GenerateVoiceoversCommand` to
  `*GenerateVoiceoversRequest`; package doc updated; HTTP entry
  surface unchanged (still only POST `/generate` mounts).
- `internal/api/assets/voiceover/handler_test.go` — new; round-trip
  enqueue suite (happy path, empty items, mixed-text reject, empty
  text/language reject, PR-VO-C1 invariant, `NormalizeStrategy`
  coercion, enqueue-error → 500, slim-surface assertion,
  duplicate-language/filename last-wins, correlation-ID propagation).
- `cmd/admin/gen_api_docs.go` — removed the stale
  `routeDescriptions` entries for `/api/media/voiceover/batch`
  (retired by Wave 21 / PR-VOICEOVER-RECOVERY V1..V7) and
  `/api/voiceover/sync` (retired); updated `/api/media/voiceover/generate`
  description to cite the P0.2 wire shape. The committed
  `docs/api/ACTIVE_API_GENERATED.md` will be regenerated by
  `./admin gen-api-docs` (P2.2).

Worker side regression test: the worker
(`internal/application/voiceover/jobs/generate_handler.go`) continues
to unmarshal `*GenerateVoiceoversCommand` from JSON via the same tags
the Command already carries — no worker change required. The wire
shape round-trips into the Command with the same JSON keys; the
P0.2 change is API-layer-only.

### Observability

**[PR-VO-C1]** New Prometheus counter `legacy_voiceover_route_invocations_total`
labelled by `route`. Operators expose this via `/metrics` to track
per-route usage during the 90-day sunset window. The companion helper
`LegacyVoiceoverDeprecationCount()` returns the cumulative invocation
count (dto.Metric writeback pattern) for admin/diagnostic surfaces.

---

### Added

**[P0.3, Wave 21, Commit 1]** Voiceover canonical parent + child job
topology — `voiceover.generate` enqueues N per-language child jobs
(`voiceover.generate_item`), one per (language, voice) pair. Worker-pool
per-capability concurrency replaces the previous in-process goroutine
fan-out inside the use case.

- **New canonical job type** `voiceover.generate_item`
  (registered in `internal/domain/job/job.go::TypeVoiceoverGenerateItem`
  and re-exported via `internal/application/jobs/registry.go`).
  Registry entry: `Concurrency: 4, Timeout: 10m, DefaultMaxRetries: 2`.
  The Concurrency field caps sibling parallelism once the in-process
  broker semaphore upgrade lands (PR-VO-D, follow-up wave).

- **`GenerateVoiceoverItemCommand`** is the canonical payload for
  the per-child job. Carries ONLY the data the child needs:
  `ParentJobID` + `RequestID` + `Text` + `Language` + `Voice` +
  `Filename` (pre-computed by parent) + `TextHash` (pre-computed by
  parent) + `Destination` + `Strategy` + `RemoveSilence` + `Metadata`.
  The child re-resolves destination so a transient resolver hiccup
  doesn't force the parent to re-run.

- **`FanoutVoiceoversUseCase`** is the parent-side scheduler. It
  validates the parent command, computes filenames + textHash via a
  shared package-local helper (no dual-implementation drift), then
  enqueues N voiceover.generate_item jobs in sequence. children share
  RequestID + TextHash. The parent job's `Job.WorkflowID` field is
  also set to the parent_job_id so future aggregator queries
  (`jobs WHERE workflow_id = ?`) return siblings without descending
  into payload JSON. **Partial fan-out returns err** — godlike/07
  no-fake-availability: parent dispatcher marks the parent job FAILED
  when any child enqueue fails, NO silent success.

- **`ProcessOneVoiceoverUseCase`** is the per-language executor.
  Holds a *voiceover.Service (canonical production surface) and
  delegates the single-language flow to `voService.GenerateBatch`
  with a single-language `BatchRequest`. Preserve the legacy
  3-state `RemoveSilence` semantic (nil | *bool(false) | *bool(true))
  so the "caller didn't set it" signal survives across the
  parent → child boundary. Future BACKFILL PR can migrate
  this dispatcher to call `GenerateVoiceoversUseCase.processOneLanguage`
  once the full 7-port surface is extracted; for P0.3, the
  voService.GenerateBatch path is the canonical SSOT.

- **`GenerateItemJobHandler`** is the per-language child worker
  handler. Holds the typed-port ProcessOneUseCase (Ports-only
  dependency per AGENTS.md Pattern 0). On cross-cutting failure
  (validate, dest resolve, no-items), surfaces both `err` AND a
  result map carrying `status: "failed"` so the aggregator (Commit 2)
  can correlate.

- **`GenerateJobHandler`** (parent) rewritten to dispatch ONLY to
  `FanoutVoiceoversUseCase.Execute(ctx, parentJobID, *cmd)`. The
  legacy in-process `executor.Run`-with-goroutines path is gone —
  the new contract is goroutine-free inside the API: NO goroutines
  ever spawn from the parent's HandleJob path.

Files touched:

- `internal/domain/job/job.go` — added `TypeVoiceoverGenerateItem`.
- `internal/application/voiceover/command.go` — added
  `GenerateVoiceoverItemCommand` + `Validate()`.
- `internal/application/voiceover/process_one_usecase.go` — NEW;
  per-language executor.
- `internal/application/voiceover/jobs/fanout_usecase.go` — NEW;
  parent-side scheduler.
- `internal/application/voiceover/jobs/fanout_usecase_test.go` —
  NEW; 7 unit tests.
- `internal/application/voiceover/jobs/generate_item_handler.go` —
  NEW; per-child worker handler.
- `internal/application/voiceover/jobs/generate_item_handler_test.go` —
  NEW; 4 unit tests.
- `internal/application/voiceover/jobs/generate_handler.go` —
  parent handler rewritten to dispatch via FanoutUseCase.
- `internal/application/jobs/registry.go` — exported
  `TypeVoiceoverGenerateItem`; registered in `Compose()` with
  `Concurrency: 4`.
- `internal/app/build_bundles_domain.go` — constructs
  `ProcessOneVoiceoverUseCase` from voService.
- `internal/app/composition.go` — `DomainBundle` gains
  `VoiceoverProcessOne` + `VoiceoverGenerateItemHandler` fields;
  late-binding block constructs FanoutUseCase + parent + child
  handlers and registers both with jobs.Service.
- `internal/app/voiceover_wiring_test.go` — boot smoke test now
  verifies BOTH `voiceover.generate` AND `voiceover.generate_item`
  have handlers after Register (regression guard against future
  refactors that drop the parent-child chain).

**Forward-pointer (Commit 2, deferred):** the COMPLETED/PARTIAL/FAILED
final-state aggregation THE parent closes its lifecycle AFTER all
children reach terminal status. That lands in Commit 2 via the
canonical outbox events + an aggregator worker that reads each
child's terminal event and tallies the parent's final Result. The
parent job currently reports SUCCEEDED on enqueue-complete (the
intermediate state) — Commit 2 will RE-finalise based on outbox
events with completed (all ok) / partial (≥1 ok, ≥1 failed) / failed
(all failed) semantics, mapping to the canonical 7-state kernel
status: SUCCEEDED + result.partial = true (final succeeded with
partial flag) / SUCCEEDED (all ok) / FAILED (all failed).

**[P0.6 (June 2026), Wave 21]** Voiceover / Artlist P0 cleanup
(`AGENTS.md Active Concerns #11` CLOSED). Two-part closure removing the
two remaining godlike/07 silent-success patterns in the
Voiceover / Artlist slice.

- **P0.6 Parte A — EnrichAsync fire-and-forget capability removed**
  (`internal/application/assets/providers/artlist/`):

  - **Port signature** — `MetadataWriter.EnrichAsync` deleted from
    `ports.go:255`. Only `Enrich` (synchronous) remains.
  - **Concrete impl** — `SemanticEnricher.EnrichAsync` deleted (incl.
    `concurrent.SafeGo` goroutine + `context.WithoutCancel` +
    30s timeout deps); `pkg/concurrent` import cleanup.
  - **Stage wrapper** — `stageEnrichAsync(ctx, *RunTagResponse)`
    function deleted from `run_orchestrator_stages.go` (25-line
    function plus doc comment).
  - **External call sites replaced** — no fake-success anywhere:
    - `run_service.go:48` sequence comment updated to remove
      `EnrichAsync` from the pipeline flow + added forward-pointer
      to P0.18.
    - `run_service.go:110` `o.stageEnrichAsync(ctx, resp)` call
      deleted.
    - `search_core.go:204–208` replaced with a documented log+drop
      block (no fake-success: caller surfaces the deprecation
      instead of letting in-process fire-and-forget silently
      elevate failures to success).

- **P0.6 Parte B — silent-success translator fallbacks replaced**:

  - `internal/domain/script/generation_result.go` —
    `scriptpkg.VideoMetadata.TranslationStatus string
    `json:"translation_status,omitempty"`` field added with
    triple-state semantics (`"translated"` /
    `"untranslated"` / `""` legacy backward-compat).
  - `internal/application/scripts/dto/metadata.go` —
    `MetadataTranslator` interface (Pattern 0 port, 2 methods;
    `*ollama.Generator` satisfies implicitly) replaces the
    `*ollama.Generator` direct dep. Function signature
    `GenerateVideoMetadata(ctx, generator *ollama.Generator, ...)`
    becomes
    `GenerateVideoMetadata(ctx, generator MetadataTranslator, ...)`.
    All 3 silent fallback sites removed:
    1. Title: `meta.Title = title` (on err) → empty + status
       downgrade.
    2. Description: `meta.Description = enDesc` (on err) → empty.
    3. Tags: `translatedTags = append(...)` fallback to source tag
       → empty + status downgrade.
    `enOK := err == nil && (desc != "" || len(tags) > 0)` guards
    against the silent empty-payload success variant (LLM returns
    no err but empty payload).
  - `internal/application/scripts/usecase/flow_helpers.go`:
    `ScriptArtlistClipSuggestion.TranslationError string
    `json:"translation_error,omitempty"`` field added.
    `artlistSearchPhrase(ctx, svc, phrase) string` →
    `artlistSearchPhrase(ctx, svc, phrase) (string, error)`.
    In `SearchArtlistClips`'s `concurrent.ParallelMap`
    callback the translate error is now propagated via
    `suggestion.TranslationError` and the Qdrant search is
    intentionally skipped (no silent fallback to the original
    phrase).

- **TDD coverage** — 8 new tests across 2 new files, 4 mock
  stubs (3 in `dto/metadata.go`, 2 in `usecase/flow_helpers.go`):

  - `internal/application/scripts/dto/metadata_test.go` — NEW;
    5 tests + 4 mock stubs
    (`mockTranslatorSuccess`, `mockTranslatorFailingTranslate`,
    `mockTranslatorFailingAll`,
    `mockTranslatorEmptyPayload`). Tests order-independent via
    `indexByLanguage` helper (concurrent.SafeGoFunc scheduling
    nondeterministic):
    1. `TestGenerateVideoMetadata_TranslationFailureDropsOriginal`
       (canonical regression: 4 languages, asserts no original
       text leaks across `it`/`es`/`fr`).
    2. `TestGenerateVideoMetadata_HappyPathPreservesTranslation`
       (positive control: en + it).
    3. `TestGenerateVideoMetadata_EnglishLLMFailureMarksUntranslated`
       (`enOK=false` → untranslated).
    4. `TestGenerateVideoMetadata_NilGeneratorReturnsEmpty`
       (nil short-circuit guard).
    5. `TestGenerateVideoMetadata_EnglishLLMEmptyPayloadMarksUntranslated`
       (silent empty-payload success variant).
  - `internal/application/scripts/usecase/flow_helpers_test.go`
    — NEW; 3 tests + 2 stub translators
    (`stubFailingTranslator`, `stubStubTranslator`):
    1. `TestArtlistSearchPhrase_TranslationFailureReturnsExplicitError`
       (per-call gate).
    2. `TestArtlistSearchPhrase_NilTranslatorReturnsExplicitError`
       (nil-translator gate).
    3. `TestSearchArtlistClips_TranslationFailurePropagatesToCaller`
       (caller-level gate: surface error + skip Qdrant search).

8 modified files + 2 new test files. Verify gate:
`go test ./internal/application/scripts/dto/... ./internal/application/scripts/usecase/... -count=3`
stable GREEN across 3 runs (no flakiness from goroutine scheduling).

**Backward-compat** — `TranslationStatus` field uses `omitempty`
so legacy test fixtures that pre-date the closure emit identical
JSON. Manual `[]scriptpkg.VideoMetadata{Language: "...", Title: "...",
...}` constructors at
`internal/application/scripts/usecase/generate_one_usecase.go:467`
+ `internal/application/scripts/adapters/generation_html_test.go:154`
round-trip cleanly (zero-value status → field omitted).

**Forward-pointer (P0.18, successive wave, see
`architecture/current.yaml#P0.18`)** — structured outbox-driven
enrichment replaces the deleted `EnrichAsync` capability. Until then,
search ingestion stores only the raw clip metadata and a separate
`/enrich` job handles semantic payload population. Filed
separately, not in this closure.

## Unreleased

### Added

**[FASE 9, DRIVE-005, June 2026]** Drive canonical `Admin` + `Reader`
port abstractions (Pattern 0) — the composition root's readiness barrier
now consumes a typed port instead of leaking the raw `*gdrive.Service`
into the lifecycle wiring.

- **New port interfaces** declared at
  `internal/infrastructure/drive/ports.go` (`Admin` for folder management
  + file lifecycle + uploads + liveness probe via `Ping`; `Reader` for
  download + metadata + listing + existence checks). Compile-time
  assertions at the bottom of the file: `var _ Admin = (*Uploader)(nil)`
  and `var _ Reader = (*Uploader)(nil)` — a signature drift between the
  port surface and the concrete `*drive.Uploader` is a build failure per
  AGENTS.md Pattern 0.

- **New `Uploader.Ping(ctx) error` method** in
  `internal/infrastructure/drive/uploader_ops.go` (line ~295) — calls
  `u.Service.About.Get().Fields("user").Context(ctx).Do()`. Nil-service
  guard: returns `fmt.Errorf("drive service not configured")` so the
  readiness barrier fails closed on misconfigured Drive.

- **Composition-layer wiring** (the canonical consumer) — the
  `internal/app/wire_services.go` `driveProbe` closure now reads
  `root.Drive.Admin.Ping(ctx)` instead of
  `root.Drive.DriveClient.About.Get().Fields("user")`. Bit-for-bit
  behavioural parity; the only difference is the consumer surface
  (typed port vs raw SDK).

- **Typed-nil-safe guard** at
  `internal/app/build_bundles_drive.go::BuildDriveBundle` — uses the
  canonical `var admin drive.Admin` + `if driveUploader != nil { admin =
  driveUploader }` pattern so the interface value stays true-nil when
  the uploader is nil. Without this guard, `Admin: driveUploader` would
  produce a non-nil interface holding a typed-nil pointer — the classic
  Go interface-nilness trap that would silently panic the readiness
  barrier on a Drive-feature-disabled deployment.

### Deprecated

**[DRIVE-005, June 2026]** `internal/app.DriveBundle.DriveClient` and
`internal/app.DriveBundle.DriveUploader` (the raw `*gdrive.Service` and
`*drive.Uploader` handles on the composition-root bundle) are
deprecated in favour of the typed-pattern ports
(`internal/app.DriveBundle.Admin` + `internal/app.DriveBundle.Reader`).

- **Canonical replacement**: `Admin` (liveness probe `Ping(ctx)`,
  folder management, file lifecycle, raw uploads) and `Reader`
  (download, metadata, listing, existence checks). All new code MUST
  consume via the typed ports per Pattern 0 + godlike/06 "one owner
  per fact".

- **Back-compat path**: the deprecated fields are retained for Wave 14+
  back-compat across the existing ~86 legacy callsites (`cmd/admin/*`,
  `internal/app/{build_bundles_*,lifecycle,module_*,registry_*}`,
  `internal/application/assets/ingest/sync/*`, the storage_wiring_test,
  and many others). The fields alias the SAME `*drive.Uploader`
  instance so **zero split-brain** — godlike/06 holds.

- **Deprecation record**: `architecture/deprecations.yaml#DRIVE-005-FIELDS`
  per godlike/07. Removal target: 2026-Q3 (aligned with the Wave 14
  mega-package split gate which is the natural deletion boundary).
  Status: EXPAND / in_progress as of FASE 9 landing.

- **Audit gate**: `rg 'root\.Drive\.DriveClient|root\.Drive\.DriveUploader'
  internal/` returns the current ~86 readsite count — the
  EXPAND-phase usage_metric baseline for `DRIVE-005-FIELDS`.

---

## Earlier (June 2026 wave)

See ARCHITECTURE.md §"Migration Status (Brutal Care Plan)" for the
historical record. Cross-references:

- **PR-VO-A1** through **PR-VO-A6** — voiceover P0 hardening bundle.
- **PR-VO-B1** — Drive upload split Processor ↔ Lifecycle
  (DriveUploaderPort).
- **PR-VO-B2** — metadata + StyleGroup propagation (no silent-drop
  through `processLanguage` + `resolveDestination`).
- **PR-VO-B3** — sync dedupe by `drive_file_id` + BCP-47 / compact
  locale parser (`pkg/localeutil/locale.go`).
