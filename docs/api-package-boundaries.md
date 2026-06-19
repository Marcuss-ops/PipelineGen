# API Package Boundaries

## Problem

`internal/api/` grew to **153 production Go files** in a single flat package
after the June 2026 god-object split. Splitting large files solved file-size
problems but created a **mega-package** — a namespace without neighbourhoods.

This document defines the target structure, dependency rules, and size limits
that will guide the verticalisation PRs.

## Principle

```
internal/api must not contain feature implementations.
It must contain only server, router, middleware, and shared HTTP contracts.
```

Every feature API must be a **thin transport layer**:

```
HTTP request  →  command object
command       →  use-case interface
use-case result →  HTTP response
```

No business orchestration, database access, Drive SDK calls, FFmpeg
invocations, or prompt composition in the API package.

## Target structure

```
internal/api/
├── server.go              # http.Server lifecycle
├── router.go              # gin.Engine factory
├── routes.go              # top-level route registration
├── response.go            # shared response helpers
├── errors.go              # shared error types and codes
│
├── middleware/             # gin middleware (auth, logging, ratelimit, …)
│   ├── auth.go
│   ├── request_id.go
│   ├── rate_limit.go
│   ├── recovery.go
│   └── logging.go
│
├── script/                # Script generation transport
│   ├── handler.go         # single Handler struct with use-case deps
│   ├── routes.go          # RegisterRoutes(*gin.RouterGroup)
│   ├── requests.go        # request DTOs
│   ├── responses.go       # response DTOs
│   └── adapters.go        # DTO ↔ command converters
│
├── sources/               # Media source management transport
│   ├── handler.go
│   ├── routes.go
│   ├── requests.go
│   ├── responses.go
│   └── adapters.go
│
├── assets/                # Asset management transport
├── jobs/                  # Job management transport
├── images/                # Image management transport
├── books/                 # Book processing transport
├── lessons/               # Lesson generation transport
├── realtime/              # Real-time matching transport
├── workers/               # Internal worker routes
├── admin/                 # Admin-only transport
└── system/                # Health, doctor, diagnostics
```

## Dependency rules

### Allowed imports (api/**)

| Package | Allowed? | Notes |
|---------|----------|-------|
| `github.com/gin-gonic/gin` | ✅ | HTTP framework |
| `net/http` | ✅ | Standard HTTP |
| `encoding/json` | ✅ | JSON marshalling |
| `context` | ✅ | Request contexts |
| `github.com/Marcuss-ops/PipelineGen/internal/core/` | ✅ | Canonical contracts/interfaces only |
| `github.com/Marcuss-ops/PipelineGen/internal/application/` | ✅ | Use-case interfaces (new, thin) |
| `go.uber.org/zap` | ✅ | Logging |

### Forbidden imports (api/**)

| Package | Status | Replacement |
|---------|--------|-------------|
| `database/sql` | 🚫 | Pass through use-case interface |
| `github.com/Marcuss-ops/PipelineGen/internal/repository/` | 🚫 | Pass through use-case interface |
| `google.golang.org/api/drive/v3` | 🚫 | Pass through use-case interface |
| `github.com/Marcuss-ops/PipelineGen/internal/platform/ffmpeg` | 🚫 | Pass through use-case interface |
| `os/exec` | 🚫 | Pass through use-case interface |
| `github.com/Marcuss-ops/PipelineGen/internal/media/` | 🚫 | Pass through use-case interface |
| `github.com/Marcuss-ops/PipelineGen/internal/sources/` | 🚫 | Pass through use-case interface |
| `github.com/Marcuss-ops/PipelineGen/internal/upload/` | 🚫 | Pass through use-case interface |

### Feature API rules

1. **One Handler struct per feature.** Constructor: `NewHandler(deps Dependencies) *Handler`
2. **One `RegisterRoutes(*gin.RouterGroup)` per feature.** No other route registration.
3. **Feature APIs must not import each other.** Script ↔ Sources cross-cutting goes
   through `internal/app/registry.go` composition.
