# LONG-FILES-DECOMPOSITION-V2-2026-07-06 — Action Plan

> **Source**: `wc -l` analysis of all Go production files on `origin/main` (2026-07-06).
> **Authoring context**: supersedes `LONG-FILES-SPLIT-2026-07-06` (8-file scope, deadline 2026-08-31).
> The V1 wave's targets were largely slimmed by recent splits (e.g. voiceover/types.go 644→split,
> jobs/registry.go 731→570, adapters_infra.go 631→split by `152ca16d`). This V2 covers the
> CURRENT 31-file set that still breaches the AGENTS.md Pattern 5 cap (≤250 LoC).

> **godlike/06 4-surface lockstep (per CANONICAL.md §1)**:
> 1. `architecture/action-plans/2026-07-06-long-files-decomposition-v2.md` (this file — narrative)
> 2. `architecture/current.yaml#LONG-FILES-DECOMPOSITION-V2-2026-07-06` (wave-tracker entry, 31 slim `linked_issues`)
> 3. `CHANGELOG.md ## Unreleased → ### Refactor` (closure meta-entry)
> 4. `AGENTS.md ## Recent cross-cutting closures` section (mirror bullet)

---

## §1 — Honest disclosure (godlike/07 no-fake-availability)

The 31 target files were selected by **static wc -l on `origin/main` as of 2026-07-06**, post-V1
progress. The audit reflects the **REAL current state** of the codebase, not a re-statement of
the V1 set (which was largely retired).

**Excluded from this V2 plan (already handled OR tolerable per user spec):**

| # | LOC | File | Reason |
|---|-----|------|--------|
| X1 | 628 | `tests/operational/voiceover_harness.go` | Test harness — tollerabile per user spec |
| X2 | 613 | `cmd/admin/backfill_asset_embeddings.go` | CLI one-shot — accettabile per user spec |
| X3 | 603 | `scripts/archcheck/gates/gate_c2_source_catalog_only_main.go` | Gate executable — standalone, accettabile |
| X4 | 573 | `scripts/admin/generate_routes_yaml.go` | Codegen script (output is YAML, not Go) |

**Already covered by prior waves (carry-forward closure):**

| File | LOC | Existing wave | Status |
|------|-----|--------------|--------|
| `internal/application/jobs/registry.go` | 570 | `GODOBJ-2026-07-03` P0 | Awaiting closure — V2 re-confirms |

**Carry-forward preservation**: the 6-item pre-existing build issue list from
`architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` is unchanged — NOT a
regression of any V2 PR.

---

## §2 — V1 compatibility note

The V1 `LONG-FILES-SPLIT-2026-07-06` wave (deadline 2026-08-31) stays on the tracker
as `status: pending` — its 20 targets are NOW <500 LOC (largely already split). The
V2 wave does NOT supersede V1's tracker entry; both waves coexist per the
**append-only ratchet** principle (slim-schema discipline). The V2 tracker carries
the 31 NEW/UNSHIPPED targets. The forward-pointer `PR-LONG-FILES-HOTSPOT-CROSSREF-V2`
post-wave git-log cross-validation closes the audit-pin.

---

## §3 — 31 target files split into 4 priority bands

### 🔴 P0 CRITICAL (≥600 LOC) — 6 files — deadline 2026-07-15

