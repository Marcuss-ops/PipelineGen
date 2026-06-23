# Script Orchestration Migration

> Priority: BLOCKER
>
> Current problem files:
>
> - `internal/api/script/flow.go`
> - `internal/api/script/handler_jobs.go`
>
> Target owner: `internal/application/scripts/`

## Problem statement

The script API package still owns application workflow. File compaction reduced
file count, but it did not establish a transport boundary.

Current API-layer responsibilities include:

- multi-provider asset search;
- topic keyword extraction and relevance filtering;
- result de-duplication and limiting;
- automatic harvest enqueue decisions;
- Artlist phrase translation and folder resolution;
- entity/image and insight post-processing;
- script job registration and execution;
- concurrency semaphore ownership;
- construction of scenes, documents and pipeline services;
- path dispatch between explicit clips, auto-search and text-only generation;
- raw goroutine prewarming.

These responsibilities make HTTP transport difficult to test, encourage direct
infrastructure imports, and let job execution depend on a handler object.

## Compatibility constraints

The migration must preserve:

- route paths and methods under `/api/script/*`;
- job type strings, including:
  - `script.generate_batch`;
  - `script.generate_from_clips`;
  - `script.generate_from_catalog` where supported;
  - `script.curate` where supported;
- existing job payload JSON;
- existing result JSON and Google Doc output behavior;
- three generation paths:
  - explicit `clip_ids`;
  - auto-search using `num_clips`;
  - text-only fallback;
- progress reporting and cancellation propagation;
- existing feature flags and deployment-mode behavior.

Do not combine this migration with route renaming from `api/script` to
`api/scripts`. First fix ownership. A path/package rename can be performed later
as a separate cleanup after all behavior is characterized.

## Target package layout

Recommended structure:

```text
internal/application/scripts/
    orchestration/
        service.go
        request.go
        result.go
        paths.go
        post_generation.go
        prewarm.go
        service_test.go

    assets/
        service.go
        ports.go
        relevance.go
        artlist.go
        suggestions.go
        service_test.go

    jobs/
        registrar.go
        generate_batch.go
        generate_from_clips.go
        generate_catalog.go
        curate.go
        stage_logger.go
        handlers_test.go

    documents/              existing or canonical document service
    scenes/                 existing or canonical scene service
    gemmamemory/            existing package retained

internal/api/script/
    handler.go
    routes.go
    dto.go
    handler_test.go

internal/app/
    wire_script.go
```

Exact subpackage names may be adjusted to existing package conventions, but
responsibility boundaries must remain the same. Do not create duplicate script
engines, memory services, scene services or document services.

## Application ownership map

### Asset discovery service

Move from `flow.go`:

```text
SearchScriptAssets
filterSearchAssets
topicRelevant
SearchArtlistClips
artlistSearchPhrase
resolveArtlistFolderForPhrase
BuildPhraseClipSuggestions
SearchIntroClips
query-building helpers
```

Target: `internal/application/scripts/assets/`.

The service should own search policy, not infrastructure access.

Suggested API:

```go
type SearchRequest struct {
    Queries []string
    Targets []Target
    Limit   int
}

type SearchResult struct {
    Suggestions []Suggestion
    Harvested   []string
}

type Service struct {
    search     SearchPort
    translator TranslatorPort
    folders    FolderResolverPort
    harvest    HarvestPort
    log        *zap.Logger
}

func (s *Service) Search(ctx context.Context, req SearchRequest) (SearchResult, error)
func (s *Service) SearchArtlist(ctx context.Context, req ArtlistRequest) ([]ArtlistSuggestion, error)
```

### Script orchestration service

Move path selection and post-generation workflow from `handler_jobs.go` into
`internal/application/scripts/orchestration/`.

Suggested entry point:

```go
type GenerateCommand struct {
    JobID    string
    Payload  script.GeneratePayload
    Progress ProgressSink
}

type GenerateResult struct {
    Values map[string]any
}

func (s *Service) Generate(ctx context.Context, cmd GenerateCommand) (GenerateResult, error)
```

The service owns:

- concurrency admission;
- explicit/auto-search/text path selection;
- pipeline construction or invocation;
- post-generation fan-out;
- scene and document coordination;
- prewarm decisions;
- stage logging;
- final result assembly.

The API handler must never choose a generation path or construct a pipeline.

### Job handlers

Job-system adapters belong under `internal/application/scripts/jobs/`, not the
HTTP package. They translate a domain job into an application command.

Suggested interfaces:

```go
type GenerateService interface {
    Generate(context.Context, orchestration.GenerateCommand) (orchestration.GenerateResult, error)
}

type BatchService interface {
    Execute(context.Context, *scripts.GenerateBatchRequest, func(int, string)) (*scripts.GenerateBatchResponse, error)
}
```

Suggested handlers:

