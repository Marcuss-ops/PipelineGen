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

- **Health**: `GET /health` (basic), `GET /health?deep=true` (component checks)
- **Script Generation**: `POST /api/script/generate-from-clips`, `POST /api/script/generate-with-images`
- **Job Status**: `GET /api/jobs/:id`
- **Images**: `POST /api/images/generate`
- **Artlist**: `POST /api/artlist/run`
- **Clips**: `POST /api/clips/search`, `GET /api/clips/info`
- **Media Assets**: `GET /api/media/search`, `GET /api/media/diagnostics`
- **Voiceover**: `POST /api/media/voiceover/generate`
- **System**: `GET /api/system/doctor`

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

La documentazione canonica del progetto è concentrata in due soli file markdown
alla radice del repository. Le directory `docs/` legacy sono state rimosse
in June 2026 e consolidate in `ARCHITECTURE.md` (regole operative e tracker
di migrazione) + `AGENTS.md` (regole per agenti AI e pattern canonici).

- [ARCHITECTURE.md](./ARCHITECTURE.md): **Canonical** — architettura di sistema,
  data flow, registry dei moduli, comandi day-1, ownership dei layer
  (`api → application → domain → infrastructure → pkg`). Include anche
  il piano di migrazione attivo (`architecture/current.yaml`) e la
  canonical ownership map ([`architecture/ownership.generated.yaml`](architecture/ownership.generated.yaml) — aggregated view of [`architecture/ownership/*.yaml`](architecture/ownership/) (per-section split, dc6add3e)).
- [AGENTS.md](./AGENTS.md): **Canonical** — regole critiche del sistema
  (driver SQLite, ban FTS5, schema boundaries, policy generazione AI,
  admin token env-var, Istruzioni per agenti AI, Git Lessons).
- [PROJECT_GUIDE.md](./PROJECT_GUIDE.md): Getting started in italiano.
- [docs/api/ACTIVE_API_GENERATED.md](./docs/api/ACTIVE_API_GENERATED.md):
  Snapshot auto-generato delle route HTTP attualmente registrate
  (rigenerabile con `./admin gen-api-docs`).

Per approfondimenti storici sulle migrazioni Wave X → Wave 17 (es.
perché certe directory sono state rimosse, mapping prima→dopo)
vedi `architecture/current.yaml` (ratchet tracker verificabile
via `bash scripts/ci-architectural-checks.sh`).

---

*Developed by the Marcuss-ops Team*
