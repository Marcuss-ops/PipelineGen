# VeloxEditing Go Master Server

> **Primary backend for the VeloxEditing automated video content creation system**
> **Stack:** Go (Gin) + Ollama + Google Drive/YouTube APIs
> **Default port:** 8080

---

## 📋 Overview

The Go Master server is the central API hub for the VeloxEditing platform. It orchestrates video creation, script generation, clip indexing, and stock management.

## 📁 Project Structure

```text
src/go-master/
├── cmd/
│   ├── server/           # Entry point (main.go)
│   ├── tools/            # CLI utilities (harvester, downloader, scanner)
│   ├── tests/            # Integration and stress testers
│   └── tmp/              # Debug scripts and temporary tools
├── internal/
│   ├── api/              # HTTP handlers (split by domain: clip, script, drive, etc.)
│   ├── bootstrap/        # Service wiring and lifecycle (init_*.go, wire.go)
│   ├── catalogdb/        # Unified SQLite catalog (FTS5 support)
│   ├── core/             # Business logic (job, worker, entities)
│   ├── ml/ollama/        # Ollama AI client
│   ├── service/          # High-level business services (pipeline, scriptdocs)
│   ├── storage/          # Persistence adapters (jsondb, postgres)
│   ├── clip/             # Clip indexing and semantic matching
│   ├── stock/            # Stock video management
│   └── youtube/          # YouTube client (yt-dlp)
├── pkg/
│   ├── config/           # Tag-driven configuration
│   ├── logger/           # Structured logging (Zap)
│   └── models/           # Shared data models
├── data/                 # JSON/SQLite database files (runtime)
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
| `OLLAMA_ADDR` | Ollama server URL |
| `VELOX_ADMIN_TOKEN` | Admin API token |

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

```text
┌──────────────────────────────────────────────────────────┐
│  GO MASTER (Port 8080)                                  │
│  ├─ HTTP API (Gin, Domain-based handlers)               │
│  ├─ Bootstrap & DI (internal/bootstrap)                 │
│  ├─ Job / Worker Orchestration                          │
│  ├─ Unified Catalog (SQLite Search)                     │
│  ├─ Script Generation (Ollama)                          │
│  ├─ Clip Indexing (Drive scanning + semantic match)     │
│  └─ External Integrations (YouTube, Drive, Rust)        │
└──────────────────────────────────────────────────────────┘
```

---

*Updated April 2026 — Production Ready*
