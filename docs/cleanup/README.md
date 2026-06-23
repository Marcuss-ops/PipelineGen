# PipelineGen Remaining Cleanup Roadmap

> Status: ACTIVE
>
> Verified baseline: `main` at `401e3847` on 2026-06-23.
>
> This roadmap records the state verified from code, not the intention stated in
> commit messages. A work package is marked complete only when its concrete exit
> checks pass.

## Current verified state

The latest cleanup round made meaningful progress:

- the Health HTTP handler no longer imports SQL, Drive, Qdrant or filesystem
  implementation code;
- application health orchestration now lives in
  `internal/application/system/health/`;
- concrete component probes now live in `internal/infrastructure/health/`;
- unhealthy deep health responses now return HTTP 503;
- the explicit health-file exception was removed from the API SQL/import gate;
- many ScriptFlow helpers, types, catalog jobs and curation jobs moved into
  `internal/application/scripts/`;
- lifecycle startup order is now Drive → Qdrant → Outbox → job runner;
- transition/effect zero semantics were corrected and transition registry tests
  were added;
- architecture and generated API documentation received a first truth refresh.

The cleanup is **not complete**. Script job execution is still owned by the API
handler, the health adapters duplicate existing clients/resources, composition
is still physically concentrated, and Wave 14 is marked complete even though
its thin-transport rule is not yet satisfied.

## Canonical direction

```text
internal/api/             binding, validation, HTTP mapping, response DTOs
internal/application/     use cases, orchestration, policies, application ports
internal/domain/          stable business contracts and values
internal/infrastructure/  SQL, filesystem, network and external-service adapters
internal/app/             construction, dependency injection, startup, shutdown
```

## Detailed plans

1. [`SCRIPT_ORCHESTRATION_MIGRATION.md`](./SCRIPT_ORCHESTRATION_MIGRATION.md)
   - Still the only blocker.
   - Removes job registration, semaphores, pipeline construction, prewarm
     goroutines, path dispatch and Drive traversal from `internal/api/script/`.

2. [`HEALTH_BOUNDARY_MIGRATION.md`](./HEALTH_BOUNDARY_MIGRATION.md)
   - Boundary relocation is complete.
   - Remaining work is hardening: reuse canonical clients/DB, typed errors,
     unknown-check HTTP 400, richer readiness and checker tests.

3. [`COMPOSITION_DOCS_AND_CONFIG_CLEANUP.md`](./COMPOSITION_DOCS_AND_CONFIG_CLEANUP.md)
   - Tracks the still-open physical composition split, migration truth,
     generated-doc verification and configuration example cleanup.

## Updated priorities

| Priority | Work package | Verified reason | Size |
|---|---|---|---|
| Blocker | Script job orchestration | `handler_jobs.go` still owns semaphore admission, service construction, goroutine prewarm, path dispatch and final result assembly. | Large |
| High | Wave 14 truth | Wave 14 says `done/verified_zero`, but its rule forbids API goroutine orchestration that still exists. | Small/Medium |
| Medium | Health hardening | Boundary is correct, but checkers reopen SQLite, parse token files, create duplicate HTTP clients and jobs health only checks a table. | Medium |
| Medium | Composition physical split | `composition.go` remains a mega-file and `wireScriptFlow` remains in `registry.go`. | Medium |
| Medium | API import hard gate | Check 19 is still soft-log and does not cover every infrastructure import, including `internal/infrastructure/files`. | Small/Medium |
| Small | Video config example | `transition_interval` exists in code but is still absent from `config.example.yaml`. | Small |

## Completed work packages

### Lifecycle ordering

Verified complete for ordering:

```text
Drive preparation -> Qdrant preparation -> Outbox start -> Job runner start
```

Remaining lifecycle enhancement: startup functions still return no error and
Qdrant preparation is explicitly best-effort, so required dependency failures
cannot currently stop startup.

### Transition behavior

Verified complete:

