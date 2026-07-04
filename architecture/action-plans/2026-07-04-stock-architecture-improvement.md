# Stock Pipeline Architecture Improvement Plan

**Created**: 2026-07-04
**Status**: ACTIVE
**Owner**: PipelineGen Agent
**Scope**: `internal/application/assets/providers/stock/stockpipeline/`
**Rule**: NO BRANCHES — direct-on-main per AGENTS.md Git-Lesson-2

---

## Executive Summary

The stock pipeline has been through a massive refactoring (Stock Cutover series, July 2026) migrating from a monolithic `Service.Run` (~280 lines) to a 6-step `Orchestrator.RunResilient`. While the architectural direction is correct (Pattern 0 ports, typed errors, single-TX spine write), the implementation has accumulated **technical debt** that needs systematic cleanup:

- **2 god-object files** (949 + 914 lines) well over the 400-line threshold
- **95 stub references** creating fake-availability surfaces (godlike/07 violation)
- **Package-level `var rng`** (non-deterministic global state)
- **Heavy primitive obsession** (`map[string]any`, `json.RawMessage`, `map[string]string`)
- **2 silent error swallows** (`_ = ...`)
- **27 git-edit hotspot** on `service.go` (highest churn in the entire repo)
- **Feature envy**: direct `s.ytdlp.ListChannel` call in `query.go` bypassing the `SourceStager` port

---

## Priority Matrix (Frequency × Complexity)

| Priority | File | Lines | Git Churn (90d) | Issues |
|----------|------|-------|-----------------|--------|
| 🔥 **P0** | `service.go` | 914 | 27 edits | God-object, `var rng`, primitive obsession, 6 RETIRED references, infra imports |
| 🔥 **P0** | `orchestrator_steps.go` | 949 | 7 edits | God-object, 12 stubs, `_ = f.Close()`, pre-Commit-7 transitional code |
| **P1** | `query.go` | ? | ~7 edits | Feature envy: direct `s.ytdlp.ListChannel` |
| **P1** | `orchestrator.go` | 639 | 9 edits | `_ = o.stepStore.MarkFailed(...)`, oversized |
| **P2** | `ports.go` | 534 | ~5 edits | Oversized (can split cutter types from renderer types) |
| **P2** | `run_upload.go` | ? | 15 edits | File MISSING from disk (pre-existing build issue) |
| **P3** | `service.go` | — | — | `HandleJob` 2-path divergence (legacy return-map + orchestrator) |

---

## Action Items

### 🔥 P0: Split `service.go` (914 lines, 27 recent edits)

**Problem**: `service.go` is the highest-churn file in the stock area (27 edits in 90 days). It mixes:
- Constructor + validation (`NewService`, sentinel errors)
- Job handler (`HandleJob`, `RegisterHandler`)  
- Legacy DTOs (`RunInput`, `ChunkResult`, `PipelineResult`, `PipelineMetadata`, `ChunkMeta`, `SourceInfo`, `ClipInfo`, `PipelineInfo`)
- Source staging (`StageSource`, `stageSection`)
- Lease extraction (`extractLease`)
- Manifest projection helpers (`manifestBytes`, `projectManifestToPipelineResult`)

**Target split** (4 files, each under 300 lines):

| New File | Content | Est. Lines |
|----------|---------|------------|
| `service.go` | `Service` struct + `NewService` + `Deps` + sentinels | ~280 |
| `job_handler.go` | `HandleJob` + `RegisterHandler` + `extractLease` + `manifestBytes` | ~200 |
| `types_run.go` | `RunInput`, `ChunkResult`, `PipelineResult`, `PipelineMetadata`, `ChunkMeta`, etc. | ~180 |
| `source_staging.go` | `StageSource`, `stageSection` | ~140 |

**Pre-existing build issue note**: `FIX-MODULE-MEDIA-DISPATCHER` (clips.Deps.MutationsDispatcher literal) blocks `go build ./internal/app/...` — not blocking per-file split validation on `go build ./internal/application/assets/providers/stock/...`.

**Deadline**: 2026-07-11

---

### 🔥 P1: Split `orchestrator_steps.go` (949 lines, 12 stubs)

**Problem**: This is the single largest file in the stock pipeline. It defines:
- 6 step types + their `Run()` methods
- `StepRunner` interface + `orchestratorRunner` impl
- `runState` struct
- `DefaultStockSteps()` factory
- `RunFingerprint()` computation
- Chunk/metadata artifact ID helpers
- `StockRunMetadata` DTO + metadata builder
- `buildChunkedStockManifest`
- `writeAndHashMetadata` I/O helper

**Target split** (5 files):

