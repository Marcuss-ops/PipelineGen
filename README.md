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
│   │   │   ├── artlist/     # Artlist API handlers
│   │   │   ├── youtubeclip/ # YouTube clip handlers
│   │   │   ├── common/      # Common handlers (health, utility)
│   │   │   ├── drive/       # Google Drive handlers
│   │   │   └── voiceover/   # Voiceover handlers
│   │   ├── middleware/      # HTTP middleware (auth, logging, rate limiting)
│   │   └── routes.go        # Route definitions
│   ├── bootstrap/        # Service wiring, DB init, migrations
│   ├── service/          # Business logic services
│   │   ├── artlist/      # Artlist pipeline service
│   │   ├── youtubeclip/  # YouTube clip extraction
│   │   ├── jobs/         # Job queue system
│   │   ├── catalog/      # Unified catalog (uses injected repos)
│   │   ├── association/  # Drive association service
│   │   ├── pipeline/     # Video pipeline service
│   │   └── voiceover/    # Voiceover service
│   ├── repository/       # Data access layer
│   │   ├── clips/        # YouTube clips repo + migrations
│   │   ├── catalog/      # Catalog repo (aggregates other repos)
│   │   └── scripts/      # Script repository
│   ├── storage/          # SQLite utilities, migrations, FTS5 diagnostics
│   ├── ml/
│   │   └── ollama/       # Ollama AI client (modularized)
│   ├── matching/         # Asset matching algorithms
│   ├── models/           # Shared data models
│   ├── upload/           # Upload handlers (drive, youtube)
│   ├── core/             # Business logic
│   ├── cron/             # Scheduled tasks
│   └── runtime/          # Runtime utilities
├── pkg/                  # Public utility packages
│   ├── media/            # Media processing (ffmpeg, downloader)
│   ├── logger/           # Structured logging (Zap)
│   └── ...               # Other utilities
├── migrations/           # Main DB migrations (velox.db, jobs.db)
│   ├── sqlite/
│   └── jobs/
├── data/                 # SQLite databases (gitignored)
│   ├── velox.db.sqlite      # Main DB (scripts, media, monitoring)
│   ├── stock.db.sqlite      # Stock footage clips
│   ├── clips.db.sqlite      # YouTube clips + embeddings
│   ├── artlist.db.sqlite    # Artlist assets
│   ├── images.db.sqlite     # Images (placeholder)
│   ├── voiceover.db.sqlite  # Voiceovers (placeholder)
│   └── jobs.db.sqlite       # Job queue
├── docs/                 # Documentation
│   ├── sqlite-databases.md  # Database architecture (MUST READ)
│   ├── api/
│   ├── architecture/
│   └── workflows/
├── go.mod
├── go.sum
├── AGENTS.md             # Instructions for AI agents
└── Makefile
```

### Key Files for Agents
- **AGENTS.md** - Critical rules and instructions
- **docs/archive/sqlite-databases.md** - Database schema boundaries and migration strategy
- **internal/storage/migrations.go** - Migration runner with FTS5 handling
- **internal/bootstrap/** - Database initialization and service wiring

---

## 🚀 Getting Started

### Prerequisites
- Go 1.25.9
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
│  GO MASTER (Port 8080)                          │
│  ├─ HTTP API (Gin, Domain-based handlers)       │
│  ├─ Bootstrap & DI (internal/bootstrap)          │
│  ├─ Job / Worker Orchestration (internal/service/jobs) │
│  ├─ Script Generation (Ollama, Modular)         │
│  ├─ Multi-DB Repository Layer                    │
│  │   ├─ velox.db ← scripts, media, monitoring   │
│  │   ├─ stock.db ← stock footage clips          │
│  │   ├─ clips.db ← YouTube clips + embeddings   │
│  │   ├─ artlist.db ← Artlist assets             │
│  │   └─ jobs.db ← job queue                     │
│  ├─ Clip Indexing (Drive scanning + matching)    │
│  └─ External Integrations (YouTube, Drive, Ollama) │
└──────────────────────────────────────────────────┘
```

### Database Architecture
The system uses **7 separate SQLite databases** for separation of concerns:
- See `docs/archive/sqlite-databases.md` for full schema documentation
- See `AGENTS.md` for critical rules on database boundaries

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