4. **No concrete service/repository construction** inside an API package. All
   dependencies are interfaces injected at construction time.

## Handler shape

Every HTTP handler method must follow this template:

```go
func (h *Handler) GenerateFromClips(c *gin.Context) {
    req, ok := apiutil.BindJSON[GenerateFromClipsRequest](c)
    if !ok {
        return
    }

    result, err := h.generateFromClips.Execute(
        c.Request.Context(),
        toCommand(req),
    )
    if err != nil {
        apiutil.HandleError(c, err)
        return
    }

    apiutil.OK(c, result)
}
```

Rules:
- Max ~15 lines per handler method
- No scene construction, clip manipulation, Drive calls, prompt composition
- No `context.Background()` — always use `c.Request.Context()`
- Error handling via `apiutil.HandleError(c, err)`

## Application layer target

Business orchestration moves to `internal/application/`:

```
internal/application/scriptflow/
├── generate/
│   ├── service.go         # GenerateFromClips, GenerateWithImages, GenerateBatch
│   ├── planning.go        # scene planning, outline
│   └── validation.go      # request validation rules
├── curate/
│   ├── service.go         # query → clip compilation
│   ├── selection.go       # clip selection strategy
│   └── ranking.go         # clip ranking/scoring
├── scenes/
│   ├── builder.go         # scene construction
│   ├── assignment.go      # clip ↔ scene assignment
│   ├── images.go          # per-scene image generation orchestration
│   └── metadata.go        # metadata extraction and enrichment
├── documents/
│   ├── builder.go         # Google Doc / HTML building
│   ├── google_docs.go     # Drive Doc upload
│   └── formatting.go      # text formatting, markdown cleanup
└── jobs/
    ├── handlers.go        # job lifecycle handlers
    ├── codec.go           # payload marshal/unmarshal
    └── payloads.go        # job payload types
```

The application layer is the **only** layer that orchestrates across
services. It imports `internal/media/`, `internal/sources/`,
`internal/repository/`, `internal/upload/`, etc.

## Module rules

Modules stay in `internal/api/` but follow stricter rules:

1. A module is a **route-registration adapter**, nothing more.
2. No module constructs its own concrete dependencies. All deps come from
   `internal/app/registry.go` or are passed via the handler's `Dependencies` struct.
3. `internal/api/module_*.go` files may only:
   - Wrap a handler in a `RouteModule`
   - Provide an `Enabled(cfg) bool` check
   - Call `handler.RegisterRoutes(group)`

## Size limits (CI-enforced)

| Directory | Max production .go files |
|-----------|--------------------------|
| `internal/api/` (root) | 15 |
| `internal/api/middleware/` | 10 |
| `internal/api/*/` (any feature) | 30 |
| `internal/application/scriptflow/*/` | 10 |

The CI check `scripts/ci-architectural-checks.sh` (Check 5) enforces these
limits. Directories over 40 files fail the build.

## Migration sequence

1. **PR 1** — Rules and boundaries (this document, AGENTS.md update, CI check) ✅
2. **PR 2** — Script transport: `internal/api/script/` (handler, DTOs, routes only)
3. **PR 3** — Extract `ScriptFlowHandler` generation orchestration → `internal/application/scriptflow/generate/`
4. **PR 4** — Extract scenes, documents, jobs → `scriptflow/scenes/`, `scriptflow/documents/`, `scriptflow/jobs/`
5. **PR 5** — Sources transport: `internal/api/sources/`
6. **PR 6** — Remaining feature APIs (assets, jobs, images, books, lessons, realtime, workers, admin, system)

## Measurable outcome

| Metric | Before | After |
|--------|--------|-------|
| `internal/api/` root files | 153 | 5–10 |
| `internal/api/script/` files | 72 (in flat) | 5–8 |
| `internal/api/sources/` files | 28 (in flat) | 5–8 |
| Any feature API files | n/a | 3–10 |
| Business logic in API | pervasive | zero |
| Package > 30 files | yes | none |