```go
func (h *Handler) HandleGenerateFromClips(
    ctx context.Context,
    j *job.Job,
    tools *appjobs.JobTools,
) (map[string]any, error)

func (h *Handler) HandleGenerateBatch(
    ctx context.Context,
    j *job.Job,
    tools *appjobs.JobTools,
) (map[string]any, error)
```

The job handler may decode payloads, adapt progress and call the use case. It
must not construct scenes/documents/pipelines on every invocation.

## Required application ports

No code under `internal/application/scripts/` may import concrete Drive,
filesystem, Qdrant, HTTP or process packages.

Define ports near their consumer.

### Asset search

```go
type SearchPort interface {
    SearchClips(
        ctx context.Context,
        query string,
        source string,
        mediaType string,
        limit int,
        threshold float64,
    ) ([]Match, error)
}
```

Use an application-neutral `Match`; do not leak Qdrant DTOs.

### Translation

```go
type TranslatorPort interface {
    Translate(ctx context.Context, text, language string) (string, error)
}
```

Wrap the current generator/translation implementation in `internal/app` or an
infrastructure adapter.

### Folder resolution

```go
type FolderResolverPort interface {
    ResolveForPhrase(ctx context.Context, phrase string) (Folder, error)
    ResolveRecommended(ctx context.Context, request FolderRequest) (Folder, error)
}
```

The application package may use folder IDs and links as neutral values, but it
must not import the Drive SDK or `internal/infrastructure/drive`.

### Harvest

```go
type HarvestPort interface {
    EnqueueHarvest(ctx context.Context, query string, count int, preset string) error
}
```

Do not silently discard errors. The service may treat harvest as best-effort,
but it must log/return structured warnings in a defined way.

### Prewarm

```go
type Prewarmer interface {
    Prewarm(ctx context.Context, jobID string, pages int) error
}
```

The application service decides whether prewarm is useful. The concrete
sidecar/network implementation stays outside the application package.

### Progress

```go
type ProgressSink interface {
    Report(percent int, message string)
}
```

Provide a no-op implementation rather than repeated nil checks if that matches
existing conventions.

## Relevance and de-duplication policy

`topicRelevant` is business policy and should be independently testable.

Move it to `assets/relevance.go` and define table-driven tests covering:

- empty topic accepts results;
- stop-word-only topic;
- exact token matches;
- prefix/stem behavior currently based on first three characters;
- tokens shorter than four characters;
- duplicate IDs;
- source exceptions such as Artlist;
- limit enforcement;
- deterministic ordering.

Do not silently replace the heuristic with embeddings during this migration.
Preserve behavior first; improve the algorithm later through a separate change.

## Auto-harvest policy

Auto-harvest should become explicit policy rather than a hidden side effect of a
search helper.

Recommended rules:

1. Search every normalized query/target pair.
2. Apply relevance, de-duplication and limit policy.
3. Only when the final result is empty, evaluate harvest eligibility.
4. De-duplicate harvest queries.
5. Enqueue through `HarvestPort`.
6. Record what was enqueued in `SearchResult.Harvested`.
7. Propagate context cancellation immediately.
8. Decide and document whether individual enqueue failures fail the request or
   return partial success with warnings.

Recommended default: partial success with structured warnings, unless the
caller explicitly requires harvest completion.

## Concurrency ownership

The script-generation semaphore currently lives on `ScriptFlowHandler`. Move it
to the application orchestration service.

Suggested construction:

```go
type Limits struct {
    MaxConcurrentGenerations int
}

func NewService(deps Deps, limits Limits) (*Service, error)
```

The semaphore must:

- be allocated once during composition;
- use context-aware acquisition;
- release through `defer` immediately after acquisition;
- expose queue/acquire/release telemetry;
- never be tied to an HTTP handler lifetime.

## Pipeline construction

The current job handler constructs `ScenesService`, `DocumentsService` and
`Pipeline` for each job. Replace this with one of the following, preferring the
first:

### Preferred: prebuilt orchestration service

Construct all stable collaborators once in `internal/app/wire_script.go` and
inject them into `orchestration.Service`.

```go
type Deps struct {
    Paths         PathExecutor
    Scenes        SceneGenerator
    Documents     DocumentWriter
    PostProcessor PostProcessor
    Prewarmer     Prewarmer
    FolderResolver FolderResolverPort
    Log           *zap.Logger
}
```

### Acceptable: application factory

If some collaborators genuinely depend on each job payload, inject a factory
interface into the orchestration service. The factory implementation is built
in `internal/app`; the API handler still does not construct services.

Do not use the HTTP handler as a dependency container.

## HTTP transport after migration

`internal/api/script/` should contain only:

- Gin binding and validation;
- request/response DTO conversion;
- calling application use cases;
- mapping typed errors to HTTP responses;
- route registration.

It must not contain:

- job execution methods;
- search heuristics;
- provider loops;
- translation;
- Drive folder traversal;
- scene/document service construction;
- semaphores;
- raw goroutines;
- infrastructure imports.

A useful static target:

