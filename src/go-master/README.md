# VeloxEditing Go Master Server

> **Primary backend for the VeloxEditing automated video content creation system**
> **Stack:** Go (Gin) + Ollama + Google Drive/YouTube APIs
> **Default port:** 8080

---

## 📋 Overview

The Go Master server is the central API hub for the VeloxEditing platform. It orchestrates video creation, script generation, clip indexing, and stock management.

## 📚 Technical Documentation

For deeper dives into specific components, refer to the following documentation:

- **[Job Lifecycle & State Machine](./docs/architecture/job_lifecycle.md)**: Details on how asynchronous tasks are managed, worker leases, and state transitions.
- **[Matching & Scoring System](./internal/matching/README.md)**: Deep dive into the algorithm used to rank and select assets (clips, images) for scripts.
- **[CLI Utilities Guide](./cmd/README.md)**: Catalog of maintenance, ingestion, and migration tools available in the `cmd/` directory.
- **[Google Docs Generation Flow](./SCRIPT_DOCS_GUIDE.md)**: Details on how scripts are formatted and published to Google Docs.

---

## 📁 Project Structure

```
src/go-master/
├── cmd/
│   ├── server/           # Entry point (main.go)
│   ├── indexer/          # CLI utilities (harvester, downloader, scanner)
│   └── tmp_publish_doc/  # Debug scripts and temporary tools
├── internal/
│   ├── api/
│   │   ├── handlers/
│   │   │   ├── script/      # Script generation handlers (modularized)
│   │   │   │   ├── handler_core.go
│   │   │   │   ├── handler_generate.go
│   │   │   │   ├── script_docs_builder.go
│   │   │   │   ├── script_docs_entities.go
│   │   │   │   ├── script_docs_render.go
│   │   │   │   ├── clip_drive_matching.go
│   │   │   │   ├── clip_drive_types.go
│   │   │   │   ├── clip_drive_catalog.go
│   │   │   │   ├── clip_drive_text.go
│   │   │   │   ├── stock_matching.go
│   │   │   │   ├── stock_catalog.go
│   │   │   │   ├── timeline_source_builder.go
│   │   │   │   ├── timeline_render.go
│   │   │   │   ├── timeline_types.go
│   │   │   │   └── timeline_utils.go
│   │   │   ├── common/        # Common handlers (health, utility)
│   │   │   ├── drive/        # Google Drive handlers
│   │   │   └── voiceover/    # Voiceover handlers
│   │   ├── middleware/      # HTTP middleware (auth, logging, rate limiting)
│   │   └── routes.go         # Route definitions
│   ├── bootstrap/        # Service wiring and lifecycle
│   ├── ml/
│   │   └── ollama/        # Ollama AI client (modularized)
│   │       ├── client_core.go
│   │       ├── client_generate.go
│   │       ├── client_entities.go
│   │       ├── client_health.go
│   │       ├── client_embed.go
│   │       ├── generate.go
│   │       ├── system_prompt.go
│   │       ├── prompt_builders.go
│   │       ├── types.go
│   │       └── utils.go
│   ├── service/          # High-level business services
│   │   ├── pipeline/      # Video pipeline service
│   │   └── voiceover/     # Voiceover service
│   ├── repository/      # Data access layer
│   │   └── scripts/       # Script repository (modularized)
│   │       ├── types.go
│   │       └── scripts.go
│   ├── models/          # Shared data models (modularized)
│   │   ├── job_types.go
│   │   ├── job_functions.go
│   │   ├── worker_types.go
│   │   └── worker_functions.go
│   ├── storage/         # Persistence adapters
│   │   ├── sqlite.go
│   │   ├── postgres/
│   │   └── sqlitecache/
│   ├── upload/           # Upload handlers
│   │   ├── drive/
│   │   └── youtube/
│   ├── core/             # Business logic
│   ├── cron/             # Scheduled tasks
│   └── runtime/          # Runtime utilities
├── pkg/                  # Public packages
│   ├── config/           # Tag-driven configuration
│   ├── logger/           # Structured logging (Zap)
│   ├── models/           # Public shared models
│   ├── security/         # Input sanitization
│   └── util/             # Utility functions
├── data/                 # JSON/SQLite database files
├── migrations/           # Database migrations
├── go.mod
├── go.sum
└── Makefile
```

---

## 🚀 Getting Started

### Prerequisites
- Go 1.21+
- Ollama running with `gemma3:4b` model
- Google OAuth credentials (`credentials.json` + `token.json`)
- `yt-dlp` installed

### Build & Run
```bash
cd src/go-master
go run cmd/server/main.go
```

---

## ⚙️ Configuration

Configuration is **tag-driven** via environment variables or `config.yaml`.

### Key Environment Variables
| Variable | Description |
|----------|-------------|
| `VELOX_PORT` | Server port (default: 8080) |
| `VELOX_STORAGE_BACKEND` | `json` or `postgres` |
| `VELOX_DB_DSN` | Postgres connection string |
| `VELOX_DATA_DIR` | Runtime data directory for JSON/SQLite state |
| `OLLAMA_ADDR` | Ollama server URL |
| `VELOX_ADMIN_TOKEN` | Admin API token |
| `VELOX_LOG_FORCE_SYNC` | Force log flush after each write in dev |

---

## 🧪 Testing

```bash
make test           # Run all tests
make test-unit      # Unit tests
make test-integration  # Integration tests
make coverage       # HTML coverage report
```

---

## 🏗️ Architecture

```
┌──────────────────────────────────────────────────┐
│  GO MASTER (Port 8080)                                  │
│  ├─ HTTP API (Gin, Domain-based handlers)               │
│  ├─ Bootstrap & DI (internal/bootstrap)                 │
│  ├─ Job / Worker Orchestration                          │
│  ├─ Script Generation (Ollama, Modular)                 │
│  ├─ Clip Indexing (Drive scanning + semantic match)     │
│  └─ External Integrations (YouTube, Drive, Rust)        │
└──────────────────────────────────────────────────┘
```

---

## 📝 Modularization Summary (April 2026)

The codebase has been refactored for better maintainability:

### Modularized Files:
1. **Script Handlers** - Split into 14 focused modules
2. **Ollama Client** - Split into 9 focused modules  
3. **Job Models** - Split into types + functions
4. **Worker Models** - Split into types + functions
5. **Script Repository** - Split into types + repository
6. **API Middleware** - Split into middleware + logger

### Benefits:
- ✅ Smaller, focused files (easier to navigate)
- ✅ Clear separation of concerns
- ✅ Better code reuse and testing
- ✅ Easier onboarding for new developers
- ✅ Prepared for horizontal scaling

*Updated April 2026 — Production Ready & Modular*
