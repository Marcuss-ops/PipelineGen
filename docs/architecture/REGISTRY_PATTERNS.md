# REGISTRY_PATTERNS.md - Canonical Patterns for Registry and Resolver

**Last updated**: 2026-05-25
**Purpose**: Define canonical patterns to avoid logic duplication. All new features must use these patterns.

---

## Golden Rule

> **No logic duplication. Every new feature must hook into a shared registry, resolver, or sampler.**

---

## Canonical Patterns

### 1. Module Registry

**Interface**: `module.Module` in `internal/module/module.go`

**Registry**: `module.Registry` - manages module lifecycle

**When to use**: For every new feature that exposes APIs or has a lifecycle (Start/Stop)

**Example**:
```go
// 1. Implement module.Module
type MyModule struct {
    cfg *config.Config
}

func (m *MyModule) Name() string { return "my-module" }
func (m *MyModule) Enabled(cfg *config.Config) bool { return cfg.Features.MyModuleEnabled }
func (m *MyModule) RegisterRoutes(rg *gin.RouterGroup) { ... }
func (m *MyModule) Start(ctx context.Context) error { ... }
func (m *MyModule) Stop(ctx context.Context) error { ... }

// 2. Register in internal/app/registry.go
func WireRegistry(...) {
    registry.Register(myModule)
}
```

**Antipattern**: Creating direct handlers in the router without going through `module.Module`

---

### 2. Destination Resolver

**Canonical Interface**: `core/destination.Resolver` in `internal/core/destination/types.go`

```go
type Resolver interface {
    Resolve(ctx context.Context, req *ResolveRequest) (*ResolveResult, error)
}
```

**Implementation**: `assetdestination.Resolver` -> adapts to `core/destination.Resolver` via `assetdestination.ToCoreResolver()`

**When to use**: To resolve asset destinations (Drive, local, S3, etc.)

**Example**:
```go
// Use the canonical resolver
var resolver destination.Resolver

result, err := resolver.Resolve(ctx, &destination.ResolveRequest{
    Source:        "youtube",
    Group:         "sports",
    SubfolderName: "Athlete Name",
})
```

**Antipattern**: Using `drivedestination.Service` directly instead of the canonical resolver

**Migration**: Replace direct `drivedestination.Service` usage with `core/destination.Resolver`:
- `internal/sources/youtube/service.go` - uses `drivedestination.Service` directly
- `internal/sources/artlist/drive_service.go` - exposes `GetDriveDestination()`
- `internal/media/assetops/destination.go` - uses `drivedestination.Service` directly

---

### 3. Asset Registry

**Interface**: `assetregistry.Registry` in `internal/media/assetregistry/registry.go`

**When to use**: For CRUD operations on assets (clips, images, voiceovers)

**Example**:
```go
type ClipRegistry interface {
    SearchClips(ctx context.Context, term string) ([]*models.Clip, error)
    GetClip(ctx context.Context, id string) (*models.Clip, error)
    UpsertClip(ctx context.Context, clip *models.Clip) error
}
```

**Registration**:
```go
registry := assetregistry.NewRegistry(log)
registry.RegisterClipSource(assetSourceYouTube, youtubeRepo)
registry.RegisterClipSource(assetSourceArtlist, artlistRepo)
```

---

### 4. Media Processor

**Canonical Interface**: `mediaasset.MediaProcessor` in `internal/media/mediaasset/processor.go`

**When to use**: To process media (download, upload, transcode, etc.)

**Status**: **CANONICAL** - Only approved media processor

**Used by**: Artlist, YouTube, Voiceover, Stock

**Antipattern**: Creating custom processors for each module

---

### 5. Job System

**Canonical Interface**: `internal/jobs/` + `internal/repository/jobs/`

**When to use**: For async or long-running operations (> 3 seconds)

**Status**: **CANONICAL SYSTEM** - All async must go through here

**Database**: `velox.db.sqlite` (table `jobs`)

**Example**:
```go
// Enqueue job
err := jobService.Enqueue(ctx, &models.Job{
    Type: "media.artlist",
    Payload: map[string]interface{}{"term": "topic_name"},
})
```

**Antipattern**: Using `context.Background()` in handlers or detached goroutines

---

## Decision Matrix

| New Feature | Use Pattern | Why |
|-------------|-------------|-----|
| New API module | `module.Module` | Lifecycle management, route registration |
| New destination | `core/destination.Resolver` | Unified, supports Drive/local/S3 |
| New asset type | `assetregistry.Registry` | Common CRUD, source-based |
| New processor | `mediaasset.MediaProcessor` | Canonical media processor |
| New async operation | `jobs.Service` | Canonical job system |

---

## Checklist for New Features

Before adding a new feature, check:

- [ ] Does a registry/resolver already exist for this function?
- [ ] If yes, can I extend the existing one?
- [ ] If no, should I create a new registry or use one of the canonical patterns?
- [ ] Does the code use `context.Background()` in handlers? (NO!)
- [ ] Does the code use the job system for operations > 3 seconds?
- [ ] Did I register the module in `internal/app/registry.go`?
- [ ] Did I update `docs/architecture/MODULE_MAP.md`?

---

## CI Checks

Run `scripts/ci-architectural-checks.sh` to validate:
- No `context.Background()` in handlers
- Usage of canonical patterns
- No unnecessary thin wrappers
- No dead code

---

## Notes for Agents

1. Before writing new code, check if a pattern already exists for that function
2. Always use canonical interfaces from `internal/core/`
3. Register new modules via `module.Registry`
4. Use the job system for async operations
5. Don't create duplicates "because it's faster" - use the shared pattern