| # | LOC | File | Split topology |
|---|-----|------|----------------|
| 1 | 674 | `internal/domain/finalization/types.go` | 3 files: `types_domain.go` (~250, artifact + publisher canonical) + `types_pipeline.go` (~220, upload intents + gates) + `types_dto.go` (~200, API request/response) |
| 2 | 661 | `internal/application/qdrant/legacyaudit/legacyaudit.go` | 4 files: `legacyaudit.go` (slim orchestrator ~80) + `audit_collection.go` (~200, collection snapshot) + `audit_payload.go` (~200, vector/payload inspection) + `audit_reconciler.go` (~180, drift detection) |
| 3 | 635 | `internal/infrastructure/database/sqlite/jobs/repository.go` | PER `152ca16d` already extracted `repository_stats.go`; RESIDUAL: extract `repository_events.go` (~220, list/scan/insert events) + `repository_jobs_crud.go` (~200) — total ~3-4 files in this sub-package |
| 4 | 629 | `internal/application/workerdoctor/default_probes.go` | 4 files: `default_probes.go` (slim registry ~80) + `probes_liveness.go` (~180, healthz/readiness) + `probes_dependency.go` (~220, DB/Qdrant/Drive deps) + `probes_invariant.go` (~180, business invariants) |
| 5 | 625 | `scripts/archcheck/checks.go` | 4 files: `checks.go` (slim checker registry ~100) + `checks_imports.go` (~180, import-graph rule) + `checks_coupling.go` (~170, package-dependency rule) + `checks_patterns.go` (~175, anti-pattern detection) |
| 6 | 601 | `internal/application/qdrant/reconciler/service.go` | 3 files: `service.go` (slim orchestrator ~120) + `service_drift.go` (~250, drift detection + supersede) + `service_projection.go` (~230, payload-mapper integration) |

### 🟠 P1 ALTA (550-599 LOC) — 11 files — deadline 2026-07-25

| # | LOC | File | Split topology |
|---|-----|------|----------------|
| 7 | 584 | `internal/application/assets/providers/stock/stockpipeline/ports.go` | 3 files: `ports.go` (canonical interface set ~150) + `ports_drives.go` (~250, Drive-side ports) + `ports_processing.go` (~190, processor/stager/cutter ports) |
| 8 | 581 | `internal/app/search_backends.go` | 4 files: `search_backends.go` (slim dispatcher ~100) + `backends_clipindexer.go` (~180) + `backends_qdrant.go` (~170) + `backends_hybrid.go` (~130, RRF fusion) |
| 9 | 580 | `internal/infrastructure/drive/uploader_ops.go` | 3 files: `uploader_ops.go` (slim: public envelope ~140) + `uploader_ops_single.go` (~220, single-file upload) + `uploader_ops_resumable.go` (~220, chunked/resumable upload) |
| 10 | 576 | `cmd/admin/qdrant_readiness_checks.go` | 3 files: `qdrant_readiness_checks.go` (CLI dispatcher ~150) + `readiness_collection_checks.go` (~230, per-collection probes) + `readiness_projection_checks.go` (~200, projection/integration probes) |
| 11 | 570 | `internal/application/jobs/registry.go` | AUDIT-PIN: is already in `GODOBJ-2026-07-03` P0 band; V2 entry flips to `shipped` when GODOBJ closure lands |
| 12 | 566 | `internal/api/assets/clips/handler.go` | 3 files: `handler.go` (slim registrator ~120) + `handler_read.go` (~230, GET routes) + `handler_write.go` (~220, POST/mutation/upload routes) |
| 13 | 563 | `internal/application/images/search_queries.go` | 3 files: `search_queries.go` (orchestrator ~120) + `queries_semantic.go` (~220, Qdrant semantic) + `queries_lexical.go` (~220, LIKE / FTS5-fallback) |
| 14 | 559 | `pkg/retry/retry.go` | 3 files: `retry.go` (canonical `Default` orchestrator ~150) + `classify.go` (~210, transient-classification logic) + `backoff.go` (~200, exponential + jitter math) |
| 15 | 556 | `cmd/admin/qdrant_readiness.go` | 3 files: `qdrant_readiness.go` (CLI dispatcher ~150) + `readiness_preflight.go` (~210, env/credential probe) + `readiness_schema.go` (~200, schema-version probe) |
| 16 | 550 | `internal/application/assets/lifecycle/service.go` | 3 files: `service.go` (slim orchestrator ~120) + `service_transitions.go` (~220, state-machine guards) + `service_projection.go` (~210, outbox/Qdrant projection) |
| 17 | 549 | `internal/infrastructure/indexing/clipindexer/indexing_api.go` | 3 files: `indexing_api.go` (slim public surface ~130) + `indexing_api_youtube.go` (~210, YouTube clip indexing) + `indexing_api_artlist.go` (~210, Artlist indexing) |

