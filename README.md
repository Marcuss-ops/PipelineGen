# VeloxEditing Backend — Production

Automated Video Content Creation Backend Stack: Go + Rust + Python (Ollama)
Updated: April 22, 2026

---

## 🎯 Project Structure

```text
refactored/
├── src/                         # Source code
│   ├── go-master/              # Go API Server (PRIMARY)
│   │   ├── cmd/                # Entry points
│   │   │   ├── server/         # API Server main
│   │   │   ├── tools/          # CLI Utilities (Harvester, Downloader, etc.)
│   │   │   ├── tests/          # Integration & stress tests
│   │   │   └── tmp/            # Debug & temporary scripts
│   │   ├── internal/           # Core logic (Domain-driven)
│   │   │   ├── api/            # HTTP handlers (split by domain)
│   │   │   ├── bootstrap/      # Service wiring & DI (WireServices)
│   │   │   ├── core/           # Pure business logic (Job, Worker, Entities)
│   │   │   ├── clip/           # Clip indexing & semantic matching
│   │   │   ├── catalogdb/      # Unified SQLite catalog
│   │   │   ├── storage/        # Storage adapters (JSON, Postgres)
│   │   │   └── ...             # Other domain services
│   │   ├── pkg/                # Shared utilities (config, logger)
│   │   └── Makefile            # Build & test commands
│   ├── rust/                   # Rust video processing engine
│   └── python/                 # AI text generation & transcription
├── bin/                        # Compiled binaries (server, video-stock-creator)
├── docs/                       # Project documentation & ADRs
├── scripts/                    # Operational scripts
└── data/                       # Databases & runtime state (unified_catalog.db)
```

---

## 🚀 Quick Start

### Prerequisites
- Go 1.21+
- Rust toolchain (required for video processing)
- Ollama running (`gemma3:4b` model)
- Google OAuth credentials (`credentials.json` + `token.json`)
- `yt-dlp` installed globally

### Start with helper script
```bash
./start.sh
```
If `bin/video-stock-creator.bundle` is missing, the server starts in **API-only mode**.

### Start Go Master manually
```bash
cd src/go-master
go run cmd/server/main.go
```

### Health Check
```bash
curl http://localhost:8080/health
```

---

## 📡 API Surface

The public health endpoint is:
`GET /health`

Most application endpoints are mounted under:
`/api/*`

For the complete endpoint inventory, see:
- `docs/API_ENDPOINTS.md`
- `docs/API_DOCUMENTATION.md`
- `docs/ENDPOINT_ATTIVI.md`

---

## 🏗️ Architecture

```text
Go Master (API + Orchestration)
  ├─ HTTP API (Gin)
  ├─ Bootstrap (Dependency Injection)
  ├─ Job / Worker management
  ├─ Script generation (Ollama)
  ├─ Unified Catalog (SQLite + FTS5)
  └─ Subprocess: Rust Video Engine

Rust Engine (Video Assembly)
  ├─ FFmpeg wrapper
  ├─ Transitions & Overlays
  └─ High-performance mixing

Python Helpers
  ├─ LLM client
  └─ YouTube transcript extraction
```

---

## 🧪 Testing

```bash
cd src/go-master
make test
make coverage-check
```

---

## ✅ CI/CD

GitHub Actions validates every PR on `src/go-master/` changes:
- Format & Vet
- Unit & Integration Tests
- Coverage threshold (60%)
- Build verification

---

*Production Ready — Updated April 2026*
