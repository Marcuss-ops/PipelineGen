# Architecture Rules for VeloxEditing Backend

> **Purpose:** This document defines the architectural guidelines for this codebase.  
> **Rule:** All new code and refactoring MUST follow these rules.

---

## 📐 1. LAYERED ARCHITECTURE

The codebase follows a **4-layer architecture**. Dependencies flow **downward only**.

```
┌─────────────────────────────────────────────────────────────┐
│  Layer 1: API Layer (internal/api/)                        │
│  - HTTP handlers (thin)                                     │
│  - Route registration                                       │
│  - Middleware                                               │
│  ↓ depends on Layer 2                                       │
├─────────────────────────────────────────────────────────────┤
│  Layer 2: Service Layer (internal/service/)                │
│  - Orchestration / business logic                           │
│  - Multi-step workflows                                     │
│  - Transaction management                                   │
│  ↓ depends on Layer 3                                       │
├─────────────────────────────────────────────────────────────┤
│  Layer 3: Domain Layer (internal/core/)                    │
│  - Core business logic                                      │
│  - Domain types and interfaces                              │
│  - No external dependencies                                 │
│  ↓ depends on Layer 4                                       │
├─────────────────────────────────────────────────────────────┤
│  Layer 4: Infrastructure Layer (internal/*/ + pkg/)        │
│  - Database / storage                                       │
│  - External API clients (Ollama, Drive, YouTube)            │
│  - Binary executors (Rust, FFmpeg)                          │
│  - Utilities (logging, config, security)                    │
└─────────────────────────────────────────────────────────────┘
```

### ✅ Allowed Dependencies
- `api → service → core → infrastructure`
- `api → core` (skip service layer if trivial)
- `api → infrastructure` (only for direct integrations)
- `service → infrastructure` (through interfaces)

### ❌ Forbidden Dependencies
- Infrastructure → Service (reverse dependency)
- Core → API (domain should not know about HTTP)
- Any layer → same layer sibling (avoid cross-cutting)

---

## 🎯 2. HANDLER RULES

### 2.1 Handlers MUST be thin

Handlers are **only** responsible for:
1. Parse and validate HTTP request
2. Call service method
3. Format and return HTTP response

**Example — GOOD:**
```go
func (h *VideoHandler) CreateMaster(c *gin.Context) {
    var req CreateMasterRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(400, gin.H{"error": err.Error()})
        return
    }

    result, err := h.pipelineService.CreateMaster(c.Request.Context(), req)
    if err != nil {
        c.JSON(500, gin.H{"error": err.Error()})
        return
    }

    c.JSON(202, result)
}
```

**Example — BAD:**
```go
func (h *VideoHandler) CreateMaster(c *gin.Context) {
    // ❌ Don't do this in handler:
    script, _ := h.scriptGen.GenerateFromText(...)
    entities, _ := h.ollamaClient.ExtractEntities(...)
    voiceover, _ := h.voiceover.Generate(...)
    config := buildRustConfig(...)
    exec.Command("video-stock-creator", ...)
    // 200+ lines of business logic
}
```

### 2.2 Handlers MUST NOT contain
- Business logic (move to service layer)
- Database queries (move to storage/repository)
- External API calls (move to service layer)
- File system operations (move to infrastructure)
- Complex orchestration (move to service/pipeline/)

### 2.3 Handler file size limit
- **Max 300 lines per handler file**
- If larger → split into multiple handler files by feature

---

## 🏗️ 3. SERVICE LAYER RULES

### 3.1 Services encapsulate workflows

```
internal/service/
├── pipeline/
│   ├── video_creation.go     # CreateMaster workflow
│   ├── script_generation.go  # Script generation workflow
│   └── voiceover_generation.go
└── maintenance/
    └── scheduler.go          # Background tasks
```

### 3.2 Services MUST
- Accept dependencies via constructor (dependency injection)
- Return domain types or DTOs (not internal structs)
- Handle errors gracefully (log + return meaningful error)
- Be testable with mock dependencies

### 3.3 Services MUST NOT
- Know about HTTP (no `gin.Context`, no `http.Request`)
- Call `config.Get()` (receive config via constructor)
- Directly execute binaries (use executor abstraction)

---

## 🔌 4. INTERFACE RULES

### 4.1 Every external dependency MUST have an interface

| Package | Interface | Purpose |
|---------|-----------|---------|
| `video` | `VideoProcessor` | Video processing via Rust binary |
| `audio/tts` | `TTSGenerator` | Text-to-speech generation |
| `ml/ollama` | `ScriptGenerator` | Script generation via Ollama |
| `ml/ollama` | `EntityExtractor` | Entity extraction via Ollama |
| `stock` | `StockManager` | Stock video management |
| `upload/drive` | `DriveClient` | Google Drive API |

