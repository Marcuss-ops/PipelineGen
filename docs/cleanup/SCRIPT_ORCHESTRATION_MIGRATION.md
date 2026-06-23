# Script Orchestration Migration

> Status: PARTIAL — BLOCKER STILL OPEN
>
> Verified against `main` at `401e3847` on 2026-06-23.
>
> Target owner: `internal/application/scripts/`.

## Audit verdict

The latest extraction moved a substantial amount of code into the application
layer, but it did **not** complete the thin-transport migration.

Completed movement includes:

```text
internal/application/scripts/
    flow_helpers.go
    flow_types.go
    insight_builder.go
    job_helpers.go
    catalog_job.go
    curation_job.go
    clip_services.go
```

This is useful progress. Search helpers, shared types, insight building and some
job implementations now have an application owner.

However, the two original API files remain production owners of orchestration:

```text
internal/api/script/flow.go
internal/api/script/handler_jobs.go
```

Wave 14 therefore cannot truthfully claim that the API layer contains only
transport.

## Current violations verified in code

### `internal/api/script/flow.go`

The file is still approximately 200 lines and contains:

- type aliases back into `internal/application/scripts`;
- forwarding functions for search, Artlist, entities and insights;
- a wrapper object around `scripts.ScriptInsightBuilder`;
- direct import of `internal/infrastructure/files`;
- Google Drive folder-name/path interpretation;
- recursive Drive SDK traversal through `driveUploader.Service.Files.List()`;
- folder creation decisions through `GetOrCreateFolder`.

The file comment explicitly describes these as “back-compat” aliases/wrappers.
That contradicts the cleanup rule forbidding re-export packages and forwarding
wrappers after migration.

### `internal/api/script/handler_jobs.go`

The file remains a large application workflow host. It still owns:

- job registration on `job.Service`;
- batch-job payload decoding and execution;
- script-generation semaphore admission and release;
- generation payload decoding;
- per-job construction of `ScenesService`;
- per-job construction of `DocumentsService`;
- per-job construction of `Pipeline`;
- a raw goroutine for image-service prewarm;
- explicit/auto-search/text-only path dispatch;
- clip context and fingerprint generation;
- engine calls;
- progress staging and telemetry;
- post-generation pipeline invocation;
- final result conversion and assembly;
- receiver methods for the three generation paths.

This is business orchestration, not delivery transport.

### Composition remains coupled to the API handler

`internal/app/registry.go::wireScriptFlow` still constructs a
`scriptapi.ScriptFlowHandler` with a large dependency bundle and relies on the
handler to register/execute script jobs.

The desired direction is:

```text
internal/app
    constructs application script services + job handlers

internal/application/scripts
    owns job orchestration and generation workflow

internal/api/script
    owns HTTP binding + use-case invocation only
```

## Compatibility constraints

The migration must preserve:

- all routes under `/api/script/*`;
- job type strings:
  - `script.generate_batch`;
  - `script.generate_from_clips`;
  - `script.generate_from_catalog` where enabled;
  - `script.curate` where enabled;
- existing job payload JSON;
- existing result JSON;
- explicit clip, auto-search and text-only generation paths;
- Google Doc, scene-image and voiceover behavior;
- progress and cancellation behavior;
- feature flags and deployment-mode behavior.

Do not combine this work with a route rename from `api/script` to `api/scripts`.
Ownership must be fixed first.

## Target structure

Use existing `internal/application/scripts/` as the single application owner.
Avoid creating another parallel script-flow package.

```text
internal/application/scripts/
    orchestration_service.go
    orchestration_paths.go
    orchestration_post_generation.go
    orchestration_result.go
    job_handlers.go
    job_registration.go
    folder_resolver.go
    prewarm.go
    *_test.go

internal/api/script/
    handler.go
    handler_batch.go
    handler_flow.go
    handler_flow_ops.go
    types.go
    helpers.go                 # transport-only helpers if genuinely needed

internal/app/
    wire_script.go
```

Subpackages such as `scripts/orchestration` or `scripts/jobs` are acceptable if
they improve cohesion, but do not duplicate existing engines, memory services,
scene services, document services or DTOs.

## Required application ports

### Folder resolution

Drive path interpretation and traversal must leave the API package.

```go
type FolderResolver interface {
    Resolve(ctx context.Context, input, defaultRootID string) (string, error)
}
```

Implementation options:

- application policy + infrastructure Drive adapter; or
- a narrow existing Drive resolver extended through its canonical registry.

The API handler must not inspect Drive IDs, clean folder names, traverse SDK
results or create folders.

### Prewarm

```go
type Prewarmer interface {
    Prewarm(ctx context.Context, jobID string, pages int) error
}
```

The orchestration service decides whether prewarm is needed. The concrete
sidecar/image-service call remains an injected adapter. Use the repository’s
canonical concurrency helper instead of a raw API-layer goroutine.

### Progress

```go
type ProgressSink interface {
    Report(percent int, message string)
}
```

Adapt `appjobs.JobTools` in the job delivery adapter. Application orchestration
should not depend directly on a transport/job-tools concrete type when a narrow
port is sufficient.

### Generation capacity

The semaphore belongs to a long-lived application service:

```go
type GenerationLimits struct {
    MaxConcurrent int
}
```

Construct it once in `internal/app`, acquire with context, and release by defer.
The capacity controller must not live on `ScriptFlowHandler`.

## Target orchestration API

```go
type GenerateCommand struct {
    JobID    string
    Spec     script.GenerationSpec
    Progress ProgressSink
}

type GenerateResult struct {
    Values map[string]any
}

type OrchestrationService struct {
    capacity       CapacityGate
    paths          PathExecutor
    scenes         SceneService
    documents      DocumentService
    postGeneration PostGenerationService
    prewarmer      Prewarmer
    folders        FolderResolver
    log            *zap.Logger
}

func (s *OrchestrationService) Generate(
    ctx context.Context,
    cmd GenerateCommand,
) (GenerateResult, error)
```

