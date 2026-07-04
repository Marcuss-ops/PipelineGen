# External-Audit-2026-07-04 — Italian International Audit Reconciliation Action Plan

**Status:** in_progress
**Date:** 2026-07-04
**Author:** PipelineGen Agent (responding to user-pasted external audit)
**Owner:** architecture doc maintainer
**Audit anchor (input):** user's paste of Italian audit snapshot targeting public `Marcuss-ops/PipelineGen` ("51 commit, Go 97.1%", `size:>40000`, etc.).
**Wave-tracker anchor (canonical state):** `architecture/current.yaml#EXTERNAL-AUDIT-2026-07-04` (newly filed, lockstep with this file).
**Companion entries:** `architecture/deprecations.yaml` (deprecation schema, future contracts) + `AGENTS.md` §Recent cross-cutting closures (audit-pin mirror — out of scope here, follow-up PR).
**Audit-pin discipline:** per godlike/06 (one canonical owner per fact) + AGENTS.md "don't surprise downstream commits", this commit (a) lands this plan, (b) lands the wave-tracker entry, (c) lands CHANGELOG.md meta-entry. NO production code change; NO test code change; NO gofmt touch; NO migration. Single commit, direct-to-main per AGENTS.md Git-Lesson-2.

---

## 1. Honest Disclosure (godlike/07 no-fake-availability)

The user-paste describes public `Marcuss-ops/PipelineGen` ("51 commit" snapshot). The local refactored tree at commit `901b26c0` is FAR ahead of the public view. Per-file size reconciliation:

