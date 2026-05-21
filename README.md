# 🚀 PipelineGen - Advanced Media Processing Engine

PipelineGen is a powerful Go-based backend service designed to automate media processing pipelines. It handles content ingestion from various sources (Artlist, YouTube), performs AI-driven processing (Voiceovers, Image Generation), and manages storage synchronization with Google Drive.

## ✨ Features

- **📺 YouTube Clips**: Search and extract clips from YouTube with automatic metadata enrichment and storage on Google Drive.
- **🎵 Artlist Ingestion**: Automated searching and downloading of premium assets from Artlist.
- **🎙️ AI Voiceovers**: Batch generation of voiceovers using advanced TTS engines with full async support.
- **🖼️ Image Generation**: Integration with NVIDIA NIM and Flux AI for high-quality image assets.
- **🔄 Job System**: Robust, SQLite-backed asynchronous job queue with progress tracking and event logging.
- **📂 Cloud Sync**: Deep integration with Google Drive for asset organization and synchronization.
- **📈 Google Accounting**: Automated export and download of video assets from Google Vids Pro using Playwright.
- **🛠️ Admin UI**: Real-time monitoring and control interface (located in `web-admin/`).

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
- Playwright (for Google Accounting)

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

3. Install Python dependencies:
   ```bash
   pip install -r scripts/requirements.txt
   pip install -r google-accounting/requirements.txt
   playwright install chromium
   ```

4. Build and Run:
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
- **Google Accounting**: `GET http://localhost:8000/list`

## 🧠 Job System

PipelineGen uses a unified job system for all long-running operations. Jobs are stored in `data/velox/velox.db.sqlite` (Main Database) and are processed by background workers. You can track job progress via the `/api/jobs` endpoints.

## 📁 Project Structure

- `cmd/server/`: Entry point for the HTTP server and workers.
- `internal/core/`: Canonical contracts and interfaces.
- `internal/service/`: Business logic and service implementations.
- `internal/api/handlers/`: REST API handlers (modularized).
- `google-accounting/`: FastAPI service for Google Vids automation.
- `pkg/models/`: Shared data models.
- `scripts/`: Utility and AI processing scripts.

## 📝 Documentation
- [AGENTS.md](./AGENTS.md): Critical system rules and architectural overview.
- [PROJECT_GUIDE.md](./PROJECT_GUIDE.md): Italian language getting started guide.
- [docs/](./docs/): Detailed technical documentation.

---
*Developed by the Marcuss-ops Team*