The application service owns:

- admission control;
- path selection;
- prewarm policy;
- pipeline execution;
- stage telemetry;
- post-generation sequencing;
- result assembly.

## Target job delivery adapter

Application-owned job handlers may decode the job payload and adapt progress,
but they must immediately call the use case:

```go
type JobHandler struct {
    generate *OrchestrationService
    batch    BatchExecutor
}

func (h *JobHandler) HandleGenerateFromClips(
    ctx context.Context,
    j *job.Job,
    tools *appjobs.JobTools,
) (map[string]any, error)
```

Job registration should happen from `internal/app/wire_script.go`, using these
application handlers. HTTP handlers must not double as job workers.

## Required migration sequence

### Phase 1 — move Drive folder resolution

Move `resolveDriveFolderID` and `findFolderByNameDeep` out of `flow.go`.

Exit checks:

```bash
! rg 'internal/infrastructure/files|driveUploader\.Service\.Files|GetOrCreateFolder' internal/api/script --type go
```

### Phase 2 — remove aliases and forwarding functions

Update all internal callers to import canonical application types/functions
directly. Then delete:

- `assetSearchTarget` alias;
- suggestion aliases;
- `EntityScriptExtractor` alias;
- `SearchScriptAssets` forwarding function;
- `SearchArtlistClips` forwarding function;
- phrase/intro forwarding functions;
- entity/insight forwarding functions;
- wrapper `ScriptInsightBuilder`.

No forwarding wrapper should remain solely for “zero churn”. Migration churn is
expected and should be completed in the same focused change.

### Phase 3 — construct stable services once

Move creation of:

- `ScenesService`;
- `DocumentsService`;
- `Pipeline` or its replacement;
- capacity semaphore;
- prewarm adapter;
- folder resolver

into `internal/app/wire_script.go`.

Do not construct stable services for every job invocation.

### Phase 4 — extract generation orchestration

Move from `handler_jobs.go`:

- capacity acquire/release;
- dispatch switch;
- three path methods;
- pipeline execution;
- telemetry stages;
- result assembly.

Add application tests for all paths before deleting the API implementation.

### Phase 5 — move job registration

Replace:

```go
ScriptFlowHandler.RegisterJobHandlers(...)
```

with application job handler registration from composition.

The route handler and job handler may share use cases, but they must be separate
delivery adapters.

### Phase 6 — delete migrated API files/shells

Target outcome:

```bash
! test -f internal/api/script/flow.go
! test -f internal/api/script/handler_jobs.go
```

If filenames remain for route organization, their contents must be strictly
transport-only and the old aliases/receiver job methods must be gone.

### Phase 7 — hard architecture gate

Check 19 is currently soft-log and its forbidden-import expression does not
cover every infrastructure package. Promote it to hard fail after cleanup and
include generic application-specific infrastructure imports.

At minimum, enforce:

```bash
! rg 'github.com/Marcuss-ops/PipelineGen/internal/infrastructure/' internal/api --type go
! rg 'scriptGenSem|NewScenesService|NewDocumentsService|NewPipeline|go func' internal/api/script --type go
! rg 'func \(h \*ScriptFlowHandler\) Handle.*Job' internal/api/script --type go
```

Explicit middleware/bootstrap exceptions should be narrow and documented.

## Characterization tests required

Before deleting the current workflow, cover:

- explicit `clip_ids` path;
- auto-search `num_clips` path;
- text-only path;
- missing clip source builder;
- missing media curator;
- context cancellation while waiting for capacity;
- semaphore release on every failure;
- prewarm gating and timeout;
- scene/document/pipeline construction dependencies;
- entity, metadata, scene, voiceover and document phases;
- progress milestones;
- final result fields and compatibility;
- cache/fingerprint behavior;
- Drive folder ID, name and nested-path resolution;
- batch generation job behavior;
- catalog/curation registration when enabled.

## Error policy

Introduce typed expected errors, for example:

```go
var (
    ErrClipPipelineUnavailable = errors.New("clip pipeline unavailable")
    ErrAutoSearchUnavailable   = errors.New("auto-search pipeline unavailable")
    ErrGenerationBusy          = errors.New("script generation capacity unavailable")
    ErrFolderResolution        = errors.New("script folder resolution failed")
)
```

Application code returns these errors. HTTP and job adapters map them
independently.

## Validation

```bash
go test ./internal/application/scripts/...
go test ./internal/api/script/...
go test ./internal/app/...
go vet ./internal/application/scripts/... ./internal/api/script/... ./internal/app/...
go build ./...
bash scripts/ci-architectural-checks.sh

! rg 'internal/infrastructure' internal/api/script --type go
! rg 'scriptGenSem|NewScenesService|NewDocumentsService|NewPipeline|go func' internal/api/script --type go
! rg 'func \(h \*ScriptFlowHandler\) Handle.*Job' internal/api/script --type go
! rg '= scripts\.' internal/api/script/flow.go internal/api/script/handler_jobs.go 2>/dev/null
```

## Definition of done

This blocker is closed only when:

- `internal/api/script/` contains HTTP binding, validation and DTO mapping only;
- no API script file imports `internal/infrastructure/*`;
- no API script file contains Drive traversal or folder creation policy;
- script capacity, prewarm, path dispatch and pipeline execution are
  application-owned;
- stable services are built once in composition;
- job handlers are application-owned delivery adapters;
- HTTP handlers are not registered as background job workers;
- forwarding aliases/wrappers are removed;
- public routes, job types, payloads and result shapes remain compatible;
- focused tests, full build, vet and hard architecture gates pass.
