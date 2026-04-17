# VeloxEditing Backend — Gemini Context

This document serves as the primary instructional context for Gemini CLI interactions with the VeloxEditing Backend project.

## 🎯 Project Overview

VeloxEditing is an automated video content creation platform. The system is a multi-language distributed backend that orchestrates video generation, AI script writing, and content harvesting.

- **Primary Orchestrator:** Go Master (`src/go-master`)
- **Video Engine:** Rust Bundle (`src/rust`)
- **AI & Transcripts:** Python Helpers (`src/python`)
- **Data Harvesting:** Node.js Scraper (`src/node-scraper`)

## 🏗️ Architecture & Stack

### 1. Go Master (`src/go-master`)
The central node that provides the HTTP API and manages the lifecycle of video "jobs" and "workers".
- **Framework:** [Gin Gonic](https://github.com/gin-gonic/gin)
- **Database:** JSON files + SQLite (transitioning to PostgreSQL per ADR-0001).
- **Service Discovery:** Modular initialization pattern in `cmd/server/` (see `wire.go`, `init_*.go`).
- **Integrations:** Google Drive, YouTube API, Ollama (local AI), OpenAI.

### 2. Rust Video Engine (`src/rust`)
A high-performance video processing tool that wraps FFmpeg to perform complex assembly, transitions, and audio mixing.
- **Output:** Compiled into `bin/video-stock-creator.bundle`.
- **Communication:** Called by Go via subprocess/CLI.

### 3. Python Helpers (`src/python`)
Utilities for LLM interactions (Ollama) and extracting YouTube transcripts.

### 4. Node Scraper (`src/node-scraper`)
Playwright/Puppeteer-based scrapers for indexing stock footage and other metadata.

## 🚀 Building and Running

### Prerequisites
- Go 1.21+
- Rust toolchain
- Python 3.10+
- Node.js 18+
- Ollama (running `gemma3:4b` or equivalent)
- `yt-dlp` installed globally

### Key Commands

| Task | Command | Location |
| :--- | :--- | :--- |
| **Start Backend** | `./start.sh` | Root |
| **Build Go** | `make build` | `src/go-master` |
| **Build Rust** | `cargo build --release` | `src/rust` |
| **Test Go** | `make test` | `src/go-master` |
| **Linter Go** | `make lint` | `src/go-master` |
| **Install Python deps** | `pip install -r requirements.txt` | Root |

### Manual Execution (Go)
```bash
cd src/go-master
go run cmd/server/main.go
```

## 🧪 Testing & Quality

- **Unit Tests:** `make test-unit`
- **Integration Tests:** `make test-integration`
- **Coverage:** Total coverage must meet a **60% threshold** (`make coverage-check`).
- **CI/CD:** GitHub Actions validates PRs on `src/go-master` changes.

## 📜 Development Conventions

1.  **Dependency Injection:** In Go, services are wired in `src/go-master/cmd/server/wire.go`. Avoid global states; pass dependencies via constructors.
2.  **API Routing:** Defined in `src/go-master/internal/api/routes.go`. Most endpoints are under `/api/*` and require Auth/RateLimiting.
3.  **Logging:** Use the `zap` logger initialized in `pkg/logger`.
4.  **Error Handling:** Prefer `anyhow` in Rust and standard Go error patterns with context.
5.  **Storage:** Be mindful of the `data/` directory where JSON state is currently kept. Respect ADR-0001 when implementing new persistence logic.

## 📂 Directory Structure Highlights

- `bin/`: Holds compiled binaries (`server`, `video-stock-creator.bundle`).
- `config/`: Master configuration and versioning.
- `data/`: Runtime JSON databases and job queues.
- `docs/`: Comprehensive architecture, API, and ADR documents.
- `src/go-master/internal/`: Core business logic packages.
- `scripts/`: Operational and automation scripts.