### 🟡 P2 MEDIA (525-549 LOC) — 7 files — deadline 2026-08-08

| # | LOC | File | Split topology |
|---|-----|------|----------------|
| 18 | 547 | `cmd/admin/cleanup.go` | 3 files: `cleanup.go` (CLI dispatcher ~150) + `cleanup_artifacts.go` (~220, artifact sweep) + `cleanup_outbox.go` (~180, outbox dead-letter sweep) |
| 19 | 544 | `internal/application/assets/providers/registry.go` | 3 files: `registry.go` (canonical registry ~120) + `registry_providers.go` (~230, per-provider registration) + `registry_capabilities.go` (~190, capability-flag tracking) |
| 20 | 543 | `cmd/admin/reconcile_qdrant.go` | 3 files: `reconcile_qdrant.go` (CLI dispatcher ~150) + `reconcile_drift.go` (~210, drift detection) + `reconcile_projection.go` (~190, payload re-projection) |
| 21 | 539 | `internal/application/voiceover/jobs/parent_aggregator.go` | 3 files: per `PR-VO-PARENT-AGGREGATOR-SPLIT` (already split into 4 files `parent_aggregator.go` + `parent_eligibility.go` + `parent_state_machine.go` + `parent_state.go` + `parent_aggregator_state.go` ~80 LOC each) |
| 22 | 539 | `internal/application/voiceover/finalizer.go` | 3 files: `finalizer.go` (slim orchestrator ~130) + `finalizer_media_assets.go` (~220, media_assets write) + `finalizer_outbox.go` (~200, outbox emission + cleanup) |
| 23 | 528 | `cmd/admin/qdrant_preflight.go` | 3 files: `qdrant_preflight.go` (CLI dispatcher ~150) + `preflight_env.go` (~190, env/credential probe) + `preflight_schema.go` (~190, schema-version probe) |
| 24 | 526 | `internal/platform/config/types.go` | 3 files: `types.go` (slim top-level types ~150) + `types_database.go` (~190, DB/Redis config) + `types_external.go` (~190, Qdrant/Drive/Ollama config) |

### 🟢 P3 BASSA (500-524 LOC) — 7 files — deadline 2026-08-22

| # | LOC | File | Split topology |
|---|-----|------|----------------|
| 25 | 518 | `internal/api/assets/clips/ingest.go` | 3 files: `ingest.go` (slim orchestrator ~130) + `ingest_preflight.go` (~200, source validation) + `ingest_persist.go` (~190, asset + outbox write) |
| 26 | 515 | `internal/infrastructure/acquisition/filesystem_stager.go` | 3 files: `filesystem_stager.go` (slim orchestrator ~130) + `stager_local.go` (~200, local-FS ops) + `stager_remote.go` (~190, Drive/HTTP fetch) |
| 27 | 510 | `internal/domain/asset/asset_types.go` | 3 files: `asset_types.go` (slim canonical `Asset` + `Location` ~150) + `types_lifecycle.go` (~190, lifecycle + deletion states) + `types_metadata.go` (~170, metadata + scoring) |
| 28 | 509 | `internal/application/assets/providers/artlist/semantic_enricher.go` | 3 files: `semantic_enricher.go` (slim orchestrator ~130) + `enricher_classify.go` (~190, semantic classification) + `enricher_persist.go` (~190, asset + outbox write) |
| 29 | 509 | `cmd/admin/reindex_qdrant.go` | 3 files: `reindex_qdrant.go` (CLI dispatcher ~150) + `reindex_walk.go` (~190, asset walk + filter) + `reindex_replay.go` (~170, replay-via-outbox) |
| 30 | 507 | `internal/infrastructure/drive/folder_manager.go` | 3 files: `folder_manager.go` (slim orchestrator ~130) + `folder_manager_create.go` (~200, create + nested segment) + `folder_manager_lifecycle.go` (~180, rename/archive/trash) |
| 31 | 504 | `internal/application/jobs/outbox/indexing.go` | 3 files: `indexing.go` (slim orchestrator ~130) + `indexing_compose.go` (~190, payload composition) + `indexing_dispatch.go` (~185, broker-side dispatch) |

