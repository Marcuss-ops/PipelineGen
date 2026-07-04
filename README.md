# PipelineGen - Advanced Media Processing Engine

PipelineGen is a Go-based backend service that manages media processing pipelines for YouTube clips and Artlist assets. It provides AI-driven script generation, voiceover synthesis, image generation, and Google Drive synchronization.

## Features

- YouTube clip search, extraction, metadata enrichment, and Drive storage.
- Artlist ingestion for automated asset search and download.
- AI voiceover generation with async job support.
- Image generation integrations.
- SQLite-backed job queue with progress tracking and event logs.
- Google Drive synchronization for generated media assets.

## Tech Stack

- Backend: Go with Gin
- Automation helpers: Python
- Database: SQLite with WAL mode
- External tools: yt-dlp and FFmpeg
- Cloud: Google Drive API
- CI/CD: GitHub Actions

## Getting Started

### Prerequisites

- Go 1.25+
- Python 3.10+
- yt-dlp installed in your path
- FFmpeg

### Installation

```bash
git clone https://github.com/Marcuss-ops/PipelineGen.git
cd PipelineGen
cp config.example.yaml config.yaml
go build -o pipelinegen ./cmd/server/
./pipelinegen --mode all
```

## API Endpoints (Core)

- Health: `GET /health`, `GET /health?deep=true`
- Script generation: `POST /api/script/generate` (canonical)
- Deprecated script adapters: `POST /api/script/generate-from-clips`, `POST /api/script/generate-with-images`
- Job status: `GET /api/jobs/:id`
- Images: `POST /api/images/generate`
- Artlist: `POST /api/artlist/run`
- Clips: `POST /api/clips/search`, `GET /api/clips/info`
- Media assets: `GET /api/media/search`, `GET /api/media/diagnostics`
- Voiceover: `POST /api/media/voiceover/generate`
- System: `GET /api/system/doctor`

## Job System

PipelineGen uses a unified job system for long-running operations. Jobs are stored in `data/media/media.db.sqlite` and processed by background workers. Track job progress through `/api/jobs` endpoints.

## Project Structure

- `cmd/`: server, worker, and admin entry points.
- `internal/api/`: HTTP transport layer.
- `internal/app/`: composition root, wiring, bootstrap, and module lifecycle.
- `internal/application/`: use-case orchestration.
- `internal/domain/`: canonical contracts and domain types.
- `internal/infrastructure/`: database, Drive, media, AI, process, and external-system adapters.
- `pkg/`: leaf utility packages.
- `scripts/`: utility and AI processing scripts.
- `migrations/sqlite/`: SQLite migrations.
- `config/`: YAML configuration and presets.

## Documentation

For the meta-question "what's authoritative here?", see [`CANONICAL.md`](./CANONICAL.md).

Canonical entry points:

- [`ARCHITECTURE.md`](./ARCHITECTURE.md): system diagram, data flow, module registry, and layer ownership.
- [`AGENTS.md`](./AGENTS.md): agent rules and workflow lessons.
- [`PROJECT_GUIDE.md`](./PROJECT_GUIDE.md): quick start in Italian.
- [`docs/api/ACTIVE_API_GENERATED.md`](./docs/api/ACTIVE_API_GENERATED.md): generated snapshot of currently registered HTTP routes.
- [`architecture/current.yaml`](./architecture/current.yaml): active architecture ratchet tracker.
- [`architecture/issues.yaml`](./architecture/issues.yaml): cross-package follow-up ledger.

*Developed by the Marcuss-ops Team*
