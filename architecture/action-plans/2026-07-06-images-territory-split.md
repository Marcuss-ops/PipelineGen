# IMAGES-TERRITORY-SPLIT-2026-07-06 — Action Plan

## §0 — Context

The `internal/api/images/` and `internal/application/images/` packages have
grown "bridge" files that mix generated (AI), retrieved (stock), and legacy
concerns. The architectural separation already exists at the registry level
(`generated/` vs `retrieved/` subpackages), but the API and service layers
still have monolithic files that need capability-driven decomposition.

**Key files to split (with current line counts):**

| File | Lines | Problem |
|---|---|---|
| `internal/api/images/territory_handlers.go` | 479 | Mixes DTO, retrieved search, generated search, generated generate, styles, aggregator, limit parsing, error sentinel, mapper |
| `internal/api/images/impl.go` | 338 | Legacy gateway: routes, request types, upload, sync, generate, batch, animate, diagnostics |
| `internal/application/images/service.go` | 304 | Mixes deps, generated façade, retrieved façade, storage, diagnostics, job handling |
| `internal/application/images/retrieved/provider_registry.go` | 538 | Registry + 4 concrete providers in one file |
| `internal/application/images/generated/provider_registry.go` | 277 | Types, provider interface, GoogleSlidesProvider, registry, errors, constants |

## §1 — Priority Order (per user spec)

1. **P0 — `territory_handlers.go`** (479 LoC → ~7 files)
2. **P0 — `impl.go`** (338 LoC → ~10 files)
3. **P1 — `retrieved/provider_registry.go`** (538 LoC → ~7 files)
4. **P1 — `service.go`** (304 LoC → ~6 files)
5. **P2 — `generated/provider_registry.go`** (277 LoC → ~5 files)

## §2 — Golden Rule

```
generated = AI-created images
retrieved = found/downloaded/ingested images from normal sources
all       = aggregator only, never own business logic
legacy    = old endpoints kept for compat, never used as new architecture
```

## §3 — Per-PR Execution Plan

Each PR lands **directly on `main`** per AGENTS.md Git-Lesson-2 (NO branches, NO `--force`, NO PR).
Race-protect via `git fetch && git log --oneline HEAD..@{u}` before every push.
Co-authored-by trailer per Git-Lesson-3.

### Priority 0 — API Layer Split

#### PR-IMG-SPLIT-1: `territory_handlers.go` → 7 capability files

Split `internal/api/images/territory_handlers.go` (479 LoC) into:

```
internal/api/images/territory_router.go          — TerritorySearch switch only
internal/api/images/retrieved_search_handler.go  — RetrievedSearch + retrievedAggregate
internal/api/images/generated_search_handler.go  — GeneratedSearch + generatedAggregate + searchGeneratedTerritory + listGeneratedTerritoryResults + ErrInvalidGeneratedSearchLimit
internal/api/images/generated_generate_handler.go — GeneratedGenerate + GeneratedGenerateRequest
internal/api/images/generated_styles_handler.go  — GeneratedStyles + styleDefToInfo
internal/api/images/all_territories_handler.go   — allTerritoriesAggregate
internal/api/images/search_result_mapper.go      — assetToResult + previewURLForAsset
```

**Gates:** gofmt + go vet + go build + go test on `./internal/api/images/...` (targeted subtree only)

#### PR-IMG-SPLIT-2: `impl.go` → capability files

Split `internal/api/images/impl.go` (338 LoC) into:

```
internal/api/images/handler.go                   — ImagesHandler struct + NewImagesHandler + RegisterRoutes (thin route map only)
internal/api/images/request_types.go             — UploadRequest, GenerateImageRequest, GenerateBatchRequest, GenerateBatchItem, AnimateRequest, batchJobResponse
internal/api/images/upload_handler.go            — Upload (retrieved/legacy URL ingestion, NOT AI)
internal/api/images/sync_handler.go              — Sync
internal/api/images/legacy_generate_handler.go   — Generate (marked legacy compat)
internal/api/images/batch_generate_handler.go    — GenerateBatch + generateBatchID (AI async job system)
internal/api/images/diagnostics_handler.go       — Diagnostics
internal/api/images/animate_handler.go           — Animate (currently NotImplemented)
```