---

## §4 — Execution order & locks

Per godlike/07 EXPAND→BACKFILL→CUTOVER→CONTRACT:

1. **EXPAND** — create the new split files (extracted code co-exists with the source); old file stays at full size.
2. **CUTOVER** — remove the extracted code from the old file, leaving only the slim orchestrator.
3. **CONTRACT** — deprecation record (only for surface-contract changes; for pure code-motion, no-op).

Per-PR lands **directly on `main`** per AGENTS.md Git-Lesson-2 (NO branches, NO `--no-ff`, NO `--force`).

**Per-PR sequencing**:

- **Phase 1 (P0 band, deadline 2026-07-15)**: 6 PRs in parallel (`PR-SPLIT-FINALIZE-TYPES-V2`, `PR-SPLIT-LEGACYAUDIT-V2`, `PR-SPLIT-JOBS-REPO-RESIDUAL`, `PR-SPLIT-WORKERDOCTOR-PROBES`, `PR-SPLIT-ARCHCHECK-CHECKS-V2`, `PR-SPLIT-QDRANT-RECONCILER`).
- **Phase 2 (P1 band, deadline 2026-07-25)**: 11 PRs; independent except `PR-SPLIT-JOBS-REGISTRY-V2` (audit-pin only, awaits GODOBJ).
- **Phase 3 (P2 band, deadline 2026-08-08)**: 7 PRs; mostly independent.
- **Phase 4 (P3 band, deadline 2026-08-22)**: 7 PRs; mostly independent.

**Band-level deadline ratchet** (per slim-shape discipline): each PR lands incrementally and flips
its `linked_issues[].status: pending → shipped` only when its targeted-subtree verification
gates are green. The parent wave entry flips to `status: shipped + exit_signal: true`
ONLY when ALL 31 `linked_issues` are `status: shipped`.

---

## §5 — Per-PR verification gates

Each per-file PR must pass on its **targeted subtree**:

```bash
gofmt -l <touched_files>                                       # must be clean
go vet ./<targeted_package>/...                                # must exit 0
go build ./<targeted_package>/...                              # must exit 0
go test -short -count=1 ./<targeted_package>/...               # must PASS
```

Pre-existing 6-item build issues from `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04`
are **out of scope** for V2 — each PR passes only on its targeted subtree.

---

## §6 — Migration sequence (godlike/07 minimum-blast-radius)

For these mechanical splits:

1. **Code-motion only**: every PR is a pure file split; NO new exported symbols, NO signature
   changes, NO dependency changes. The old file becomes the slim orchestrator; new files
   become capability-specific companions in the **same package** (no new import surface).
2. **Compile-time pins preserved**: any `var _ Port = (*Adapter)(nil)` stays in place.
3. **Canonical SSOT preserved**: each new file owns exactly one capability concern.

---

## §7 — Honest limitations (godlike/07)

1. **Static prioritization**: this V2 audit prioritized by raw LOC count only. Some files
   (e.g. `internal/platform/config/types.go`) are naturally large because the canonical
   config shape is single-source; the per-PR agent MUST validate the split improves cohesion
   before landing (an empty split is a HARD-FAIL per godlike/07 fail-closed-at-output).

2. **`jobs/registry.go`** is V2 placeheld at `status: pending` while it remains under
   `GODOBJ-2026-07-03` P0 band. After GODOBJ shipment, V2 flips its slot to `shipped` as
   audit-pin.