| New File | Content | Est. Lines |
|----------|---------|------------|
| `orchestrator_steps.go` | Step types + `Run()` methods only | ~500 |
| `step_runner.go` | `StepRunner` interface + `orchestratorRunner` + `runState` | ~150 |
| `orchestrator_fingerprint.go` | `RunFingerprint()` + `ChunkArtifactID` + `ChunkArtifactFilename` + `MetadataArtifactID` | ~90 |
| `orchestrator_metadata.go` | `StockRunMetadata` + `ChunkMetadataEntry` + `buildStockRunMetadata` + `writeAndHashMetadata` + `buildChunkedStockManifest` | ~200 |
| `orchestrator_defaults.go` | `DefaultStockSteps()` + compile-time assertions | ~25 |

**Additional**: Remove the `TODO(Commit-7)` transitional path in `StockPublishStep.Run` that silently skips chunks when compose_chunks is a stub (godlike/07 no-fake-availability). Once Commit 7 wires real renderer, this guard should be a fail-closed error.

**Deadline**: 2026-07-14

---

### P2: Eliminate `var rng` global state + deterministic fingerprinting

**Problem**: `service.go` line ~26:
```go
var rng = rand.New(rand.NewSource(time.Now().UnixNano()))
```
This package-level mutable global state is used in `processSingleVideo` for random clip offsets. The `RunFingerprint()` in `orchestrator_steps.go` already computes a deterministic SHA-256 from inputs — but the `rng` usage in clip extraction means **the same logical run produces different outputs**.

**Fix**: Replace `rng.Float64()` usage with deterministic pseudo-random derived from `RunFingerprint() + chunk_index + clip_index`. Use `hash/fnv` or the first 8 bytes of a SHA-256 as the seed for a local `rand.New(rand.NewSource(deterministicSeed))` per call.

**Deadline**: 2026-07-14

---

### P3: Fix 2 silent error swallows

**Problem**: Two instances of `_ =` that silently drop errors:

1. `orchestrator.go`: `_ = o.stepStore.MarkFailed(ctx, key, runErr.Error())` — if checkpoint persistence fails, the orchestrator continues as if the step was marked failed. On resume, the step could be re-executed even though it already failed.

2. `orchestrator_steps.go` (`writeAndHashMetadata`): `_ = f.Close()` — file close error dropped.