| Audit file | Audit claim (public) | Local refactored (this commit's tip) | Local reduction | Local prior-art waves |
|---|---|---|---|---|
| `internal/app/lifecycle.go` | "size:>40000, gestisce worker, scheduler, monitor, sweepers, job runner e startup plan" | **1152 LoC** | ~97% | FASE 3.7 monitor reclamation (commits 1a + 1b + 2 + 3); `lifecycle_sweepers.go` (259 LoC) + `lifecycle_job_runner.go` (204 LoC) + `server_lifecycle.go` (251 LoC) already split off. NOT-yet: full split-by-capability (PR-LIFECYCLE-SPLIT-BY-CAPABILITY P0-B). |
| `scripts/ci-architectural-checks.sh` | "size:>40000, decine di gate regex" | **3273 LoC** | ~92% | Checks 1 / 5 / 8 / 9 / 50 (S7-Step-7) / 51 (C4 Dispatcher) / 52 (C6 ArtifactUploader) / 53 (C7 CompleteJob) / 54 (FASE 3.7 monitor-infra lock) promoted across June-July 2026. NOT-yet: Go `cmd/archcheck` registry migration (PR-ARCHCHECK-GO-MIGRATION-PHASE-1 P0-B). |
| `internal/app/wire_assets.go` | "size:>30000, firma enorme, conserva parametri `catalogRepo` 'may reuse'" | **633 LoC** | ~98% | Renamed from `module_media.go` via commit `dbb9f569` per FIX-APP-MODULE-MEDIA-DISPATCHER closure; raw `MutationsDispatcher` literal removed; composition-time nil-gate via `WireAssets` defined. NOT-yet: split-by-capability (PR-WIRE-ASSETS-CAPABILITY-SPLIT P0-B) + nil-policy classification (PR-WIRE-ASSETS-NIL-CLASSIFICATION). |
| `internal/app/composition.go` | "size:>30000" | **657 LoC** | ~98% | DRIVE-005 closure (commit `a8c781ae` June 2026) physically removed raw `*gdrive.Service` from `DriveBundle`. STILL: 8 Qdrant fields retained (CollectionManager, QdrantDeleter, QdrantRuntime, LocatorCleaner, QdrantClient, QdrantHealthProbe, QdrantSearcher, + audit-pin comments at lines 165/173/182/191/201/206/211). NOT-yet: Qdrant live/dead decision (PR-QDRANT-FINAL-DECISION P0-B) + bundle-per-capability split (PR-COMPOSITION-BUNDLE-SPLIT P1-B, forward-cites GODOBJ-2026-07-03 Band 2). |
| `internal/api/mediasearch/handler.go` | "size:>30000, DTO + readiness + sanitize + error mapping + response mapping" | **766 LoC** | ~97% | AUDIT-2026-07-02 `PR-MANIFEST-STREAM-RECOVERY` (pending); AUDIT-2026-07-02 `PR-AGGREGATE-FILTER-UNIFORM` (shipped 2026-07-25). NOT-yet: dto/readiness/errors/sanitize/response_mapper file split (PR-MEDIASEARCH-HANDLER-SPLIT P1-B; forward-cites AUDIT-2026-07-02 PR-MANIFEST-STREAM-RECOVERY). |
| `internal/application/search/ports.go` | "size:>30000" | **674 LoC**, sibling files: `errors.go` (53) + `aggregator.go` (842) + `dedup.go` + `rank.go` + `score_local.go` + `cursor.go` + `telemetry.go` + `types.go` + `source_aliases.go` + `embedding_channel_registry_test.go` + others. `registry.go` does NOT exist (audit-aligned). | ~98% | Wave 30 (Semantic Multimodal Search) + AUDIT-2026-07-02 (mediasearch carry-forward) + `aggregate_filter_uniform` (shipped). NOT-yet: ports/registry/errors/types_query/types_result/document file split (PR-SEARCH-PORTS-SPLIT P1-B; forward-cites Wave 30). |

The audit's "they're still 40K+" view is stale against this commit's tip. The public `origin/main`'s last push (`03d42b0c fix(app): resolve workerruntime syntax errors (4 fixes)`) carries an older view; the local refactor includes ~80+ closed waves from the June-July 2026 wave tracker. **Honest framing:** each remaining hotspot is a **semantic smell still present in the (smaller) refactored file**, NOT a size-of-file problem.

---

## 2. Classification matrix (10 audit items × local status per godlike/07)

| # | Audit ID | File / Smell | Local status (rg-verified 2026-07-04) | Action |
|---|---|---|---|---|
| A1 | P0-hotspot-1 | `lifecycle.go` (1152 LoC) split-by-capability | STILL-P0-HERE (smaller, same god-shape) | NEW: PR-LIFECYCLE-SPLIT-BY-CAPABILITY (deadline 2026-08-15) |
| A2 | P0-hotspot-2 | `ci-architectural-checks.sh` (3273 LoC) Go-archcheck migration | STILL-P0-HERE (54 checks in same shell file) | NEW: PR-ARCHCHECK-GO-MIGRATION-PHASE-1 (deadline 2026-08-15) — also closes S4 |
| A3 | P0-hotspot-3 | `wire_assets.go` (633 LoC) split-by-capability | STILL-P0-HERE post-rename (FIX-APP-MODULE-MEDIA-DISPATCHER closed subset) | NEW: PR-WIRE-ASSETS-CAPABILITY-SPLIT (deadline 2026-08-15) |
| A4 | P1-hotspot-1 | `composition.go` (657 LoC) bundle split | PARTIALLY-DONE (DRIVE-005 closed raw `*gdrive.Service` from `DriveBundle`); Qdrant fields + 6+ stale audit comments carry-forward; GODOBJ-2026-07-03 Band 2 mechanical covers topology | PARTIAL: PR-QDRANT-FINAL-DECISION (new, P0-B 2026-08-01) + PR-COMPOSITION-BUNDLE-SPLIT (forward-cite GODOBJ-2026-07-03 P1-B 2026-08-22) |
| A5 | P1-hotspot-2 | `mediasearch/handler.go` (766 LoC) dto/readiness/errors/sanitize/response_mapper split | STILL-P1-HERE (handler-shape not yet split) | NEW: PR-MEDIASEARCH-HANDLER-SPLIT (deadline 2026-08-01) + FORWARD-CITE: AUDIT-2026-07-02 PR-MANIFEST-STREAM-RECOVERY (manifest-side) + AUDIT-2026-07-02 PR-AGGREGATE-FILTER-UNIFORM (filter-side) |
| A6 | P1-hotspot-3 | `search/ports.go` (674 LoC) ports/registry/errors/types_query/types_result/document split | STILL-P1-HERE; `registry.go` does NOT exist | NEW: PR-SEARCH-PORTS-SPLIT (deadline 2026-08-22) + FORWARD-CITE: Wave 30 (multi-channel) |
| S1 | Smell 1 | `lifecycle.go:407-430` — `yt-cache-prewarm` + `yt-nightly-prewarm` Start returns nil logged-disabled | CONFIRMED STILL PRESENT (rg: 2 prewarm steps at lifecycle.go:407 / 422 with `return nil` in Start func body + INFO log step disabled) | NEW: PR-LIFECYCLE-CAPABILITY-DISABLED-SENTINEL (deadline 2026-07-15) |
| S2 | Smell 2 | Qdrant ambiguous state — `composition.go` carries 8 Qdrant fields, `lifecycle.go` removed Qdrant steps | CONFIRMED STILL AMBIGUOUS (rg: 8 Qdrant fields at composition.go:168/173/182/191/201/206/211; `lifecycle_test.go` has qdrant-collection-stub + qdrant-cleaner-stub + qdrant-health-monitor-stop references at lines 96/133/142) | NEW: PR-QDRANT-FINAL-DECISION (deadline 2026-08-01) |
| S3 | Smell 3 | `wire_assets.go` nil policies inconsistent (searchFanOut nil → error; dispatcher nil → raw-repo fallback) | CONFIRMED INCONSISTENT (rg: nil checks at wire_assets.go:91/307/353/402/443/489/549/599); multiple `!ok || X == nil` patterns | NEW: PR-WIRE-ASSETS-NIL-CLASSIFICATION (deadline 2026-07-25) |
| S4 | Smell 4 | CI gate is mini-framework — 54 separate gate functions in shell | CONFIRMED STILL FRAMEWORK | MERGE with A2 (single ticket: PR-ARCHCHECK-GO-MIGRATION-PHASE-1) |

**Deduplicated list:** 9 net-new linked_issues (A2 ≡ S4 merged; A5 forward-cite only; A6 forward-cite only). Per godlike/06 SSOT, the wave-tracker `linked_issues` array carries only the net-new 9; the 2 forward-cite items ride on AUDIT-2026-07-02 + Wave 30 instead of duplicating.

---

## 3. Forward-Pointer Discipline / Union With Existing Waves

Per godlike/06 SSOT (one canonical owner per fact), the new tickets MUST NOT duplicate existing trackers:

| New ticket | Existing tracker that ALSO tracks the related surface | Resolution |
|---|---|---|
| PR-LIFECYCLE-SPLIT-BY-CAPABILITY | `GODOBJ-2026-07-03` does NOT include lifecycle.go (the audit found it absent from the 12-file list); `PRE-EXISTING-BUILD-ISSUES-2026-07-04` does NOT include either | NEW canonical entry under this wave |
| PR-ARCHCHECK-GO-MIGRATION-PHASE-1 | `S7-Step-7` (Entry 5: Check 50 promotion as the preamble pattern) | Forward-cite S7-Step-7 as the upstream for Check 50 migration precedent; new top-level wave carries the Go-registry migration |
| PR-WIRE-ASSETS-CAPABILITY-SPLIT | `FIX-APP-MODULE-MEDIA-DISPATCHER` already shipped (rename + raw-literal-removal via commit `dbb9f569`) | NEW canonical entry; the previous PR closed a SUBSET (rename + literal removal) but NOT the capability-split topology |
| PR-QDRANT-FINAL-DECISION + PR-COMPOSITION-BUNDLE-SPLIT | `GODOBJ-2026-07-03` Band 2 mechanical covers composition.go (Band-2 #7 target: `internal/app/composition.go`) | Forward-cite + new sub-ticket for Qdrant final-disposition (decide live-or-dead across 8 fields + 5 build_bundles_*.go) |
| PR-MEDIASEARCH-HANDLER-SPLIT | `AUDIT-2026-07-02 PR-MANIFEST-STREAM-RECOVERY` (manifest + readiness surface) pending | Forward-cite + new sub-ticket for the handler-shape file split (dto/readiness/errors/sanitize/response_mapper); shares AUDIT-2026-07-02 closure gate |
| PR-SEARCH-PORTS-SPLIT | `id: 30` (Wave 30 — Unified Semantic Multimodal Search) — 30 entries on ports/registry/channels/embedders | Forward-cite + new sub-ticket for file-by-file split (audit's specific 6-file prescription: ports/registry/errors/types_query/types_result/document) |

Per godlike/06 SSOT, the new wave-tracker entry cites these forward-references inline rather than redeclaring them.

---

## 4. Execution Order & Locks (godlike/07 EXPAND→BACKFILL→CUTOVER→CONTRACT)

1. **Wave-tracker registration first** (this commit): file `architecture/current.yaml#EXTERNAL-AUDIT-2026-07-04` with the 9 slim-schema `linked_issues` (AGENTS.md slim-schema per canonical current.yaml header).

2. **EXTERNAL-AUDIT-2026-07-04 work concurrent with**:
   - `GODOBJ-2026-07-03` Band 2 (composition.go mechanical split) — locks: composition.go + bundle-* files in `internal/app/`
   - `GODOBJ-2026-07-03` Band 1 abs (extraction_service) — locks: `internal/application/youtube/*` use-case surface
   - Wave 30 (semantic multimodal) — locks: `internal/application/search/*`, `internal/infrastructure/embeddings/*`

3. **NOT concurrent with** (sequenced after):
   - `AUDIT-RESIDUE-2026-07-04` PR-IMAGES-SHIM-REMOVAL (shipped 2026-07-04) — no contention
   - `AUDIT-RESIDUE-2026-07-04` PR-DRIVECLIENT-RAW-RETIRE (shipped 2026-07-04) — no contention
   - `AUDIT-RESIDUE-2026-07-04` PR-CHROME-PROVIDER-SPLIT (shipped 2026-07-04) — no contention
   - `AUDIT-RESIDUE-2026-07-04` PR-REFLECT-ELIM-HANDLER-REGISTRATION (shipped 2026-07-04) — no contention

4. **Sequencing within EXTERNAL-AUDIT-2026-07-04** (per-file ratchet — each PR passes `gofmt + go vet + go build + go test -short` on touched subtree):
   - PR-LIFECYCLE-CAPABILITY-DISABLED-SENTINEL (1 week, isolated edit of 2 Start funcs at lifecycle.go:407-430) → unblocks shutdown-discipline audit
   - PR-WIRE-ASSETS-NIL-CLASSIFICATION (1.5 weeks, type-class change on Wire* dep-getters at wire_assets.go:91/307/353/402/443/489/549/599) → unblocks
   - PR-QDRANT-FINAL-DECISION (4 weeks, retire 8 fields across composition.go + lifecycle.go + 5 build_bundles_*.go OR wire them up canonically) → unblocks
   - PR-LIFECYCLE-SPLIT-BY-CAPABILITY (5 weeks, file topology rewrite — split into `lifecycle_worker.go` + `lifecycle_scheduler.go` + `lifecycle_maintenance.go` + `lifecycle_monitor_adapters.go` + thin `lifecycle.go` orchestrator) → after sentinel
   - PR-WIRE-ASSETS-CAPABILITY-SPLIT (5 weeks, file topology rewrite — split into `wire_assets_clips.go` + `wire_assets_storage.go` + `wire_assets_search.go` + `wire_assets_diagnostics.go` + `wire_assets_voiceover.go`) → after nil-classification
   - PR-MEDIASEARCH-HANDLER-SPLIT (3 weeks, dto-split) → forward-cite
   - PR-SEARCH-PORTS-SPLIT (5 weeks, port-file topology) → forward-cite
   - PR-ARCHCHECK-GO-MIGRATION-PHASE-1 (6 weeks, parallel infra change) → parallel-track, non-blocking
   - PR-COMPOSITION-BUNDLE-SPLIT (5 weeks, post-QDRANT-FINAL-DECISION, forward-cites GODOBJ-2026-07-03 Band 2)

---

## 5. Migration sequence (godlike/07 EXPAND→BACKFILL→CUTOVER→CONTRACT)

For each ticket (e.g. PR-LIFECYCLE-SPLIT-BY-CAPABILITY):

- **EXPAND**: canonical split files land; legacy surface coexists in parallel. Wave-tracker entry flips to `status: in_progress / exit_signal: false`. `rg <legacy_symbol>` returns SAME count (no migration yet).
- **BACKFILL**: callers migrate to canonical split files. Each `rg <legacy_symbol>` hit decrements; ratchet counter at `architecture/current.yaml#EXTERNAL-AUDIT-2026-07-04.<ticket>.usage_metric`. `legacy_call_count` decreases toward 0.
- **CUTOVER**: legacy surface returns typed `ErrLegacySurfaceRetired` (declared in `internal/infrastructure/<surface>/errors.go`). `error.Is(err, ErrLegacySurfaceRetired)` check on the surface's last touchpoint surfaces the typed error per godlike/07 no-fake-availability.
- **CONTRACT**: physical removal via `git rm <legacy_file>` after grace period. A deprecation record (`architecture/deprecations.yaml#<ticket_id>`) is filed at EXPAND; flips `status: removed` at CONTRACT godlike/06 zero-baseline rule. NO `--force` per AGENTS.md Git-Lesson-2.

---

## 6. Honest Limitations (godlike/07)

1. **Static priority via LoC vs git-log frequency**: the user-paste ranked hotspots by file size. Per godlike/07 forward-pointer convention (PR-GODOBJ-HOTSPOT-CROSSREF at GODOBJ-2026-07-03, deadline 2026-08-01), the priority MUST cross-validate against `git log --since=90.days --pretty=format: --name-only | sort | uniq -c | sort -rn | head -30`. If a high-frequency hotspot NOT in this plan surfaces, the slim-schema `linked_issues` appendix-only ratchet adds it without inline edits.

2. **Public vs local view drift**: the user-paste reflects a public-side snapshot. As of 2026-07-04, `origin/main` is **1 commit ahead** of local (commit `03d42b0c fix(app): resolve workerruntime syntax errors (4 fixes)`). This plan lands AFTER local rebases onto `origin/main`'s tip per AGENTS.md Git-Lesson-2/4/5.

3. **Pre-existing build issues carry forward unchanged** (per CHANGELOG convention): the 5-item (now 4-item after FIX-STOCKPIPELINE-REDECLARATION closure) carry-forward is out-of-scope. Each per-ticket commit lands in isolation on its own subtree and passes targeted gates independently.

4. **Cross-package residue** (out-of-scope): asset-level vs stock-level `IndexingStatus` retirement is owned by `architecture/current.yaml#id-29` PR-CrossPackage-IndexingStatus-§12-5 (deadline 2026-08-15) — orthogonal to this audit.

5. **Qdrant migration risk**: if PR-QDRANT-FINAL-DECISION picks "retire", Wave 30 ingestion path (semantic multimodal) is affected. The decision MUST be locked BEFORE Wave 30's BACKFILL phase flips the ingestion-side choreography. Forward-pointer `PR-QDRANT-FINAL-DECISION-WAVE30-COORDINATION` tracks the cross-wave dependency.

6. **CI gate migration risk**: PR-ARCHCHECK-GO-MIGRATION-PHASE-1 spans shell→Go. Currently-running gates (Checks 1-54) are wave-ratchet transitional baselines; a Go migration can either (a) preserve them 1:1 as regressions on the Go side, or (b) deprecate them and require new Check-* ports per godlike/06 SSOT. PR-ARCHCHECK-GO-MIGRATION-PHASE-1 commits to option (a) per minimal-blast-radius.

7. **Public-repo preservation** (godlike/07 audit-pin): the public `Marcuss-ops/PipelineGen` HEAD on `origin/main` is `03d42b0c`. The local refactor carries ~80+ closed waves that are NOT visible on public. A future agent who clones `origin/main` and runs the audit against it would see the same `40K+` file sizes the user pasted. The audit is reconciled as a STATEFUL AT-TIME reading, not as future-debt inventory.

---

## 7. Wave-tracker entry pointer (canonical anchor)

`architecture/current.yaml#EXTERNAL-AUDIT-2026-07-04` is the canonical state surface. Per godlike/06 SSOT (one canonical owner per fact):
- **Narrative** lives here in the markdown plan (per-capability depth + dead-code reasoning + risk surface).
- **Status/state** lives in the YAML tracker (id + status + linked_issues + deadlines).
- **Migration discipline** lives in the per-ticket CHANGELOG entries (the meta-entry for THIS commit lands here too).
- **Audit-pin archaeology** lives in the godlike/06 audit-pins in production code (forward-cited where applicable; not retracted by this plan).

Operator surfaces read the YAML; engineering surfaces read the markdown; cross-reference is the wave-tracker entry's `linked_issues[].id` value.

---

## 8. Cross-references

- `AGENTS.md` §godlike/06 (SSOT; one canonical owner per fact) → §"Documentation Map"
- `AGENTS.md` §godlike/07 (typed-error contract; no fake availability) → §"API package: thin transport only" + §"Modular edit patterns"
- `AGENTS.md` Pattern 0 (port abstraction layer)
- `AGENTS.md` Pattern 5 (splitting a package; file-cap + capability-discipline)
- `AGENTS.md` Git-Lesson-2 (direct-to-main workflow; no `--no-ff`, no `--force`)
- `AGENTS.md` Git-Lesson-4 (non-fast-forward race recovery)
- `AGENTS.md` Git-Lesson-5 (byte-equivalent-replay acceptance)
- `architecture/current.yaml#GODOBJ-2026-07-03` (composition.go as Band-2 mechanical)
- `architecture/current.yaml#AUDIT-2026-07-02` (mediasearch carry-forward)
- `architecture/current.yaml#AUDIT-RESIDUE-2026-07-04` (recent residue closures — DRIVECLIENT-RAW / IMAGES-SHIM / CHROME-PROVIDER / REFLECT-ELIM all shipped)
- `architecture/current.yaml#S7-Step-7` (pkg/retry + Check 50 baseline)
- `architecture/current.yaml#id-30` (Wave 30 semantic multimodal search)
- `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` (5-item carry-forward)

---

## 9. Lifecycle (audit trail)

| Date | Action | SHA / State | Author |
|---|---|---|---|
| 2026-07-04 | Plan doc lands (this file) | (this commit) | PipelineGen Agent |
| 2026-07-04 | Wave-tracker entry lands (lockstep) | (this commit) | PipelineGen Agent |
| 2026-07-04 | CHANGELOG.md closure meta-entry lands | (this commit) | PipelineGen Agent |
| 2026-07-04 | Rebase onto `03d42b0c` + ff-push `origin main` | (this commit) | PipelineGen Agent |
| 2026-07-15 | PR-LIFECYCLE-CAPABILITY-DISABLED-SENTINEL deadline | (PR) | (TBD) |
| 2026-07-25 | PR-WIRE-ASSETS-NIL-CLASSIFICATION deadline | (PR) | (TBD) |
| 2026-08-01 | PR-QDRANT-FINAL-DECISION + PR-MEDIASEARCH-HANDLER-SPLIT deadline | (PR) | (TBD) |
| 2026-08-15 | P0-batch (LIFECYCLE-SPLIT + WIRE-ASSETS-SPLIT + ARCHCHECK-PHASE-1) | (PR batch) | (TBD) |
| 2026-08-22 | P1-batch (COMPOSITION-BUNDLE-SPLIT + SEARCH-PORTS-SPLIT) | (PR) | (TBD) |

---

**End of canonical action plan.** Future agents: read this file alongside `architecture/current.yaml#EXTERNAL-AUDIT-2026-07-04` for the canonical state surface, and alongside `architecture/current.yaml#AUDIT-2026-07-02` / `architecture/current.yaml#AUDIT-RESIDUE-2026-07-04` for the parallel audit waves that share surface (no double-tracking per godlike/06 SSOT).

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local> (per AGENTS.md Git-Lesson-3)
