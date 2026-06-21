# 🚀 PipelineGen - Advanced Media Processing Engine

PipelineGen is a Go-based backend service that manages media processing pipelines
for YouTube clips and Artlist assets. It provides AI-driven script generation,
voiceover synthesis, image generation, and Google Drive synchronization.

## ✨ Features

- **📺 YouTube Clips**: Search and extract clips from YouTube with automatic metadata
  enrichment and storage on Google Drive.
- **🎵 Artlist Ingestion**: Automated searching and downloading of premium assets from
  Artlist.
- **🎙️ AI Voiceovers**: Batch generation of voiceovers using advanced TTS engines with
  full async support.
- **🖼️ Image Generation**: Integration with NVIDIA NIM and Flux AI for high-quality
  image assets.
- **🔄 Job System**: Robust, SQLite-backed asynchronous job queue with progress tracking
  and event logging.
- **📂 Cloud Sync**: Deep integration with Google Drive for asset organization and
  synchronization.

## 🛠 Tech Stack

- **Backend**: Go (Gin Gonic)
- **Scraper/Automation**: Python (FastAPI, Playwright)
- **Database**: SQLite (WAL mode for high concurrency)
- **External Tools**: `yt-dlp`, FFmpeg, Python scripts for AI processing
- **Cloud**: Google Drive API (OAuth2)
- **CI/CD**: GitHub Actions

## 🚀 Getting Started

### Prerequisites

- Go 1.25+
- Python 3.10+
- `yt-dlp` installed in your path
- FFmpeg

### Installation

1. Clone the repository:
   ```bash
   git clone https://github.com/Marcuss-ops/PipelineGen.git
   cd PipelineGen
   ```

2. Configure the application:
   ```bash
   cp config.example.yaml config.yaml
   # Edit config.yaml with your credentials and paths
   ```

3. Build and Run:
   ```bash
   go build -o pipelinegen ./cmd/server/
   ./pipelinegen --mode all
   ```

## 🔌 API Endpoints (Core)

- **Search & Sources**: `GET /api/sources/search/live?query=...`
- **Asset Management**: `GET /api/sources`
- **Job Status**: `GET /api/jobs/:id`
- **Images**: `POST /api/images/generate/nvidia`
- **Artlist**: `POST /api/artlist/run`

## 🧠 Job System

PipelineGen uses a unified job system for all long-running operations. Jobs are
stored in `data/media/media.db.sqlite` and processed by background workers.
See `ARCHITECTURE.md` for the full system diagram and module registry.
Track job progress via the `/api/jobs` endpoints.

## 📁 Project Structure

- `cmd/`: Entry points (`cmd/server/` HTTP + workers, `cmd/worker/` standalone worker, `cmd/admin/` CLI).
- `internal/api/`: HTTP transport layer (thin — no business logic).
- `internal/app/`: Composition root, wiring, bootstrap, module lifecycle.
- `internal/application/`: Use-case orchestration (scripts, assets, images, content, voiceover, jobs, association, realtime).
- `internal/domain/`: Canonical contracts and types (`domain/asset/`, `domain/job/`, `domain/script/`).
- `internal/infrastructure/`: Adapters to external systems (`database/sqlite/`, `drive/`, `media/ffmpeg/`, `ai/`, `qdrant/`, `process/`).
- `pkg/`: Leaf utility packages (retry, textutil, hashutil, concurrent, etc.).
- `scripts/`: Utility and AI processing scripts.
- `migrations/sqlite/`: SQLite migrations applied at startup.
- `config/`: YAML configuration and presets.

## 📝 Documentation

- [ARCHITECTURE.md](./ARCHITECTURE.md): Canonical system architecture, data flows,
  module registry, and day-1 commands.
- [Operational roadmap PR0–PR5](./docs/roadmap/README.md): Concrete numbered TODOs,
  architecture exit gates, and the final full-working end-to-end certification plan.
- [AGENTS.md](./AGENTS.md): Critical system rules and instructions for AI agents.
- [PROJECT_GUIDE.md](./PROJECT_GUIDE.md): Italian language getting started guide.
- [docs/SCRIPT_PIPELINE.md](./docs/SCRIPT_PIPELINE.md): Complete script pipeline
  documentation.
- [docs/](./docs/): Additional technical documentation.

---

*Developed by the Marcuss-ops Team*
