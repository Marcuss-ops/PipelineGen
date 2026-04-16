# VeloxEditing Backend — Production

> **Automated Video Content Creation Backend**
> **Stack:** Go + Rust + Python (Ollama)
> **Updated:** April 16, 2026

---

## 🎯 Project Structure

```
refactored/
├── src/                         # Source code
│   ├── go-master/              # Go API Server (PRIMARY)
│   │   ├── cmd/server/         # Entry point (42 lines, DI pattern)
│   │   ├── internal/           # Core logic (50+ packages)
│   │   │   ├── api/            # HTTP handlers + middleware + routes
│   │   │   ├── di/             # Dependency injection container
│   │   │   ├── core/           # Job, worker, entity services
│   │   │   ├── service/        # Business services
│   │   │   ├── storage/        # JSON file + SQLite storage
│   │   │   ├── upload/         # Google Drive + YouTube upload
│   │   │   ├── ml/ollama/      # Ollama AI integration
│   │   │   ├── gpu/            # GPU detection
│   │   │   ├── clip/           # Clip indexing, semantic matching
│   │   │   ├── stock/          # Stock video management
│   │   │   └── youtube/        # YouTube client (yt-dlp)
│   │   ├── pkg/                # Shared models, config, logger
│   │   ├── data/               # JSON database (runtime)
│   │   ├── tests/              # Unit & integration tests
│   │   └── Makefile            # Build & test commands
│   ├── rust/                   # Rust video processing (Cargo project)
│   └── python/                 # Ollama text generation (script + transcript)
├── bin/                        # Compiled binaries
├── docs/                       # Documentation
├── scripts/                    # Utility scripts
└── .github/workflows/          # CI workflows
```

---

## 🚀 Quick Start

### Prerequisites
- Go 1.21+
- Rust toolchain (optional at startup, required for full video processing)
- Ollama running (`gemma3:4b` model)
- Google OAuth credentials (Drive + YouTube)
- yt-dlp installed

### Start with helper script
```bash
./start.sh
```

If `bin/video-stock-creator.bundle` is missing, the script starts the Go Master in **API-only mode** and warns that Rust-backed video endpoints are unavailable until the bundle is compiled.

### Start Go Master manually
```bash
cd src/go-master
go build -o ../../bin/server ./cmd/server
../../bin/server

# Or directly:
go run cmd/server/main.go
```

### Health Check
```bash
curl http://localhost:8080/health
```

---

## 📡 API Surface

The public health endpoint is:

```bash
GET /health
```

Most application endpoints are mounted under:

```bash
/api/*
```

For the complete endpoint inventory, see:
- `docs/API_ENDPOINTS.md`
- `docs/API_DOCUMENTATION.md`
- `docs/ENDPOINT_ATTIVI.md`

---

## 🏗️ Architecture

```text
Go Master (API + orchestration)
  ├─ HTTP API (Gin)
  ├─ Job / Worker management
  ├─ Script generation (Ollama)
  ├─ Clip indexing and stock orchestration
  ├─ Drive / YouTube integration
  └─ Calls Rust bundle for video assembly when available

Rust bundle
  ├─ FFmpeg video assembly
  ├─ Transitions & effects
  └─ Audio mixing

Python helpers
  ├─ Text generation
  └─ Transcript-related utilities
```

---

## 🧪 Testing

```bash
cd src/go-master
make test
make coverage
```

Main targets:

```bash
make build
make test
make test-unit
make test-integration
make coverage
make coverage-check
make fmt
make vet
make lint
make swagger
make ci
```

---

## ✅ CI

GitHub Actions is configured for the Go Master and runs on push / pull request for `main`, `master`, and `develop` when files under `src/go-master/` change.

The workflow performs:
- format check
- `go vet`
- unit tests
- integration tests
- coverage threshold check
- build verification

---

## ⚙️ Notes

- **Go Master** is the main entry point.
- **Rust bundle** is required for full video processing, but not for basic API startup.
- **Health endpoint** is `/health`, not `/api/health`.
- Some historical docs may list extra endpoints; treat `src/go-master/internal/api/routes.go` as the source of truth.

---

*Production Ready — Updated April 16, 2026*
