# PipelineGen — CHANGELOG

Per godlike/07 (docs/architecture/godlike/07_ZERO_LEGACY_POLICY.md), this
CHANGELOG records every user-visible API and behavior change. Each entry
cross-references the architecture/deprecations.yaml record (if any) and
the canonical ARCHITECTURE.md section that owns the change.

---

## Unreleased

### Fixed

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

### Observability

**[PR-VO-C1]** New Prometheus counter `legacy_voiceover_route_invocations_total`
labelled by `route`. Operators expose this via `/metrics` to track
per-route usage during the 90-day sunset window. The companion helper
`LegacyVoiceoverDeprecationCount()` returns the cumulative invocation
count (dto.Metric writeback pattern) for admin/diagnostic surfaces.

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