### 4.2 Define interfaces near the consumer

```go
// In service/pipeline/video_creation.go (NOT in video package)
type VideoProcessor interface {
    GenerateVideo(ctx context.Context, req GenerationRequest) (*Result, error)
}
```

### 4.3 Interface Segregation Principle

Prefer many small interfaces over one large interface:
```go
// ✅ GOOD
type ScriptGenerator interface {
    GenerateFromText(ctx context.Context, req *TextGenerationRequest) (*GenerationResult, error)
}

type EntityExtractor interface {
    ExtractEntities(ctx context.Context, text string) ([]Entity, error)
}

// ❌ BAD
type OllamaService interface {
    GenerateFromText(...)
    ExtractEntities(...)
    GenerateFromYouTube(...)
    Summarize(...)
    ListModels(...)
    CheckHealth(...)
}
```

---

## 📦 5. PACKAGE ORGANIZATION

### 5.1 Package responsibilities

| Package | Responsibility | MUST NOT |
|---------|---------------|----------|
| `internal/api/` | HTTP routing, middleware | Business logic |
| `internal/service/` | Workflow orchestration | HTTP, direct DB |
| `internal/core/` | Domain logic, types | External deps |
| `internal/ml/` | ML/AI client implementations | HTTP handlers |
| `internal/video/` | Video processing | Business logic |
| `internal/audio/` | Audio processing | Business logic |
| `internal/stock/` | Stock management | HTTP handlers |
| `internal/storage/` | Data persistence | Business logic |
| `internal/nlp/` | Text processing utilities | HTTP, business logic |
| `pkg/config/` | Configuration | Business logic |
| `pkg/logger/` | Logging | Business logic |
| `pkg/models/` | Shared data types | Business logic |
| `pkg/security/` | Security utilities | Business logic |

### 5.2 Package naming
- **Lowercase, single word** when possible: `video`, `audio`, `stock`
- **Descriptive when needed**: `pipeline`, `entities`, `upload`
- **No abbreviations**: `configuration` not `config` (except `pkg/config/` is OK)

### 5.3 Package size limits
- **Max 500 lines per .go file**
- **Max 2000 lines per package** (excluding tests)
- If exceeded → split into sub-packages

---

## 🚫 6. FORBIDDEN PATTERNS

### 6.1 No God Constructors

**❌ BAD:** `routes.go` instantiates all dependencies
```go
func NewRouter() *Router {
    ollamaClient := ollama.NewClient("", "")
    scriptGen := ollama.NewGenerator(ollamaClient)
    edgeTTS := tts.NewEdgeTTS("/tmp/velox/voiceovers")
    // ... 20 more initializations
}
```

**✅ GOOD:** Composition root in `cmd/server/main.go`
```go
func main() {
    ollamaClient := ollama.NewClient(cfg.OllamaAddr, cfg.OllamaModel)
    extractor := entities.NewOllamaExtractor(ollamaClient)
    segmenter := entities.NewNLPSegmenter()
    entityService := entities.NewEntityService(extractor, segmenter)

    pipelineService := pipeline.NewVideoCreationService(
        scriptGen, entityService, voiceover, videoProc,
    )

    videoHandler := handlers.NewVideoHandler(pipelineService)
    // ...
}
```

### 6.2 No hidden dependencies

**❌ BAD:**
```go
func (s *JobService) CreateJob(req JobRequest) (*Job, error) {
    cfg := config.Get() // Hidden dependency!
    // ...
}
```

**✅ GOOD:**
```go
func NewJobService(storage JobStorage, cfg *config.Config) *JobService {
    return &JobService{storage: storage, cfg: cfg}
}
```

### 6.3 No direct binary execution in handlers

**❌ BAD:**
```go
func (h *StockHandler) Process(c *gin.Context) {
    cmd := exec.Command("video-stock-creator", args...)
    output, _ := cmd.CombinedOutput()
    // ...
}
```

**✅ GOOD:**
```go
func (h *StockHandler) Process(c *gin.Context) {
    result, err := h.rustExecutor.Execute(ctx, req)
    // ...
}
```

### 6.4 No hardcoded paths

**❌ BAD:**
```go
outputPath := "/tmp/velox/output/" + name + ".mp4"
```

**✅ GOOD:**
```go
outputPath := filepath.Join(h.cfg.OutputDir, name+".mp4")
```

