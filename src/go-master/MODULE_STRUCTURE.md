# Module Structure & Organization

> **Last Updated:** April 9, 2026  
> **Status:** In Progress - Phase 1 Complete

---

## 📊 Current Modular Structure

```
internal/
├── api/                          # HTTP Layer
│   ├── server.go                 # Server lifecycle
│   ├── routes.go                 # Route registration + DI
│   ├── middleware/               # Auth, logging, rate limiting
│   └── handlers/                 # HTTP handlers (thin)
│       ├── video.go              # Video creation (→ entity service)
│       ├── script.go             # Script generation
│       ├── voiceover.go          # Voiceover generation
│       ├── nlp.go                # NLP + entity endpoints
│       ├── stock.go              # Stock management
│       ├── clip.go               # Clip management
│       ├── job.go                # Job management
│       ├── worker.go             # Worker management
│       ├── dashboard.go          # Dashboard stats
│       ├── stats.go              # Statistics
│       ├── admin.go              # Admin endpoints
│       ├── scraper.go            # Node.js scraper integration
│       ├── drive.go              # Google Drive integration
│       ├── youtube.go            # YouTube integration
│       └── health.go             # Health checks
│
├── service/                      # ⚠️ TODO: Create this layer
│   ├── pipeline/
│   │   ├── video_creation.go     # Move from video.go handler
│   │   ├── script_generation.go
│   │   └── voiceover_generation.go
│   └── maintenance/
│       └── scheduler.go          # Background tasks from server.go
│
├── core/                         # Domain Layer
│   ├── entities/                 # ✅ Modularized
│   │   ├── types.go              # Domain types (Entity, Category)
│   │   ├── service.go            # EntityService orchestrator
│   │   ├── extractor_ollama.go   # Ollama-based entity extraction
│   │   └── segmenter_nlp.go      # NLP-based segmentation
│   ├── job/                      # Job domain logic
│   └── worker/                   # Worker domain logic
│
├── ml/                           # ML/AI Infrastructure
│   └── ollama/
│       ├── client.go             # HTTP client + entity extraction
│       ├── generate.go           # Script generation orchestrator
│       ├── prompts.go            # Prompt templates
│       └── types.go              # Request/response types
│
├── audio/                        # Audio Infrastructure
│   ├── tts/
│   │   ├── edge.go               # EdgeTTS CLI wrapper
│   │   ├── voices.go             # Voice mappings (16 languages)
│   │   └── types.go              # TTS types
│   └── processor.go              # Audio processing utilities
│
├── nlp/                          # NLP Utilities
│   ├── tokenizer.go              # Tokenization + stopwords
│   ├── tfidf.go                  # TF-IDF keyword extraction
│   ├── moments.go                # VTT parsing + moment extraction
│   └── types.go                  # NLP types (Moment, Keyword)
│
├── video/                        # Video Infrastructure
│   └── processor.go              # Rust binary executor
│
├── stock/                        # Stock Management
│   └── manager.go                # Stock project management
│
├── clip/                         # Clip Management
│   ├── suggester.go              # Clip suggestion logic
│   ├── cache.go                  # Search result caching
│   └── types.go                  # Clip types
│
├── upload/                       # Upload Infrastructure
│   ├── drive/                    # Google Drive client
│   └── youtube/                  # YouTube uploader
│
├── youtube/                      # YouTube Integration
│   └── downloader.go             # Video download logic
│
└── storage/                      # Storage Layer
    ├── interfaces.go             # Storage interfaces
    ├── factory.go                # Storage factory
    └── jsondb/                   # JSON file storage impl
```

---

## ✅ Completed Modularization

### 1. Entity Extraction Pipeline

**Files Created:**
- `internal/core/entities/types.go` - Domain types and interfaces
- `internal/core/entities/service.go` - EntityService orchestrator
- `internal/core/entities/extractor_ollama.go` - Ollama extractor impl
- `internal/core/entities/segmenter_nlp.go` - Segmentation impl

**What Changed:**
- ✅ Segmentation moved from handler to `core/entities/`
- ✅ Entity extraction uses interface-based approach
- ✅ CreateMaster handler delegates to EntityService
- ✅ API endpoint `/api/nlp/entities` uses entity service

**Architecture:**
```
Handler (video.go)
  ↓
EntityService.AnalyzeScript()
  ↓
NLPSegmenter.Split() → OllamaExtractor.ExtractFromScript()
  ↓
ScriptEntityAnalysis returned to handler
```

### 2. NLP Handler Cleanup

**Fixed:**
- ✅ Added missing request types (ExtractMomentsRequest, AnalyzeRequest, etc.)
- ✅ Fixed import issues with nlp package
- ✅ Entity extraction endpoint uses entity service
- ✅ Segment endpoint uses entity service segmenter

---

## 🎯 Next Steps (Priority Order)

### P0: Create Service Layer

**Why:** Remove orchestration logic from handlers

**What to create:**
```
internal/service/pipeline/
├── video_creation.go     # CreateMaster workflow
├── script_generation.go  # Script workflow
└── voiceover_generation.go
```

**What to move:**
- From `handlers/video.go` lines 510-812 → `service/pipeline/video_creation.go`
- Handler becomes thin: parse request → call service → return response

### P1: Split Large Handlers

**stock.go (1292 lines) → split into:**
- `stock_projects.go` - Project CRUD
- `stock_search.go` - YouTube search
- `stock_process.go` - Rust binary execution
- `stock_clip.go` - Clip/studio creation

**drive.go (600 lines) → split into:**
- `drive_folders.go` - Folder operations
- `drive_docs.go` - Document operations
- `drive_upload.go` - Upload operations

### P2: Add Interfaces

**Create interfaces for:**
- `video.VideoProcessor` interface
- `audio/tts.TTSGenerator` interface
- `ml/ollama.ScriptGenerator` interface
- `stock.StockManager` interface

**Define interfaces near consumers:**
```go
// In service/pipeline/video_creation.go
type VideoProcessor interface {
    GenerateVideo(ctx context.Context, req GenerationRequest) (*Result, error)
}
```

### P3: Move God Constructor

**From:** `internal/api/routes.go` NewRouter()

**To:** `cmd/server/main.go` composition root

**Why:** API layer should not know how to build ML clients, TTS engines, etc.

### P4: Extract Background Tasks

**From:** `internal/api/server.go` startBackgroundTasks()

**To:** `internal/service/maintenance/scheduler.go`

**Tasks to extract:**
- Zombie job checker
- Auto-cleanup
- Worker offline checker
- Auto-save

---

## 📏 Code Organization Stats

| Metric | Before | After | Target |
|--------|--------|-------|--------|
| **Largest handler file** | 825 lines (video.go) | 825 lines | < 300 lines |
| **Largest package** | ~3000 lines (handlers) | ~3000 lines | < 2000 lines |
| **Files > 500 lines** | 4 files | 4 files | 0 files |
| **Service layer files** | 0 | 4 | ~15 files |
| **Interfaces defined** | 0 | 2 | ~10 interfaces |
| **God constructor lines** | ~100 (routes.go) | ~100 | 0 (move to main.go) |

---

## 📚 References

- **Architecture Rules:** See `ARCHITECTURE_RULES.md`
- **Clean Architecture:** Robert C. Martin
- **Go Best Practices:** [Effective Go](https://go.dev/doc/effective_go)
- **Dependency Injection:** [Alex Edwards Blog](https://www.alexedwards.net/blog/dependency-injection-in-go)
