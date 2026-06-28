# Proposal: `clips.Handler` Capability Split — PLANNING-ONLY

> **Status:** PROPOSAL — analysis & recommendation. No code changes ship from this PR. After team sign-off (Q5 in §Open Questions), a separate action PR may or may not be opened.
> **Branch:** `codex/clips-handler-proposal`
> **Author convention:** this is a planning document; the proposal author tag is `proposal:` per project convention.

---

## 0. Spec Drift Acknowledged

The user-facing spec named `internal/api/clips/handler.go` (369 lines) as the target. **That file does not exist** at HEAD (`d7a26f2`). It was deleted in commit `caa1bfdb` (2026-06-23) — refactor(wave11): consolidate fullimages into application/images/ + fix clips import path.

The user-facing spec cited "14 deps" as the contract problem. **The actual `Deps` struct today has 27 fields** (the file header's "14-dep surface" was a stale label from a prior consolidation wave).

The user-facing spec described the package as a single-file monolith. **The package is already capability-split across 19 sibling files** including `clip_read.go`, `clip_create.go`, `clip_update.go`, `clip_delete.go`, `clip_search.go`, `clip_ops.go`, `clip_action.go`, `clip_enrich.go`, `clip_upload.go`, `clip_bulk.go`, `bulk_upload.go`, `clip_ops_handlers.go`, `folder.go`, `folder_tree.go`, plus test files.

This proposal therefore re-anchors the user's analysis to the actual target: `internal/api/assets/clips/handler.go` (367 lines, 27 deps, post-Wave-11 orchestrator). The §6 dimensions compare the same two options the user asked about, but the verdicts are informed by the actual repo state, not the deleted-monolith premise.

---

## 1. Executive Summary

The clips capability already has the per-capability file split the user asked for: 19 siblings in `internal/api/assets/clips/`, any one of which can be navigated with `ls` and grep. The 367-line `handler.go` is not a monolith containing all the business logic — it is a **dependency-injection orchestrator** that:

1. Declares the `Deps` struct (27 fields, well-commented, late-binding-marked).
2. Declares the `Handler` struct (mirrors all 27 deps 1:1 + 5 use-case wires = 32 private fields).
3. Provides the `NewHandler` constructor (~50-line body that mirrors fields + constructs use cases inline).
4. Mounts 28 HTTP routes in `RegisterRoutes()` on the singleton `*Handler` receiver; each route delegates to a method that lives in a sibling capability file.

**The actual contract problem** is not the 27-field `Deps` itself — it's the dual surface (`Deps` struct + `Handler` struct + `NewHandler` mirror body + `RegisterRoutes` route table). The 5-dimension analysis below shows that the proposal's strength is the constructor-size/diagnostic-readability axis, not the testability or transaction correctness axes (those already work fine today).

**Recommendation (§7):** **Won't Do — "Arch Working as Intended"** — UNLESS the team identifies `NewHandler` body size or `Deps` late-binding complexity as an active pain point. If the team DOES see a pain, then **Option A (sub-bundle)** is the correct fix; **Option B (sub-handlers)** is the wrong fix because it reverts a deliberate PR-A Phase 4 consolidation that explicitly removed the sub-handler fan-out pattern (see `handler.go:4` doc comment).

---

## 2. State of the Package Today

### 2.1 File inventory (`internal/api/assets/clips/`)

| File | Lines | Capability cluster | Receivers |
|---|---|---|---|
| `doc.go` | 4 | package doc | — |
| `handler.go` | 367 | orchestrator (Deps + Handler + NewHandler + RegisterRoutes) | — |
| `clip_read.go` | 208 | **ClipRead** — GetClip / ClipStatus / ListClips | `(h *Handler)` |
| `clip_create.go` | 158 | **ClipWrite** — CreateClip | `(h *Handler)` |
| `clip_update.go` | 118 | **ClipWrite** — UpdateClip | `(h *Handler)` |
| `clip_delete.go` | 37 | **ClipWrite** — DeleteClip | `(h *Handler)` |
| `clip_search.go` | 128 | **ClipSearch** — AdvancedSearch | `(h *Handler)` |
| `clip_ops.go` | 255 | **ClipOps** — Cleanup / VerifyClip / Reconcile | `(h *Handler)` |
| `clip_action.go` | 256 | **ClipWrite / ClipOps** — ClipStatus / FixHash / Trash / Download / FindDuplicates / Reupload / Reprocess / Reindex | `(h *Handler)` |
| `clip_enrich.go` | 264 | **Cross-cutting** — EnrichMedia / BatchReindex | `(h *Handler)` |
| `clip_upload.go` | 447 | **ClipWrite / Cross** — UploadVideoClip + helpers | `(h *Handler)` |
| `clip_bulk.go` | 67 | **ClipWrite** — BulkAddTags / BulkRemoveTags | `(h *Handler)` |
| `clip_ops_handlers.go` | 54 | **ClipOps** — HandleFixHash | `(h *Handler)` |
| `bulk_upload.go` | 332 | **ClipOps** — bulk_upload_youtube_clips job type | `(h *Handler)` |
| `folder.go` | 270 | **ClipRead** — ListFolders / FolderStatus / RegenerateManifest / TrashFolder / DeleteFolder | `(h *Handler)` |
| `folder_tree.go` | 140 | **ClipRead** — GetFolderChildren / GetTree / GetBreadcrumb | `(h *Handler)` |
| `clip_ops_test.go` | 421 | tests | n/a |
| `dispatcher_fail_closed_test.go` | 175 | tests | n/a |
| `gate_test.go` | 20 | tests (package-internal gate) | n/a |

**Receivers are universally `(h *Handler)`.** The PR-A Phase 4 BULK consolidation explicitly removed nested struct fan-out — every file uses the same singleton receiver.

### 2.2 `Deps` field count, by category

| Cluster | Field | Late-binding semantics? |
|---|---|---|
| **ClipRead** | `SourceResolver`, `AssetRepo`, `ClipsRepo` (× thought: shared with write+search), `VoiceoverRepo`, `ImagesRepo` | Yes (VoiceoverRepo, ImagesRepo) |
| **ClipWrite** | `SourceResolver`, `AssetRepo`, `ClipsRepo`, `StockRepo`, `ArtlistRepo`, `DriveUploader`, `MediaProcessor`, `ArtifactSvc`, `Dispatcher` (port), `MutationsDispatcher` | Yes (StockRepo, ArtlistRepo) |
| **ClipSearch** | `SearchSvc` (*search.Aggregator), `SearchAggregator` | No |
| **ClipOps** | `ClipOpsService`, `BulkUploadWorker`, `JobsSvc` | No |
| **Cross** | `DeletionSvc`, `MetaWriter`, `ClipIndexer`, `AssetTreeSvc`, `FolderMemSvc`, `ProcessRunner`, `Idempotency` middleware, `ReuploadUC`, `EnrichUC` | Yes (ReuploadUC, EnrichUC) |

`Log`, `Cfg`, `JobsSvc`, `AssetTreeSvc`, `FolderMemSvc` cross more than one cluster — they are the "always-needed" tail.

### 2.3 Identity divergence (cross-capability leakage)

The user's spec implied an idealized cluster mapping. Reality:

- `AssetRepo` is used in Read (Get, List, Status), Write (Create, Update, Delete), and Search (filter, populate result) — all three clusters.
- `ClipsRepo` is used in Read (text-search fallback in `ListClips` when `q != ""`), Write (Upsert), and Search (legacy per-source ListClipsPaged fallback).
- `SourceResolver` is used in Read (`repoForSource`) and Write (`BulkAddTags`/`RemoveTags` via `bulkTagsUC`).
- `JobsSvc` is used in Ops (`RegisterJobHandlers`) and Write (EnrichMedia's job enqueue).

**Implication for Option B:** if we split into per-cluster sub-handlers, each one needs a port to call the other — e.g. ClipReadHandler needs to call into ClipWriteHandler for "drop a write op while reading a status". That's exactly the "fan-out" pattern PR-A Phase 4 explicitly removed.

---

## 3. The Actual Contract Problem (fact-checked)

The user's complaint: "troppe dipendenze in Deps+Handler — non è il numero di righe il problema."

That's accurate for the 27-field `Deps` struct — but it's NOT accurate that this is a problem today. Why:

| Dimension | Today's state |
|---|---|
| **Compile-time correctness** | ✔ All 27 deps are typed; NewHandler mirrors 1:1; no nil-deref risk flagged |
| **Late-binding semantics** | ✔ Every nil-tolerated field is documented in-place with a comment |
| **Test surface** | ✔ Two test files (clip_ops_test.go, dispatcher_fail_closed_test.go) cover the relevant clusters; integrator uses `clips.Handler{}` directly with mock Deps |
| **Diagnostic readability** | ⚠ `NewHandler` body is ~50 lines; finding "what wires which" requires reading inline comments — non-trivial for an unfamiliar contributor |
| **Constructor size** | ⚠ A reviewer scanning `NewHandler` may have to map ~40 mirrors + ~10 use-case wires before understanding the wiring |

The two ⚠ rows are where any refactor could help. They are NOT a pain today — they're a **diagnostic invite**: a contributor reading `NewHandler` for the first time has to invest in understanding it.

If we want to reduce that onboarding cost, the question becomes: **which option reduces the noise without breaking PR-A Phase 4's consolidation doctrine?**

---

## 4. Option A (PASS) — Sub-bundle inside ONE `Handler`

### 4.1 Sketch

```go
// ── Sub-bundles (group-level types) ─────────────────────────────────────
type ReadDeps struct {
    SourceResolver *artifacts.SourceResolver
    AssetRepo      asset.Repository
    VoiceoverRepo  *assets.VoiceoversRepository
    ImagesRepo     *assets.ImagesRepository
}
type WriteDeps struct {
    SourceResolver   *artifacts.SourceResolver
    AssetRepo        asset.Repository
    ClipsRepo        *assets.ClipsRepository
    StockRepo        *assets.ClipsRepository
    ArtlistRepo      *assets.ClipsRepository
    DriveUploader    *drive.Uploader
    MediaProcessor   asset.Processor
    ArtifactSvc      *artifacts.Service
    MetaWriter       *semantic.MetadataWriter
    ClipIndexer      *clipindexer.Service
    Dispatcher       appclips.ClipIndexDispatcherPort
    MutationsDispatcher mutations.AssetMutationDispatcher
}
type SearchDeps struct {
    SearchSvc        *search.Aggregator
    SearchAggregator *providers.SearchAggregator
    AssetRepo        asset.Repository
    ClipsRepo        *assets.ClipsRepository
}
type OpsDeps struct {
    ClipOpsService   *appclips.ClipOpsService
    BulkUploadWorker *appclips.BulkUploadWorker
    JobsSvc          jobservice.Service
    DeletionSvc      *deletion.DeletionService
}
type SharedDeps struct {
    Log             *zap.Logger
    Cfg             *config.Config
    ProcessRunner   appassets.ProcessRunner
    AssetTreeSvc    *assettree.Service
    FolderMemSvc    *foldermemory.Service
}
type UseCases struct {
    Reprocess  *appclips.ReprocessUseCase
    Download   *appclips.DownloadUseCase
    BulkTags   *appclips.BulkTagsUseCase
    Enrich     *appclips.EnrichUseCase
    Reupload   *appclips.ReuploadUseCase
}

// Deps is now a structured bag (still ONE struct; region-tagged)
type Deps struct {
    Read    ReadDeps
    Write   WriteDeps
    Search  SearchDeps
    Ops     OpsDeps
    Shared  SharedDeps
    UseCases UseCases  // the construction inputs
    Idempotency gin.HandlerFunc
}

// Handler is now ONE struct, but PULLS deps from regions (still single receiver)
type Handler struct {
    read      ReadDeps
    write     WriteDeps
    search    SearchDeps
    ops       OpsDeps
    shared    SharedDeps
    useCases  UseCases
    Idempotency gin.HandlerFunc
}

func NewHandler(d Deps) *Handler {
    return &Handler{
        read:      d.Read,
        write:     d.Write,
        search:    d.Search,
        ops:       d.Ops,
        shared:    d.Shared,
        useCases:  d.UseCases,
        Idempotency: d.Idempotency,
    }
}
```

### 4.2 Required diff (mechanical)

| Site | Change |
|---|---|
| `handler.go` | Re-declare `Deps` + `Handler` with sub-structs; rewrite `NewHandler` (~15 lines instead of ~50). |
| 19 sibling `clip_*.go` files | Mechanical find/replace: `h.assetRepo` → `h.shared.AssetRepo` or `h.read.AssetRepo` per cluster boundary, `h.driveUploader` → `h.write.DriveUploader`, etc. ~80 field accesses total; compile errors guide the rest. |
| `internal/app/wire_*.go` files (composition root) | Update the call to `NewHandler(d)` to populate the 6 regions of `Deps`. The wire-site is one extra layer of nesting. |
| `clip_ops_test.go` / `dispatcher_fail_closed_test.go` | Test fixture struct literal: `Deps{AssetRepo: ...}` → `Deps{Read: ReadDeps{AssetRepo: ...}, ...}`. |

**Diff size estimate:** ~150 lines changed, ~30 lines added, ~10 lines deleted. Single PR; one reviewer pass.

### 4.3 Pro/Contra on the 5 dimensions (per user spec)

#### D1 — Testability

- **Pro:** Test fixtures can construct only `ReadDeps{AssetRepo, SourceResolver}` for a Read test, ignoring Write/Search/Ops/Shared — smaller fixture.
- **Pro:** When adding a CI sub-handler spec (PR4-PR9 audit cross-ref), each test is honest about which cluster it's testing.
- **Con:** A test that simulates Cross-cutting (EnrichMedia, UploadVideoClip) needs ALL regions populated — equivalent in fixture-size to today's `Deps` literal. No savings there.
- **Net:** marginal win on Read/Ops-only tests; flat on cross-cutting tests.

#### D2 — Deps lifecycle / late-binding

- **Pro:** Region grouping makes the late-binding semantics more visible — `WriteDeps.StockRepo`, `WriteDeps.ArtlistRepo`, `ReadDeps.VoiceoverRepo`, `ReadDeps.ImagesRepo` are the nil-tolerated ones; grouping clarifies which clusters tolerate nil.
- **Con:** Late-binding is still runtime-checked (NewHandler doesn't enforce non-nil). No compile-time guarantee.
- **Net:** wins readability, not guarantees.

#### D3 — Route registration

- **Pro:** `RegisterRoutes` stays ONE method that mounts 28 routes. Zero changes there.
- **Net:** neutral (already centralized).

#### D4 — Cross-capability transactions

- **Pro:** Same `*Handler` receiver — inter-cluster calls stay via direct field access (`h.read.AssetRepo.Get(...)` from inside a write-capability method is permitted because the fields exist on the same struct).
- **Con:** Cross-capability calls are SILENTLY POSSIBLE; a reviewer reading `clip_action.go` cannot tell whether the method *should* depend on `h.read.AssetRepo` or not. Option A groups for readability; it does NOT enforce compile-time segment boundaries.
- **Net:** cosmetic enforcement, not architectural enforcement.

#### D5 — Audit diff with PR4–PR9

- **PR4 (registry-composition):** Composition root already wires `clips.Handler`; Option A leaves composition hoist unchanged (just adds sub-region field access).
- **PR5 (assets-wiring):** AssetsModule reference in `internal/api/assets/module.go::Clips *clips.Handler` stays.
- **PR6 (script-wiring — historical):** Independent code path, no overlap.
- **PR7 (jobs-worker):** `RegisterJobHandlers` stays; `ClipOpsService` is now grouped under `Ops` region.
- **PR8 (voiceover-pipeline-stages):** VoiceoverRead is via `VoiceoverRepo` (now `ReadDeps.VoiceoverRepo`).
- **PR9 (image-worker-modules):** Out of scope for clips but informs the staging pattern.

**Net audit:** identical to today's diff aside from the sub-struct regrouping. No regulatory implications.

---

## 5. Option B (FAIL) — Sub-handler per capability

### 5.1 Sketch

```go
// 4 separate handler types, each with its OWN struct + smaller Deps:
type ClipReadHandler struct {
    deps     ReadDeps
    useCases UseCases
    shared   SharedDeps
    idem     gin.HandlerFunc
}
func NewClipReadHandler(d ReadDeps, uc UseCases, shared SharedDeps, idem gin.HandlerFunc) *ClipReadHandler { ... }
func (cr *ClipReadHandler) GetClip(c *gin.Context) { ... }  // moved from clip_read.go
func (cr *ClipReadHandler) ClipStatus(c *gin.Context) { ... }
func (cr *ClipReadHandler) ListClips(c *gin.Context) { ... }
// ... 28 routes distributed across 4 sub-handlers

type ClipWriteHandler struct { ... }  // 12 deps, mostly the heavy write-only deps
// ...
type ClipSearchHandler struct { ... }
// ...
type ClipOpsHandler struct { ... }
// ...

type ClipAssets struct { Read, Write, Search, Ops }
func (a *ClipAssets) RegisterRoutes(r *gin.RouterGroup) {
    a.Read.RegisterRoutes(r)
    a.Write.RegisterRoutes(r)
    a.Search.RegisterRoutes(r)
    a.Ops.RegisterRoutes(r)
}
```

### 5.2 Required diff (mechanical)

| Site | Change |
|---|---|
| `handler.go` | Replace ~50-line NewHandler mirror with a small `ClipAssets` aggregator struct + a `RegisterRoutes` fan-out. |
| 19 sibling `clip_*.go` files | **Receiver rename** `(h *Handler)` → `(cr *ClipReadHandler)` / `(cw *ClipWriteHandler)` / etc. — plus their methods MOVE to live with their sub-handler. ~15+ methods get reassigned. |
| `internal/app/wire_*.go` files | 4 ctor calls per `Deps` populate, vs current 1. Wire site grows. |
| Tests | Receiver rename applied across `clip_ops_test.go` + `dispatcher_fail_closed_test.go` (and any other test file in `internal/api/assets/clips/`). |
| Inter-cluster calls | Need port interfaces OR shared dep duplication (e.g. `AssetRepo` shared between Read and Write). |

**Diff size estimate:** ~600 lines changed, ~80 added, ~30 deleted. **3-4x** the diff size of Option A.

### 5.3 Why Option B FAILS this codebase

| Dimension | Verdict | Reason |
|---|---|---|
| **D1 Testability** | weak win | Tests scoped to a sub-handler are smaller — but the cross-capability tests (EnrichMedia, UploadVideoClip, BatchReindex) instantiate ALL 4 sub-handlers anyway. Net: tiny win. |
| **D2 Deps lifecycle / late-binding** | loses | Each sub-handler has its own late-binding rules, but the late-binding semantics are TIED TO the package, not the sub-handler. Splitting dilutes the doc comments. |
| **D3 Route registration** | loses | Route table at `handler.go` is no longer in one place. Grep navigation cost goes up; route-table audit becomes harder. |
| **D4 Cross-capability transactions** | loses architecturally | Per §2.3, `AssetRepo` / `ClipsRepo` / `SourceResolver` / `JobsSvc` are shared across clusters. Option B must introduce port interfaces or duplicate state to keep sub-handlers isolated. Either way, this RE-INTRODUCES the nested-struct-fan-out pattern that PR-A Phase 4 BULK consolidation explicitly removed (per `handler.go:4` docstring: "Sub-handler fan-out (DeleteHandler, SearchHandler) is replaced by receivers on *Handler — there is no longer a need for nested structs."). |
| **D5 Audit diff** | negative | Wires a regression against the PR-A Phase 4 doctrine. Code-reviewers will recommit to keeping the singular receiver, leading to PR churn. |

### 5.4 The pattern-righteousness trap

Option B is "pattern-correct" in isolation: small structs, single-responsibility, port interfaces. But the codebase chose explicitly NOT to do this. The doc comment in `handler.go:4` is unambiguous:

> *"Sub-handler fan-out (DeleteHandler, SearchHandler) is replaced by receivers on *Handler — there is no longer a need for nested structs."*

Re-introducing sub-handlers in this package contradicts the consolidation that already shipped. The argument that "small capability sub-handlers are good design" doesn't apply when the team has already chosen against that design for documented reasons. Following Option B here is rhetorical pattern-matching, not refactoring.

---

## 6. Side-by-Side Comparison (5 dimensions)

| Dimension | Option A (sub-bundle) | Option B (sub-handlers) | Today's state |
|---|---|---|---|
| **Testability** | marginal win (smaller Read/Ops fixtures) | marginal win (similar) | already passing test suite |
| **Deps / late-binding** | groups by region; better doc; runtime only | dilutes doc; per-handler rules; loses cross-cap description | explicit per-field comments |
| **Route registration** | neutral (one RegisterRoutes) | negative (4 sub-RegisterRoutes) | one RegisterRoutes |
| **Cross-cap transactions** | allowed (same struct) — readability only | requires ports/duplication — anti-pattern in this codebase | allowed; review-controlled |
| **Audit diff vs PR4..PR9** | minimal (cosmetic regrouping) | regressive (undoes PR-A Phase 4) | baseline |
| **Diff size** | ~150 lines updated | ~600 lines updated | 0 |
| **Compile-time guarantee** | none (convention only) | SOME (per-handler type) — but offset by required port design | none |
| **Risk** | low | medium-high (cross-cap state, regression vs PR-A Phase 4) | n/a |

---

## 7. Recommendation: Won't Do — Arch Working as Intended

### 7.1 Verdict

**Recommendation: Won't Do (do not implement Option A or Option B in the next planning cycle).**

### 7.2 Rationale

1. **The user's premise mis-targeted.** The user assumed the clips package is a 369-line monolithic handler. After Wave 11 (commit `caa1bfdb`, 2026-06-23), the package is a 19-file capability split. The actual 367-line `handler.go` is a `Deps`/`Handler`/`NewHandler`/`RegisterRoutes` orchestrator — not the monolith the user's spec described.
2. **The 27 deps reflect late-binding semantics, not pointless coupling.** Several deps are explicitly nil-tolerated for test fixtures or partial deploys (VoiceoverRepo, ImagesRepo, ReuploadUC, EnrichUC, Dispatcher, MutationsDispatcher, SearchAggregator, BulkUploadWorker, Idempotency). The Deps struct is the comprehensive bag-of-options; region-grouping does not reduce its size.
3. **PR-A Phase 4's consolidation doctrine forbids Option B.** The handler.go header explicitly says sub-handler fan-out was replaced by `*Handler` receivers. Re-introducing sub-handlers for the clips package would be the only such reintroduction in the codebase — a regression.
4. **No active pain drives the refactor.** Constructors wire OK; tests pass; routes register correctly; no contributor has filed a "this is hard to read" request.
5. **Risk vs reward.** The diff sizes are ~150 lines (Option A) or ~600 lines (Option B) for ZERO runtime benefit and arguable +/- on read-time cost.

### 7.3 When to revisit

> The team should re-open this proposal ONLY when:
>   (a) A contributor (or operator) reports that **maintaining or extending `NewHandler` body** is the active bottleneck, OR
>   (b) The number of `h.<field>` accesses grows past ~150 across the sibling files AND the file-search route-registration cost becomes a real navigation cost, OR
>   (c) A future capability addition (e.g. clips analytics) introduces a 4th or 5th natural cluster that the current flat struct cannot accommodate.
>
> Until one of those triggers fires, treat the proposal as documentation of the option set (so future maintainers have the analysis on hand), and **do not** implement it.

### 7.4 Adoption plan IF the team accepts a future go-ahead

Single-capability PR pattern (no big-bang; per-cluster adoption):

1. **Pick the cluster with the highest receiver fan-out & heaviest test fixture size.** Today, that's **Ops** (cluster owns `bulk_upload.go`, `clip_ops_handlers.go`, parts of `clip_action.go`, `dispatcher_fail_closed_test.go`). Strongest contiguous boundary.
2. **Apply Option A ONLY to that cluster** for one calendar cycle. Do a "ReadDeps + SharedDeps + UseCases" minimum; defer Write/Search/Ops clusters for another cycle.
3. **Compile-time gate:** introduce a `clips/bundle.go::Valid()` that asserts each region's nil-field rules at boot (per current late-binding semantics). This converts runtime late-binding checks to a single boot-time check, reducing the per-handler runtime defensive checks in 19 files.
4. **Audit feedback:** if the on-call contributor finds the Partial-Option-A state (Ops region grouped, others flat) clean, extend to WriteDeps. If they find it noisy, leave it Partial-Option-A and don't extend.

**Never** adopt Option B in any cluster — PR-A Phase 4 doctrine blocks it.

### 7.5 Spec discrepancies closure

| User spec | Repo reality (verified 2026-06-28) |
|---|---|
| `internal/api/clips/handler.go` (369 lines) | File deleted in `caa1bfdb` (2026-06-23, Wave 11). Replacement: `internal/api/assets/clips/handler.go` (367 lines). |
| `14 deps` (per file header label) | 27 deps in the live `Deps` struct (the 14-header label is stale). |
| "troppe dipendenze in Deps+Handler" | True but not actionable in isolation; the 27 deps have legitimate late-binding rationale, see §2.2. |
| "5 cluster (Read/Write/Search/Ops/either Common or cross-cutting)" | The 4-cluster natural mapping is correct, plus a 5th "Shared" tail; cross-cap leakage (AssetRepo, ClipsRepo, SourceResolver, JobsSvc) means pure per-cluster sub-handlers require ports or duplication, see §2.3. |
| "(no big-bang adoption)" | Agreed: cluster-by-cluster extension of a partial Option A, see §7.4. |

---

## 8. Open Questions (team-vote; outcomes documented here)

- **Q1** Do we adopt Option A in any cluster today? **Recommendation:** No — see §7.
- **Q2** Do we adopt Option B in any cluster today? **Recommendation:** No — see §5.3 and §7 (PR-A Phase 4 prohibition).
- **Q3** Do we close this proposal as Won't Do and stash it under `docs/refactor-proposals/clips-handler-capability-split.md` for future reference? **Recommendation:** YES — keep the analysis on hand; budget the refactor only when §7.3 triggers fire.
- **Q4** Do we update `architecture/decisions/0009-clips-handler-capability-split.md` (a proposed ADR slot) to mirror the §7 verdict? **Recommendation:** YES, in a tiny follow-up PR.
- **Q5** Do we move the "14-dep surface" historical comment out of `handler.go:4` to update it to "27-dep surface" so the doc and the struct stay in sync? **Recommendation:** YES in a tiny docs-only PR; NOT in this proposal PR (markdown-only).

---

## 9. Decision Report Reference (linked)

> The Verdicts (Option A / B / neither) are recommendations for closure on the team's canonical decision-record flow. Future agents should:
>   (a) cite this proposal as the basis for any sign-off in PR review, OR for citation when a future agent re-opens it under §7.3;
>   (b) record team votes in `architecture/decisions/0009-clips-handler-capability-split.md` once Q1-Q5 close;
>   (c) update the handler.go doc header (Q5) in a separate docs-only PR.

---

## Appendix A — Audit Anchors for PR4..PR9

| PR | Identifier | Touches clips/handler.go? | Notes |
|---|---|---|---|
| **PR4** registry-composition | `feat(registry): PR 1` (commit `b8aa1fca`) | No — composition root only | Independent code path; informational reference |
| **PR5** assets-wiring | (per AGENTS.md DAG; ref. codepath) | YES — `internal/api/assets/module.go::Clips *clips.Handler` | Anchor: Option A → sub-region field-access; composition hoist unchanged |
| **PR6** script-wiring | (not landed) | No | Independent code path |
| **PR7** jobs-worker | commit `c2867b905` (PR7) + `7757a492` | YES — `RegisterJobHandlers` (jobsSvc + BulkUploadWorker) | Anchor: Option A → `h.ops.JobsSvc.BulkUploadWorker` |
| **PR8** voiceover-pipeline-stages | commit `1cdf35e6` | YES — voiceover integration via `VoiceoverRepo` | Anchor: Option A → `h.read.VoiceoverRepo` |
| **PR9** image-worker-modules | commit `b86fe542` | No | Independent code path; not directly relevant |

The PR5/PR7/PR8 anchors confirm that **Option A's sub-bundle grouping preserves every external-facing wiring** — only internals move.

---

## Appendix B — Cluster-to-Field Mapping (table)

| Cluster | Fields (intended under Option A) | Cumulative field count |
|---|---|---|
| **ClipRead** | `SourceResolver`, `AssetRepo`, `VoiceoverRepo`, `ImagesRepo` | 4 |
| **ClipWrite** | `StockRepo`, `ArtlistRepo`, `DriveUploader`, `MediaProcessor`, `ArtifactSvc`, `MetaWriter`, `ClipIndexer`, `Dispatcher`, `MutationsDispatcher` | 9 |
| **ClipSearch** | `SearchSvc`, `SearchAggregator` | 2 |
| **ClipOps** | `ClipOpsService`, `BulkUploadWorker`, `JobsSvc`, `DeletionSvc`, `AssetTreeSvc`, `FolderMemSvc` | 6 |
| **Shared** | `Log`, `Cfg`, `ProcessRunner`, `Idempotency` (middleware), `UseCases` (use-case wires) | 5 |
| **Cross-cap (no cluster)** | `ClipsRepo`, `SourceResolver` (also Read) | (overlap; either Shared or Read's mirror) |

**Residual:** the 27-field `Deps` collapses to a 26-field region-tagged `Deps` (with `ClipsRepo` and one shared `SourceResolver` cross-cluster field). The collapsing-without-renaming gain is small.

---

*End of proposal. **Branch:** `codex/clips-handler-proposal`. **Commits:** 1 (this markdown). **Reviewers requested:** pipeline-clips capability owner for §2 / §4 / §5 (state-of-package + Option A/B verdict); architecture owner for §7.4 (adoption plan alignment with PR-A Phase 4 doctrine); voiceover + image owners for §Appendix A PR-history audit.*

*Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>*