**Fix**:
1. Log the `MarkFailed` error at WARN level (non-fatal: the step error is already propagated to the caller)
2. Wrap the `Close()` error alongside the write error in `writeAndHashMetadata` (fail-closed: if Close() fails, the temp file's bytes may not be fully flushed)

**Deadline**: 2026-07-11

---

### P4: Feature envy: `query.go` direct `s.ytdlp.ListChannel`

**Problem**: `query.go` calls `s.ytdlp.ListChannel(ctx, channelURL, ...)` directly, bypassing the `acquisition.SourceStager` port that was introduced in Stock Cutover §12-4. The `ytdlp` field on `Service` is supposed to be retired — `StageSource` and `stageSection` already route through the port.

**Fix**: Extend `acquisition.SourceStager` with a `ListChannelVideos` method (or a separate `ChannelLister` port) and migrate `query.go` to use it. Remove the `ytdlp *downloader.YTDLPDownloader` field from `Service` entirely.

**Deadline**: 2026-07-18

---

### P5: Eliminate `json.RawMessage` and `map[string]any` primitive obsession

**Problem**: The stock pipeline heavily uses untyped containers:
- `HandleJob` returns `map[string]any` result map
- `ToJobPayload()` serializes to `map[string]any` 
- `json.RawMessage` appears in finalizer gate types
- `ChunkMetadataInput.Extra map[string]string`

**Fix**: 
1. Define a typed `StockJobResult` struct for `HandleJob` return (replacing `map[string]any`)
2. Use typed `StockRunPayload` directly in `Enqueue` instead of `map[string]any`
3. Keep `Extra map[string]string` — this is acceptable for user-provided metadata

**Deadline**: 2026-07-25

---

### P6: Remove 95 stub references — fake-availability cleanup

**Problem**: 95 lines mention "stub" across stockpipeline, with 12 in `orchestrator_steps.go` alone. `StockStageSourcesStep` and `StockComposeChunksStep` are pure stubs that log and return nil. This is godlike/07 fake-availability: the pipeline advertises 6 steps but only 4 do real work.

**Fix** (post Commit 6 + Commit 7):
- Commit 6: Wire `SourceStager.Prepare` loop in `StockStageSourcesStep` → remove stub marker
- Commit 7: Wire `StockRenderer.Render` loop in `StockComposeChunksStep` → remove stub marker
- Remove the pre-Commit-7 `ErrStockChunkLocalMissing` skip guard in `StockPublishStep`

**Deadline**: 2026-07-25 (aligned with Commit 6+7 delivery)

---

### P7: Reduce cross-package infra imports (I/O Binder)

**Problem**: `service.go` has 6 direct `internal/infrastructure` imports:
```go
"internal/infrastructure/ai/semantic"
"internal/infrastructure/database/assetindex"
"internal/infrastructure/database/sqlite/assets"
"internal/infrastructure/database/sqlite/outbox"
"internal/infrastructure/downloader"
"internal/infrastructure/indexing/clipindexer"
```

**Fix**: 
- `*semantic.MetadataWriter` is already behind a port — good
- `*clipindexer.Service` is already behind a port — good
- `*assetindex.Service` → narrow interface `stockAssetIndexUpserter` already exists
- `*assets.ClipsRepository` → narrow interface `stockClipsSearchTermUpdater` already exists
- `*outbox.Dispatcher` → narrow interface `stockChunkDispatcher` already exists
- `*downloader.YTDLPDownloader` → **MUST be removed** (P4 above)
- The narrow interfaces should move to `ports.go`, eliminating the infra imports from `service.go`

**Deadline**: 2026-07-25

---

### P8: Consolidate `HandleJob` 2-path divergence

**Problem**: `HandleJob` and `StockFinalizeStep.Run` both contain spine-write logic. `HandleJob` has an §F.1 fallback path when `s.finalizer == nil` that skips the spine write entirely (legacy return-map). `StockFinalizeStep.Run` has the canonical spine-write path via `JobFinalizer.CompleteWithArtifacts`. Two writers for the same fact = godlike/06 violation.

**Fix**: Once §F.2 follow-up wires the production `*finalizer.Finalizer` concrete at the composition root, collapse the `HandleJob` fallback path — `HandleJob` should delegate exclusively to `runOrchestratorResilient` → `StockFinalizeStep.Run` for the spine write.

**Deadline**: 2026-08-01

---

## Carry-Forward: Pre-existing Build Issues (NOT in scope)

These 6 items predate this action plan and block full-project `go build ./...`. They are NOT regressions from stock changes:

| ID | File | Issue | Deadline |
|----|------|-------|----------|
| FIX-MONITOR-ENQUEUE-TOLOWER | `monitor/enqueue.go` | `strings.ToLower` undefined | 2026-07-15 |
| FIX-MONITOR-SCHEDULER-ENQUEUER | `monitor/scheduler.go` | `NewUnboundJobEnqueuer` undefined | 2026-07-15 |
| FIX-STOCKPIPELINE-REDECLARATION | `run_upload.go` | File MISSING from disk | 2026-07-25 |
| FIX-APP-MODULE-MEDIA-DISPATCHER | `app/module_media.go` | clips.Deps.MutationsDispatcher literal | 2026-07-25 |
| FIX-IMAGES-ROUTING-CYCLE | `images/routing/` | Import cycle | 2026-08-01 |
| FIX-APP-WIRE-SCRIPT-SYNTAX | `app/workerruntime/` | Build error | 2026-08-01 |

---

## Execution Order (direct-on-main, per AGENTS.md Git-Lesson-2)

```
Week 1 (2026-07-11 deadline):
  ├── P0: Split service.go into 4 files
  ├── P3: Fix 2 silent error swallows
  └── P2: Eliminate var rng global state

Week 2 (2026-07-14 deadline):
  └── P1: Split orchestrator_steps.go into 5 files

Week 3 (2026-07-18 deadline):
  └── P4: Feature envy — query.go s.ytdlp.ListChannel → SourceStager port

Week 4-5 (2026-07-25 deadline):
  ├── P5: Eliminate primitive obsession (typed DTOs)
  ├── P6: Remove 95 stub references (post Commit 6+7)
  └── P7: Reduce cross-package infra imports

Week 6+ (2026-08-01 deadline):
  └── P8: Consolidate HandleJob 2-path divergence (§F.2)
```

---

## Success Criteria

Each action item is complete when:
1. `gofmt -d` returns empty on the modified files
2. `go vet ./internal/application/assets/providers/stock/...` exits 0
3. `go build ./internal/application/assets/providers/stock/...` exits 0
4. `go test -short ./internal/application/assets/providers/stock/...` exits 0
5. `rg <old-symbol>` returns 0 production-code hits (not comments, not tests)
6. AGENTS.md + CHANGELOG.md + architecture/current.yaml are updated in lockstep (godlike/06 3-surface)

---

## godlike/07 Honest Limitations

1. **Cross-package build issues**: 6 pre-existing items block `go build ./...` — per-file validation uses targeted subtree commands
2. **Commit 6 + Commit 7 dependencies**: P6 (stub removal) is gated on the real `SourceStager` and `StockRenderer` implementations landing first
3. **`run_upload.go`**: File listed in git hotspot analysis but MISSING from disk — retired in prior god-object decomposition wave; the `run_upload_indexing_test.go` is the surviving test file
4. **Narrow interfaces**: The `stockAssetIndexUpserter`, `stockClipsSearchTermUpdater`, `stockChunkDispatcher` narrow interfaces are already defined but not yet extracted to `ports.go` — P7 addresses this