### 6.5 No circular imports

```
storage → core/worker → storage  ← CYCLE!
```

**Solution:** Move shared types to `pkg/models/`

---

## 🔧 7. DEPENDENCY INJECTION

### 7.1 Composition root

All dependency wiring happens in **one place**: `cmd/server/main.go`

### 7.2 Constructor pattern

```go
type VideoCreationService struct {
    scriptGen     ScriptGenerator
    entityService EntityService
    voiceover     TTSGenerator
    videoProc     VideoProcessor
}

func NewVideoCreationService(
    scriptGen ScriptGenerator,
    entityService EntityService,
    voiceover TTSGenerator,
    videoProc VideoProcessor,
) *VideoCreationService {
    return &VideoCreationService{
        scriptGen:     scriptGen,
        entityService: entityService,
        voiceover:     voiceover,
        videoProc:     videoProc,
    }
}
```

### 7.3 Interface satisfaction check

```go
// Compile-time interface compliance check
var _ ScriptGenerator = (*OllamaGenerator)(nil)
```

---

## 📝 8. ERROR HANDLING

### 8.1 Wrap errors with context

```go
// ❌ BAD
return nil, err

// ✅ GOOD
return nil, fmt.Errorf("failed to generate script: %w", err)
```

### 8.2 Log at the boundary

```go
// In handler:
result, err := h.service.DoSomething()
if err != nil {
    logger.Error("Operation failed", zap.Error(err))
    c.JSON(500, gin.H{"error": "Operation failed"})
    return
}
```

### 8.3 Service layer returns wrapped errors

```go
func (s *Service) DoSomething() (*Result, error) {
    data, err := s.external.Call()
    if err != nil {
        return nil, fmt.Errorf("external call failed: %w", err)
    }
    return data, nil
}
```

---

## 🧪 9. TESTING RULES

### 9.1 Every service MUST have tests

- Use interfaces for mock dependencies
- Test happy path AND error paths
- Min 70% coverage for service layer

### 9.2 Handlers SHOULD have tests

- Use `httptest.NewRecorder()` for HTTP testing
- Mock all service dependencies

### 9.3 Integration tests

- Test against real database (JSON files in temp dir)
- Test against real external services when possible

---

## 📏 10. CODE ORGANIZATION CHECKLIST

Before committing code, verify:

- [ ] Handler is < 300 lines
- [ ] No business logic in handler
- [ ] All external deps have interfaces
- [ ] No hardcoded paths (use config)
- [ ] No `config.Get()` in services
- [ ] Errors are wrapped with context
- [ ] Dependencies injected via constructor
- [ ] File is < 500 lines
- [ ] Package has single responsibility
- [ ] Tests exist for new code

---

## 🗺️ 11. CURRENT STATE vs TARGET STATE

### Current Issues (Priority Order)

| Priority | Issue | Target |
|----------|-------|--------|
| 🔴 P0 | `video.go` has 800+ lines orchestration | Move to `service/pipeline/` |
| 🔴 P0 | `stock.go` has 1200+ lines | Split into 3-4 handler files |
| 🔴 P0 | `routes.go` is God constructor | Move to `cmd/server/main.go` |
| 🟡 P1 | No interfaces for external deps | Add interfaces to all packages |
| 🟡 P1 | Hidden `config.Get()` dependencies | Pass config via constructors |
| 🟢 P2 | Hardcoded paths everywhere | Move to config |
| 🟢 P2 | Background tasks in server.go | Move to `service/maintenance/` |
| 🟢 P2 | NLP handler missing imports | Fix imports |

### Target Structure

```
internal/
├── api/handlers/         # Thin handlers only (< 300 lines each)
│   ├── video.go          # → delegates to pipeline service
│   ├── stock_projects.go # Split from stock.go
│   ├── stock_search.go   # Split from stock.go
│   └── ...
├── service/              # NEW: Orchestration layer
│   ├── pipeline/
│   │   └── video_creation.go
│   └── maintenance/
│       └── scheduler.go
├── core/                 # Domain logic
│   ├── entities/         # ✅ Already good
│   ├── job/
│   └── worker/
└── [infrastructure packages]
```

---

## 📚 12. REFERENCES

- [Clean Architecture by Robert C. Martin](https://www.amazon.com/Clean-Architecture-Craftsmans-Software-Structure/dp/0134494164)
- [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments)
- [Effective Go](https://go.dev/doc/effective_go)
- [Dependency Injection in Go](https://www.alexedwards.net/blog/dependency-injection-in-go)
