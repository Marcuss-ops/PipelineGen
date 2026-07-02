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

For the meta-question *"what's authoritative here?"* — which doc is
canonical for which topic, and which are read-only historical references
— see [**`CANONICAL.md`**](./CANONICAL.md).

Per-doc landing pages:

- [ARCHITECTURE.md](./ARCHITECTURE.md) — system diagram, data flow,
  module registry, day-1 commands, ownership dei layer
  (`api → application → domain → infrastructure → pkg`).
- [AGENTS.md](./AGENTS.md) — agent rules (DB driver, FTS5 ban,
  schema boundaries, AI generation policy, Git workflow lessons).
- [PROJECT_GUIDE.md](./PROJECT_GUIDE.md) — quick start in italiano.
- [docs/api/ACTIVE_API_GENERATED.md](./docs/api/ACTIVE_API_GENERATED.md)
  — auto-generated snapshot of currently-registered HTTP routes
  (regenerable with `./admin gen-api-docs`).

Per la migrazione Wave X → Wave 17 (stato attivo / storico),
vedi `architecture/current.yaml` (ratchet tracker verificabile
via `bash scripts/ci-architectural-checks.sh`).

## 📐 Architecture ledger consolidata

Canonical closure cycles (waves landed / commit chains completed)
are recorded here with their canonical SHAs and forward-pointers.
New entries are appended at the bottom as cycles reach
`status: done + exit_signal: true` in
[`architecture/current.yaml`](./architecture/current.yaml). Each
cycle also surfaces its forward-pointer into the cross-package
issues ledger
([`architecture/issues.yaml`](./architecture/issues.yaml)) when a
typed-enum retirement or capability extraction needs to land in a
different package.

### Commit 4-expanded (luglio 2026)

Commit 4-expanded closure cycle (luglio 2026): canonical SHA
`9aa4c9e2` (byte-equivalent-replay); forward-port SHA `0c74e408`
(handoff archive); closure-note SHA `7dba2adf` (AGENTS.md Active
Concerns #13); cleaning-anchor SHA `d4952d45` (audit-pin canonical
`//nolint:audit-pin:gdl-07-14 stock-cutover-commit4-expanded`);
§12-5 cross-package YouTube `IndexingStatus` forward-pointer tracked
in
[`architecture/issues.yaml#PR-CROSSPACKAGE-INDEXING-STATUS-§12-5`](./architecture/issues.yaml)
(`16b3aa61`) +
[`architecture/current.yaml#id-29`](./architecture/current.yaml)
(`exit_signal:true`). See
[`COMMIT_4_EXPANDED_HANDOFF.md`](./COMMIT_4_EXPANDED_HANDOFF.md)
§Post-landing-Audit for closure lineage + drift audit (3→4
sentinels + `ed4f8331` §12-4 mid-flight + 7→10 audit-pin anchors).

---

*Developed by the Marcuss-ops Team*