```bash
! rg 'internal/infrastructure|database/sql|\bgo func\b|NewPipeline|NewScenesService|NewDocumentsService' internal/api/script --type go
```

## Composition wiring

Create:

```text
internal/app/wire_script.go
```

Move `wireScriptFlow` out of `registry.go` into this file while retaining the
same `package app`.

The wiring sequence should be:

1. Reuse canonical `AIBundle.ScriptEngine` and `MemoryService`.
2. Construct repository adapters once.
3. Construct asset discovery service from ports/adapters.
4. Construct scene/document/post-generation services once.
5. Construct orchestration service with its semaphore/limits.
6. Construct application job handlers.
7. Register job handlers with the canonical jobs service.
8. Construct thin HTTP handler/use cases.
9. Register the route module.

Avoid a dependency struct with 20+ unrelated fields. Use focused bundles:

```go
type ScriptSearchDeps struct { ... }
type ScriptGenerationDeps struct { ... }
type ScriptDeliveryDeps struct { ... }
```

Each bundle should remain cohesive and preferably contain no more than ten
fields.

## Migration sequence

### Phase 0: behavior characterization

Before moving code, add tests for:

- explicit clip path;
- auto-search path;
- text-only path;
- missing clip source builder returns explicit error;
- context cancellation while waiting for the generation semaphore;
- post-generation entity/image/voiceover/doc phases;
- progress adaptation;
- auto-harvest only when final search results are empty;
- duplicate and relevance behavior;
- Artlist translation fallback;
- prewarm gating.

These tests should target extracted application behavior. Where the current code
is hard to instantiate, add narrow characterization tests around pure helpers
first.

### Phase 1: move pure models and heuristics

Move suggestion DTOs, target values, relevance, de-duplication and query helpers
into `internal/application/scripts/assets/`. Update imports and tests in the same
change. Do not leave forwarding functions in the API package.

### Phase 2: define ports and move search orchestration

Introduce search/translation/folder/harvest ports and move
`SearchScriptAssets`, Artlist search and related helpers. Build adapters in
`internal/app` from existing services.

### Phase 3: extract post-generation service

Move entity extraction, insight building, metadata conversion, scene/voiceover
coordination and document assembly into application services. Preserve current
parallel/sequential dependency graph.

### Phase 4: extract job orchestration

Move semaphore, payload dispatch, pipeline invocation, stage logs and result
assembly to `orchestration.Service` and `scripts/jobs` handlers.

### Phase 5: update job registration

Register the new application job handlers from `wire_script.go`. Remove
`ScriptFlowHandler.RegisterJobHandlers` when no longer used.

### Phase 6: shrink API package

Delete `flow.go` and `handler_jobs.go` after all callers use application
services. Keep only transport files. Do not leave deprecated aliases.

### Phase 7: harden architecture gate

Add or strengthen checks so `internal/api/script` cannot import infrastructure,
construct application services or contain raw job handler methods.

## Error policy

Create typed application errors for expected conditions, for example:

```go
var (
    ErrClipPipelineUnavailable = errors.New("clip pipeline unavailable")
    ErrSearchUnavailable       = errors.New("script asset search unavailable")
    ErrDocumentUnavailable     = errors.New("document writer unavailable")
    ErrGenerationBusy          = errors.New("script generation capacity exhausted")
)
```

HTTP and job adapters translate these errors independently:

- API may return 400/409/503 as appropriate.
- Job handler returns an error for retry/failure policy.
- Application services never return Gin responses or job-specific JSON errors.

## Tests and validation

Focused commands:

```bash
go test ./internal/application/scripts/...
go test ./internal/api/script/...
go test ./internal/app/...
go vet ./internal/application/scripts/... ./internal/api/script/... ./internal/app/...
go build ./...
bash scripts/ci-architectural-checks.sh
```

Static exit checks:

```bash
! test -f internal/api/script/flow.go
! test -f internal/api/script/handler_jobs.go
! rg 'internal/infrastructure|database/sql' internal/api/script --type go
! rg 'func \(h \*ScriptFlowHandler\) Handle.*Job' internal/api/script --type go
! rg 'NewScenesService|NewDocumentsService|NewPipeline|scriptGenSem' internal/api/script --type go
rg 'script.generate_batch|script.generate_from_clips' internal/application/scripts internal/app --type go
```

## Definition of done

The migration is complete when:

- asset search, relevance, de-duplication, translation and harvest policy live
  under `internal/application/scripts/`;
- generation path selection, concurrency control, post-generation processing
  and job execution live under `internal/application/scripts/`;
- `internal/api/script/` contains transport only;
- API code has no infrastructure imports, semaphores, raw goroutines or service
  construction;
- application code depends only on application/domain contracts and ports;
- concrete adapters are constructed in `internal/app/wire_script.go`;
- all existing routes, job types, payloads and result shapes remain compatible;
- no forwarding wrappers or duplicate implementations remain;
- focused tests, vet, full build and architecture checks pass.
