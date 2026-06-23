# PipelineGen Remaining Cleanup Roadmap

> Status: ACTIVE
>
> Audit baseline: `main` at `6a953f3d` (2026-06-23).
>
> This directory is the operational implementation guide for the remaining
> architecture cleanup. The codebase is the source of truth: before starting a
> work package, re-run the listed discovery commands against the current
> `origin/main` and update this document when the real state differs.

## Purpose

The large namespace cleanup is mostly complete, but several responsibilities
still live in the wrong layer. The remaining work is not a directory-renaming
exercise. It must establish hard boundaries that prevent the same coupling from
returning.

Canonical direction:

```text
internal/api/             HTTP parsing, validation, status mapping, response DTOs
internal/application/     use cases, orchestration, policies, application ports
internal/domain/          stable business contracts and domain values
internal/infrastructure/  SQL, filesystem, network clients, Drive, Qdrant, process execution
internal/app/             construction, dependency injection, startup and shutdown
```

## Documents

1. [`HEALTH_BOUNDARY_MIGRATION.md`](./HEALTH_BOUNDARY_MIGRATION.md)
   - Removes SQL, token-file access, Drive HTTP, Qdrant HTTP and job-store
     inspection from `internal/api/common/health.go`.
   - Defines the application health service, checker ports, concrete adapters,
     HTTP semantics and composition wiring.

2. [`SCRIPT_ORCHESTRATION_MIGRATION.md`](./SCRIPT_ORCHESTRATION_MIGRATION.md)
   - Moves search, relevance filtering, translation, harvest decisions,
     post-generation processing and background job orchestration out of
     `internal/api/script/`.
   - Defines the application services, ports, migration sequence and route/job
     compatibility requirements.

3. [`COMPOSITION_DOCS_AND_CONFIG_CLEANUP.md`](./COMPOSITION_DOCS_AND_CONFIG_CLEANUP.md)
   - Physically splits the composition mega-files without changing package
     boundaries.
   - Corrects startup ordering, architecture documentation, migration tracking,
     generated API docs and `transition_interval` configuration examples.

## Current priorities

| Priority | Work package | Why it is still open | Size |
|---|---|---|---|
| Blocker | Health boundary | The API handler opens SQLite, reads OAuth token files and calls Drive/Qdrant directly. | Large |
| Blocker | Script orchestration | `flow.go` and `handler_jobs.go` still contain application workflow and concurrency control. | Large |
| Medium | Composition physical split | `composition.go` is still a mega-file and script wiring remains inside `registry.go`. | Medium |
| Medium | Repository truth | `ARCHITECTURE.md`, Wave 14 and generated API docs describe removed structures/routes. | Medium |
| Small | Video config example | `transition_interval` is implemented but absent from `config.example.yaml`. | Small |

## Required execution order

The order below is intentional. Do not combine all work into one large code
change.

### 1. Health boundary

Complete first because it creates an explicit pattern for system-level
capabilities:

```text
API handler -> application service -> application port -> infrastructure adapter
```

It also removes the broad exception currently allowing the health handler to
violate API import rules.

### 2. Script orchestration

Complete after health so the same port-and-adapter conventions can be reused.
Preserve every public route, job type and payload while moving implementation
ownership.

### 3. Lifecycle ordering and composition split

Once the application boundaries are correct, split `internal/app` physically.
Do not move wiring into feature packages and do not let application packages
construct infrastructure adapters.

### 4. Documentation and tracker repair

Update documentation only from the verified post-migration state. Generated API
docs must be regenerated from the actual router, not manually patched.

### 5. Configuration example and focused regressions

Add `transition_interval`, then lock transition/effect zero-value semantics with
tests.

## Cross-cutting invariants

Every work package must preserve these rules:

- Public HTTP paths, methods and payload shapes remain stable unless explicitly
  versioned.
- Existing job type strings remain stable.
- API packages do not import `database/sql`, SQLite drivers, Drive SDKs,
  Qdrant clients, process execution packages or filesystem adapters.
- Application packages do not import concrete infrastructure packages.
- Concrete adapters are built only under `internal/app`.
- No compatibility aliases, forwarding wrappers or duplicate implementations
  are left behind.
- New feature behavior enters an existing registry, resolver or sampler instead
  of creating a parallel dispatch path.
- Errors are typed at the application boundary and translated to HTTP or job
  status only at delivery boundaries.
- Startup work that can fail is not hidden inside constructors.
- A tracker wave may be marked `done` only after its exact exit gate passes.

## Common discovery commands

Run these before each work package and again before completion:

```bash
git fetch origin
git checkout main
git pull --ff-only origin main

rg 'database/sql|go-sqlite3|googleapis.com/drive|qdrant|os\.ReadFile' internal/api --type go
rg 'internal/infrastructure' internal/api/script --type go
rg 'func Build[A-Za-z]+Bundle|func wire[A-Za-z]+' internal/app --type go
rg 'CoreDeps|internal/module|internal/media|internal/sources' ARCHITECTURE.md README.md REPOSITORY_CLEANUP.md
rg '/api/health|qdrant/health|ollama-timeout' docs/api/ACTIVE_API_GENERATED.md
```

## Global validation gate

A work package is incomplete until the relevant focused tests and the repository
checks pass:

```bash
gofmt -w <changed-go-files>
go test ./internal/application/system/health/... ./internal/infrastructure/health/... ./internal/api/system/...
go test ./internal/application/scripts/... ./internal/api/script/...
go test ./internal/app/...
go vet ./...
go build ./...
bash scripts/ci-architectural-checks.sh
go run ./cmd/admin gen-api-docs docs/api/ACTIVE_API_GENERATED.md
git diff --exit-code -- docs/api/ACTIVE_API_GENERATED.md
git status -sb
git log -n 5 --oneline
```

Adjust the focused package list as files move, but do not omit `go vet`, full
build, architecture checks or generated-doc verification.

## Completion definition

The remaining cleanup is complete only when all of the following are true:

- `internal/api/common/health.go` is deleted or contains no component probing.
- No API package imports SQL, Drive, Qdrant or filesystem implementation code.
- `internal/api/script/` contains only HTTP transport and DTO mapping.
- Script job orchestration is owned by `internal/application/scripts/`.
- `composition.go` and `registry.go` are split into focused same-package files.
- Startup dependencies run in a deterministic order before workers consume jobs.
- `ARCHITECTURE.md`, `architecture/migration.yaml` and generated API docs match
  the current code.
- `config.example.yaml` documents `transition_interval`.
- Architecture gates enforce the cleaned boundaries without broad file-specific
  exceptions.
