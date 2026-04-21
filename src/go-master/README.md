# VeloxEditing Go Master Server

> **Primary backend for the VeloxEditing automated video content creation system**
> **Stack:** Go (Gin) + Ollama + Google Drive/YouTube APIs
> **Default port:** 8080

---

## 📋 Overview

The Go Master server is the central API hub for the VeloxEditing platform. It exposes 60+ HTTP endpoints for video creation, script generation, clip indexing, stock management, YouTube integration, and job/worker orchestration.

## 📁 Project Structure

```
src/go-master/
├── cmd/
│   ├── server/           # Entry point + DI wiring (main.go, wire.go, helpers.go)
│   ├── drive_scanner/    # Drive folder scanner utility
│   ├── test_artlist/     # Artlist integration tester
│   ├── test_drive/       # Drive integration tester
│   ├── test_harvester/   # Harvester integration tester
│   └── test_scan/        # Scanner integration tester
├── internal/
│   ├── adapters/         # Cross-package adapters
│   ├── api/              # HTTP server, routes, handlers (50+ files)
│   ├── appenddb/         # Append-only database
│   ├── artlist/          # Artlist watcher & downloader
│   ├── artlistdb/        # Artlist local DB
│   ├── artlistsync/      # Artlist Drive sync
│   ├── audio/tts/        # EdgeTTS voiceover generation
│   ├── clip/             # Clip indexing, semantic matching, scoring
│   ├── clipcache/        # Clip download cache
│   ├── clipdb/           # Clip index DB
│   ├── clipprocessor/    # Clip processing pipeline
│   ├── clipsearch/       # Dynamic clip search service
│   ├── core/
│   │   ├── entities/     # Entity extraction (Ollama + NLP)
│   │   ├── job/          # Job management service
│   │   └── worker/       # Worker management service
│   ├── ddgimages/        # DuckDuckGo image search
│   ├── download/         # Generic video downloader
│   ├── downloader/       # Platform-specific downloaders (TikTok)
│   ├── entityimages/     # Entity image finder
│   ├── gpu/              # GPU detection & management
│   ├── harvester/        # YouTube content harvester
│   ├── interview/        # Interview analyzer
│   ├── ml/ollama/        # Ollama AI client & generators
│   ├── nvidia/           # NVIDIA AI client
│   ├── nlp/              # NLP tokenization & TF-IDF
│   ├── script/           # Script → clip mapping
│   ├── stock/            # Stock video management
│   ├── stockdb/          # Stock clip DB
│   ├── stockjob/         # Stock job scheduler
│   ├── stocksync/        # Drive sync for stock clips
│   ├── storage/jsondb/   # JSON file storage (queue, workers, clip index)
│   ├── textgen/          # AI text generation with GPU support
│   ├── timestamp/        # Timestamp mapping service
│   ├── translation/      # Italian → English clip search translation
│   ├── upload/drive/     # Google Drive upload & OAuth
│   ├── video/            # Video processing adapter
│   ├── watcher/          # Drive file change watcher
│   └── youtube/          # YouTube client (yt-dlp backend)
├── pkg/
│   ├── config/           # Configuration (tag-driven defaults + env overrides)
│   ├── logger/           # Zap-based structured logging
│   ├── models/           # Shared data models (Job, Worker)
│   ├── security/         # Sanitization & URL validation
│   └── util/             # Math utilities
├── tests/
│   ├── e2e/              # End-to-end tests
│   ├── integration/      # Integration tests
│   └── mocks/            # Test mocks
├── config/               # Prometheus & alerting config
├── data/                 # JSON database files (runtime)
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
- yt-dlp installed
- FFmpeg installed (for video processing)

### Build & Run

```bash
cd src/go-master

# Build
go build -o ../../bin/server ./cmd/server

# Run with defaults
../../bin/server

# Or directly
go run ./cmd/server
```

### Health Check

```bash
curl http://localhost:8080/health
```

---

## ⚙️ Configuration

All configuration is **tag-driven**: defaults are defined via `default:` struct tags in `pkg/config/types.go`, and environment variable names via `env:` tags. The loader applies them automatically via reflection.

### Load Order
1. **Defaults** from struct tags (applied to zero-value fields)
2. **YAML config file** (if `config.yaml` exists, or path from `VELOX_CONFIG`)
3. **Environment variables** (from `env:` tags)

### Key Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `VELOX_HOST` | `0.0.0.0` | Server bind address |
| `VELOX_PORT` | `8080` | Server port |
| `VELOX_READ_TIMEOUT` | `600` | HTTP read timeout (seconds) |
| `VELOX_WRITE_TIMEOUT` | `600` | HTTP write timeout (seconds) |
| `VELOX_LOG_LEVEL` | `info` | Log level (debug, info, warn, error) |
| `VELOX_LOG_FORMAT` | `json` | Log format (json, console) |
| `VELOX_DATA_DIR` | `./data` | Data directory |
| `VELOX_ENABLE_AUTH` | `false` | Enable auth middleware |
| `VELOX_ADMIN_TOKEN` | `` | Admin API token |
| `OLLAMA_ADDR` | `http://localhost:11434` | Ollama server URL |
| `VELOX_STOCK_DIR` | `/tmp/velox` | Stock video directory |
| `VELOX_DOWNLOAD_DIR` | `/tmp/velox/downloads` | Download directory |
| `VELOX_YTDLP_PATH` | `yt-dlp` | Path to yt-dlp binary |

Full list in `pkg/config/types.go`.

---

## 🧪 Testing

```bash
# Run all tests
go test ./...

# With race detection
go test -race ./...

# Coverage report
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# Integration tests only
go test -tags=integration ./tests/integration/...
```

### Makefile Targets

```bash
make build          # Compile server
make test           # Run all tests
make test-unit      # Unit tests with race detection
make test-integration  # Integration tests
make coverage       # HTML coverage report
make lint           # golangci-lint
make fmt            # go fmt
make vet            # go vet
make swagger        # Generate Swagger docs
make ci             # Full CI pipeline (fmt + vet + lint + test + build)
```

---

## 📚 API Documentation

When the server is running, Swagger UI is available at:

```
http://localhost:8080/api/docs/index.html
```

See the root [README.md](../../README.md) for the full endpoint listing.

---

## 🏗️ Architecture

```
┌──────────────────────────────────────────────────────────┐
│  GO MASTER (Port 8080)                                  │
│  ├─ API HTTP (Gin, 60+ endpoints)                      │
│  ├─ DI Container (cmd/server/wire.go)                  │
│  ├─ Job / Worker Management                             │
│  ├─ Script Generation (Ollama)                          │
│  ├─ Entity Extraction (Ollama + NLP)                    │
│  ├─ Voiceover (EdgeTTS)                                │
│  ├─ Clip Indexing (Drive scanning + semantic match)     │
│  ├─ Stock Orchestrator (YouTube → download → Drive)     │
│  ├─ Channel Monitor (cron, AI folder classification)    │
│  ├─ Artlist Pipeline (search → download → classify)     │
│  ├─ Google Drive / YouTube Upload                       │
│  ├─ DriveSync / ArtlistSync (background)                │
│  ├─ DriveWatcher (file change events)                   │
│  ├─ Harvester (YouTube content harvesting)              │
│  ├─ Stock Job Scheduler (cron)                          │
│  └─ GPU / NVIDIA AI Integration                         │
└──────────────────────────────────────────────────────────┘
```

---

*Updated April 2026 — Production Ready*