- `0` disables effects/transitions;
- negative values select defaults;
- modulo-by-zero guards are present;
- the 15-entry transition catalog has concrete infrastructure tests.

### Health transport boundary

Verified complete at the layer boundary:

```text
HTTP handler -> application health service -> infrastructure checker
```

This does not yet mean the health subsystem itself is finished; see the health
hardening document.

## Required execution order

1. Finish ScriptFlow orchestration extraction.
2. Downgrade/fix Wave 14 tracker truth and promote its exit gate from file-count
   only to transport-boundary verification.
3. Harden the health subsystem without moving infrastructure back into API.
4. Promote API forbidden-import checks to hard fail after remaining imports are
   removed.
5. Split composition and script wiring into focused same-package files.
6. Add the missing configuration example field and complete documentation
   consistency checks.

## Cross-cutting invariants

- Public HTTP routes, methods and payload shapes remain stable.
- Existing job type strings remain stable.
- API packages contain no job workflow, provider search policy, filesystem
  traversal, SQL, Drive SDK, Qdrant client or process execution.
- Application packages depend on ports rather than concrete infrastructure.
- Concrete adapters are constructed only in `internal/app`.
- No compatibility aliases, forwarding wrappers or duplicate implementations
  remain after the migration phase that required them.
- New behavior enters an existing registry, resolver or sampler rather than a
  parallel dispatch structure.
- Expected failures use typed application errors and are mapped separately by
  HTTP and job delivery adapters.
- Tracker state is derived from verified code, never from an operator override.

## Discovery commands

```bash
git fetch origin
git checkout main
git pull --ff-only origin main

rg 'internal/infrastructure' internal/api/script --type go
rg 'scriptGenSem|NewScenesService|NewDocumentsService|NewPipeline|go func' internal/api/script --type go
rg 'func \(h \*ScriptFlowHandler\) Handle.*Job' internal/api/script --type go
rg 'database/sql|os\.ReadFile|http\.Client' internal/infrastructure/health --type go
rg 'func Build[A-Za-z]+Bundle|func wireScriptFlow' internal/app --type go
rg 'CoreDeps|internal/module|internal/media|internal/sources' ARCHITECTURE.md README.md REPOSITORY_CLEANUP.md
rg '/api/health|qdrant/health|ollama-timeout' docs/api/ACTIVE_API_GENERATED.md
```

## Validation gate

```bash
gofmt -w <changed-go-files>
go test ./internal/application/system/health/...
go test ./internal/infrastructure/health/...
go test ./internal/application/scripts/...
go test ./internal/api/script/...
go test ./internal/app/...
go test ./internal/infrastructure/media/render/...
go vet ./...
go build ./...
yq eval '.' architecture/migration.yaml >/dev/null
bash scripts/ci-architectural-checks.sh
go run ./cmd/admin gen-api-docs docs/api/ACTIVE_API_GENERATED.md
git diff --exit-code -- docs/api/ACTIVE_API_GENERATED.md
git status -sb
git log -n 5 --oneline
```

No GitHub Actions run or combined status was exposed for `401e3847`; therefore
this roadmap does not claim that CI passed for that commit.

## Completion definition

Cleanup is complete only when:

- `internal/api/script/` contains transport and DTO conversion only;
- script job handlers and orchestration are application-owned;
- `flow.go` and `handler_jobs.go` forwarding/orchestration shells are removed;
- no production API file imports `internal/infrastructure/*`;
- Check 19 is hard-fail with a zero target;
- health checkers reuse canonical DB/Drive/Qdrant/job capabilities;
- unknown health checks return HTTP 400 and readiness policy is application-owned;
- `composition.go` and `registry.go` are split into focused same-package files;
- required startup failures are observable instead of silently best-effort;
- Wave 14 status and exit gate match the actual transport boundary;
- `config.example.yaml` documents `transition_interval`;
- architecture, migration and generated API documentation match current code;
- focused tests, full build, vet and architecture gates pass.