3. **Pre-existing 6-item build issue carry-forward** (`PRE-EXISTING-BUILD-ISSUES-2026-07-04`)
   UNCHANGED — NOT a regression of any V2 PR.

4. **No new feature work**: V2 is purely structural. New features are forward-pointed to
   their own waves (`VO-DECOMPOSITION-2026-07-04`, etc.).

5. **No test file splits**: `youtube_discoveries_test.go` (1554 LOC) and other large test
   files are separate concerns.

---

## §8 — Wave-tracker entry pointer (canonical anchor)

`architecture/current.yaml#LONG-FILES-DECOMPOSITION-V2-2026-07-06`:

- 31 net-new slim-shape `linked_issues` (1 per PR above).
- 4 per-band deadline anchors (2026-07-15 / 2026-07-25 / 2026-08-08 / 2026-08-22).
- 1 forward-pointer `PR-LONG-FILES-HOTSPOT-CROSSREF-V2` (deadline 2026-09-01).

**Status at registration**: `pending` (no V2 PR shipped yet).

**Status at V2 close**: `shipped + exit_signal: true` AFTER all 31 `linked_issues`
flip to `status: shipped` AND the post-wave git-log frequency cross-validation
(`PR-LONG-FILES-HOTSPOT-CROSSREF-V2`) surfaces ZERO new high-frequency hotspots not
captured here. Append-only ratchet per slim-schema SSOT.

---

## §9 — Cross-references

- `architecture/current.yaml#LONG-FILES-SPLIT-2026-07-06` — V1 (8-file scope, largely retired by recent splits)
- `architecture/current.yaml#LONG-FILES-DECOMPOSITION-2026-07-06` — V1 sibling wave-tracker (8 files)
- `architecture/current.yaml#GODOBJ-2026-07-03` — god-object decomposition (16 files, 4 bands); covers `jobs/registry.go`
- `architecture/current.yaml#CODE-QUALITY-AUDIT-2026-07-05` — anti-patterns audit (4 P0 + 3 P1)
- `architecture/current.yaml#CODE-QUALITY-CLEANUP-2026-07-04` — cleanup priorities (12 dirty areas)
- `architecture/current.yaml#VO-DECOMPOSITION-2026-07-04` — voiceover-specific decomposition (4 bands)
- `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` — 6-item carry-forward (NOT V2 regressions)
- AGENTS.md Pattern 5 — god-object / long-file split discipline (≤250 LoC/file cap)
- AGENTS.md Git-Lesson-2 — direct-to-main workflow (no branches, no `--no-ff`, no `--force`)
- AGENTS.md Git-Lesson-3 — `Co-authored-by` trailer discipline
- AGENTS.md Git-Lesson-4/5 — race-protect for byte-equivalent-replay recovery
- godlike/06 SSOT (one canonical owner per fact)
- godlike/07 no-fake-availability, typed-error contract, minimum-blast-radius

---

## §10 — Lifecycle (audit-trail)

- **2026-07-06**: V2 plan created (`pending` status). Covers 31 currently-untracked
  Go files >500 LOC on `origin/main`.
- **2026-07-15**: target close for Phase 1 (P0 CRITICAL — 6 PRs).
- **2026-07-25**: target close for Phase 2 (P1 ALTA — 11 PRs).
- **2026-08-08**: target close for Phase 3 (P2 MEDIA — 7 PRs).
- **2026-08-22**: target close for Phase 4 (P3 BASSA — 7 PRs).
- **2026-09-01**: forward-pointer `PR-LONG-FILES-HOTSPOT-CROSSREF-V2`
  runs `git log --since=90.days --pretty=format: --name-only | sort | uniq -c | ...`
  for post-wave frequency cross-validation.

**Co-authored-by**: PipelineGen Agent <agent@pipelinegen.local>
(per AGENTS.md Git-Lesson-3 auditability convention)