**Gates:** gofmt + go vet + go build + go test on `./internal/api/images/...`

### Priority 1 — Application Layer Split

#### PR-IMG-SPLIT-3: `retrieved/provider_registry.go` → 7 files

Split `internal/application/images/retrieved/provider_registry.go` (538 LoC) into:

```
internal/application/images/retrieved/provider.go           — RetrievalProvider interface
internal/application/images/retrieved/storage_bridge.go     — storage bridge helpers
internal/application/images/retrieved/provider_wikipedia.go — Wikipedia provider
internal/application/images/retrieved/provider_searxng.go   — SearXNG provider
internal/application/images/retrieved/provider_duckduckgo.go — DuckDuckGo provider
internal/application/images/retrieved/provider_drive.go     — Drive provider
internal/application/images/retrieved/provider_registry.go  — RetrievalProviderRegistry (SearchAll, SearchByName, Providers, Diagnostics, constructors)
```

**Gates:** gofmt + go vet + go build + go test on `./internal/application/images/retrieved/...`

#### PR-IMG-SPLIT-4: `service.go` → capability files

Split `internal/application/images/service.go` (304 LoC) into:

```
internal/application/images/deps.go                       — ImagesDeps, ImagesCoreDeps, ImagesStorageDeps, ImagesGenAIDeps, ImagesExternalDeps
internal/application/images/service.go                    — Service struct + NewService (thin constructor only)
internal/application/images/service_generated.go          — GenerateSmartImage, TriggerPrewarm, HandleJob, RegisterHandler
internal/application/images/service_retrieved.go          — SearchAndDownload, SearchWebImage
internal/application/images/service_generated_read.go     — ListImagesByOrigin
internal/application/images/service_storage.go            — IngestImage, UploadToStyleDrive, RegisterVideoAsset, SyncFromDrive, FormatDriveLink
internal/application/images/service_diagnostics.go        — DiagnosticsReport, Diagnostics, CapabilityResolution, AllCapabilities, Log, Repo, SyncAssets
```

**Gates:** gofmt + go vet + go build + go test on `./internal/application/images/...`

### Priority 2 — Generated Registry Split

#### PR-IMG-SPLIT-5: `generated/provider_registry.go` → 5 files

Split `internal/application/images/generated/provider_registry.go` (277 LoC) into:

```
internal/application/images/generated/types.go                     — request/result types
internal/application/images/generated/errors.go                    — sentinel errors
internal/application/images/generated/provider.go                  — GenerationProvider interface
internal/application/images/generated/provider_google_slides.go    — GoogleSlidesProvider
internal/application/images/generated/provider_registry.go         — GenerationProviderRegistry (constructors, SearchByName, Providers, Diagnostics)
```

**Gates:** gofmt + go vet + go build + go test on `./internal/application/images/generated/...`

## §4 — Wave-Tracker Entry

Canonical anchor: `architecture/current.yaml#IMAGES-TERRITORY-SPLIT-2026-07-06`
5 slim-shape `linked_issues`: PR-IMG-SPLIT-1 through PR-IMG-SPLIT-5
Deadline: 2026-08-15
Owner: `internal/api/images + internal/application/images`

## §5 — Cross-References

- `architecture/current.yaml#GODOBJ-2026-07-03` — God-object decomposition precedent (same per-file split pattern)
- `architecture/current.yaml#CLEANUP-PRIORITY-1-5-2026-07-25` — Sister cleanup wave
- AGENTS.md Pattern 5 (split packages by capability) + Pattern 0 (port abstraction)
- AGENTS.md Git-Lesson-2/3/4/5 (direct-to-main + Co-authored-by + race-protect + byte-equivalent-replay)

## §6 — Honest Scope-Lock (godlike/07)

- This wave is a **mechanical capability-driven split** — zero behavior change, zero new types, zero new tests beyond the existing suite.
- Pre-existing build issues from `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` carry forward unchanged.
- The user's note about stale documentation in `territory_handlers.go` (old forward-pointer comment saying generated search returns empty) is fixed within PR-IMG-SPLIT-1.
- The `internal/api/images/compose_test.go` and `capability_test.go` files may need test import adjustments — handled per-PR.
