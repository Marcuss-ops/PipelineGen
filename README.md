# VeloxEditing Backend — Production

Automated Video Content Creation Backend Stack: Go + Rust + Python (Ollama)
Updated: April 23, 2026

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
│   │   ├── internal/           # Core logic (Highly Modularized < 300 lines/file)
│   │   │   ├── api/            # HTTP handlers (Modularized by domain)
│   │   │   │   └── handlers/script/ # 3-Zone Script Generation & Pipeline
│   │   │   ├── core/           # Pure business logic (Job, Worker, Entities)
│   │   │   ├── clipsearch/     # Dynamic clip harvesting & matching
│   │   │   ├── catalogdb/      # Unified SQLite catalog (FTS5)
│   │   │   ├── harvester/      # Scheduled content harvesting
│   │   │   ├── upload/drive/   # Robust Google Drive integration
│   │   │   └── ...             # Specialized domain services
│   │   ├── pkg/                # Shared utilities (config, logger)
│   │   └── Makefile            # Build & test commands
│   ├── rust/                   # Rust video processing engine
│   └── python/                 # AI text generation & transcription
├── bin/                        # Compiled binaries (server, video-stock-creator)
├── docs/                       # Project documentation & ADRs
├── scripts/                    # Operational scripts (Verification & E2E)
└── data/                       # Databases & runtime state (set via VELOX_DATA_DIR)
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

## 🏗️ New Features & Refactoring

### 🧩 Advanced Modularization
The entire Go backend has been refactored for maintainability. Every production file now strictly adheres to a **300-line limit**. Logic is cleanly separated into:
- `types.go`: Domain models and interfaces.
- `core.go` / `lifecycle.go`: Main business logic and state transitions.
- `worker.go`: Background loops and concurrent processing.
- `persistence.go` / `queries.go`: Storage operations.

### 📝 3-Zone Script Generation
Advanced script pipeline that automates the transition from raw text to professional video scripts:
1.  **Semantic Segmentation:** Intelligence splitting of text into meaningful chapters.
2.  **Entity & Highlight Extraction:** Automatic identification of protagonists, special names, and visual cues.
3.  **Automatic Clip Matching:** Multi-source association (Artlist + Stock Drive + YouTube) with visual hash deduplication.
4.  **Google Docs Integration:** Direct publishing of formatted scripts with links and metadata.

### 📂 Unified Catalog & Harvesting
- **SQLite + FTS5:** High-performance Full-Text Search for thousands of indexed clips.
- **Dynamic Harvester:** Parallel background workers for YouTube content discovery.
- **Smart Drive Client:** Auto-refreshing tokens, retry logic, and automatic topic-to-group routing.

---

## 📡 API Surface

The public health endpoint is:
`GET /health`

Most application endpoints are mounted under:
`/api/script-pipeline/*`

For the complete endpoint inventory, see:
- `docs/API_ENDPOINTS.md`
- `docs/API_DOCUMENTATION.md`
- `docs/ENDPOINT_ATTIVI.md`

---

## 🧪 Testing

```bash
cd src/go-master
make test
python3 scripts/generate_script.py --topic "Gervonta Davis" --text-file /tmp/gervonta_source.txt
```

---

## ✅ CI/CD

GitHub Actions validates every PR on `src/go-master/` changes:
- Format & Vet (Strict 300-line check)
- Unit & Integration Tests
- Coverage threshold (60%)
- Build verification

---

*Production Ready — Updated April 2026*
