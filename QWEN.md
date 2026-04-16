# VeloxEditing Backend - Project Context

## Project Overview

**VeloxEditing** is a production backend system for automated video content creation. It orchestrates the full pipeline of video generation — from script writing and voiceover synthesis to stock clip downloading, video editing with transitions/effects, and publishing to Google Drive/YouTube.

### Architecture Summary

The system is built on a **Go + Rust + Python** stack, with a clear separation of concerns:

| Layer | Technology | Role |
|-------|-----------|------|
| **Layer 1 — API Server** | Go (Gin framework) | HTTP API, job orchestration, worker management, external service integration (Ollama, EdgeTTS, Google Drive, YouTube) |
| **Layer 2 — Video Processing** | Rust (precompiled binary `video-stock-creator.bundle`) | CPU-intensive video processing: FFmpeg pipelines, transitions, effects, audio mixing |
| **Layer 3 — Workers** (optional) | Go | Distributed job execution, polling the master for work, calling the Rust binary |
| **Layer 4 — Python Legacy** | Python | Historical modules in `modules/` — **not integrated** into the Go backend, kept for reference |

### Directory Structure

```
refactored/
├── go-master/                 # Go API Server (PRIMARY — port 8080)
│   ├── cmd/server/            # Entry point
│   ├── internal/              # Core logic (api, core, storage, video, audio, ml, upload, etc.)
│   ├── pkg/                   # Shared models, config, logger
│   ├── tests/                 # Unit & integration tests
│   ├── server                 # Precompiled binary
│   ├── go.mod / go.sum        # Go dependencies
│   ├── Makefile               # Build & test commands
│   └── credentials.json / token.json  # Google OAuth tokens
│
├── video-stock-creator.bundle # Rust binary for video processing
│
├── modules/                   # Python legacy code (NOT used by Go backend)
│   ├── video/                 # Old FFmpeg Python code (replaced by Rust)
│   ├── youtube/               # YouTube management utilities
│   ├── audio/                 # Audio processing
│   ├── core/                  # Old job/state management (replaced by Go)
│   └── utils/                 # Utility modules
│
├── scripts/                   # Utility scripts
│   ├── worker_bootstrap.py    # Worker bootstrap
│   ├── entity_classifier.py   # Entity classification
│   ├── Generatevideoparallelized.py  # Parallel video generation
│   └── standalone_multi_video.py     # Multi-video standalone
│
├── docs/                      # Documentation
│   ├── API_DOCUMENTATION.md   # Full API docs
│   ├── API_ENDPOINTS.md       # Endpoint listing
│   ├── MASTER_API_YOUTUBE.md  # YouTube API integration
│   ├── SCRIPT_GENERATION_API.md  # Script generation API
│   └── STOCK_CLIPS_API.md     # Stock clips API
│
├── assets/                    # Shared assets (audio, fonts, transitions)
├── config/                    # Configuration files
├── data/                      # JSON database (queue, jobs, workers)
├── effects/                   # Video effects definitions
└── requirements.txt           # Python dependencies (for legacy modules)
```

## Key Technologies

- **Go 1.18+** — API server (Gin framework), job/worker management, external integrations
- **Rust** — Precompiled binary `video-stock-creator.bundle` for video processing
- **Python** — Legacy modules (not actively used); utilities for tracing, YouTube management
- **FFmpeg** — Video/audio processing (wrapped by Rust binary)
- **Ollama** — AI script generation
- **EdgeTTS** — Voiceover/text-to-speech
- **Google Drive & YouTube APIs** — Upload and publishing
- **JSON file storage** — Simple persistence layer (`data/` directory)

## Building and Running

### Prerequisites

- Go 1.18 or higher
- The `video-stock-creator.bundle` binary must be present in the project root
- (Optional) Python 3.x for legacy modules and utility scripts

### Start the Go Master Server (Port 8080)

```bash
# Quick start (uses precompiled binary if available, otherwise compiles)
./start.sh

# Or manually:
cd go-master
go build -o server ./cmd/server
./server

# Or with Go run:
cd go-master
go run cmd/server/main.go
```

### Start the Go Worker (Optional, Port 5000)

```bash
cd go-worker
go run cmd/worker/main.go
```

### Makefile Targets (in `go-master/`)

```bash
make build          # Compile the server
make run            # Build and run
make test           # Run all tests
make test-unit      # Run unit tests with race detection
make test-integration  # Run integration tests
make coverage       # Generate HTML coverage report
make lint           # Run golangci-lint
make fmt            # Format code
make vet            # Run go vet
make swagger        # Generate Swagger docs
make clean          # Clean build artifacts
make deps           # Download and tidy dependencies
```

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `VELOX_HOST` | `0.0.0.0` | Server bind address |
| `VELOX_PORT` | `8000` | Server port |
| `VELOX_LOG_LEVEL` | `info` | Log level (debug, info, warn, error) |
| `VELOX_LOG_FORMAT` | `json` | Log format (json, console) |
| `VELOX_DATA_DIR` | `./data` | Data directory for JSON DB |
| `VELOX_ENABLE_AUTH` | `false` | Enable authentication middleware |
| `VELOX_ADMIN_TOKEN` | `` | Admin API token |
| `VELOX_MAX_PARALLEL_PER_PROJECT` | `2` | Max parallel jobs per project |

## API Endpoints

### Core Video Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/video/create-master` | Main entry point for video creation |
| POST | `/api/video/generate` | Generate video via Rust binary |
| POST | `/api/video/process` | Process existing video |
| GET | `/api/video/status/:id` | Get processing status |

### Job Management

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/jobs/create` | Create new job |
| GET | `/api/jobs/:id` | Get job details |
| GET | `/api/jobs` | List jobs |
| POST | `/api/jobs/:id/complete` | Complete job (worker) |
| POST | `/api/jobs/:id/cancel` | Cancel job |

### Worker Management

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/workers` | List workers |
| POST | `/api/workers/register` | Register worker |
| POST | `/api/workers/heartbeat` | Heartbeat from worker |
| GET | `/api/workers/jobs` | Available jobs for polling |

### Script & Voiceover

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/script/generate` | Generate script via Ollama |
| POST | `/api/voiceover/generate` | Generate voiceover via EdgeTTS |

### Stock & Clips

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/stock/create` | Create stock clip |
| POST | `/api/stock/batch-create` | Batch stock creation |
| POST | `/api/clip/suggest` | Get clip suggestions |
| POST | `/api/clip/search-folders` | Search clip folders |

### Health & Docs

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/health` | Health check |
| GET | `/api/docs/*` | Swagger UI |
| GET | `/api/metrics` | Prometheus metrics |

## Testing

```bash
# Go tests
cd go-master
go test ./... -v

# With race detection
go test -race ./...

# Coverage report
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# Health check
curl http://localhost:8080/health
```

## Development Conventions

- **Go code**: Standard Go project layout with `cmd/`, `internal/`, `pkg/` directories
- **Interfaces**: Core components define interfaces in `internal/core/*/interfaces.go` for clean separation between agents/layers
- **Storage**: JSON file-based persistence in `data/` directory
- **Logging**: Structured logging via `go.uber.org/zap` (JSON format by default)
- **Testing**: Unit tests in `internal/...` and integration tests in `tests/integration/...`
- **Code quality**: `golangci-lint`, `go vet`, `go fmt` are used for linting

## Important Notes

1. **Python modules in `modules/` are legacy** — they are NOT called by the Go backend. The operational system is Go + Rust.
2. **The Rust binary (`video-stock-creator.bundle`) is mandatory** — it must be present in the project root or in PATH.
3. **Go Worker is optional** — the Master can execute jobs synchronously without workers.
4. **Swagger docs** are available at `http://localhost:8080/api/docs/index.html` when the server is running.
5. **Google OAuth** — `credentials.json` and `token.json` in `go-master/` are needed for Drive/YouTube integration.

## Python Dependencies

Python dependencies for legacy modules and utility scripts are listed in `requirements.txt`. Key packages include:
- Video: `moviepy`, `opencv-python-headless`, `pillow`
- Audio: `faster-whisper`, `pydub`, `librosa`, `edge-tts`
- AI/ML: `torch`, `spacy`, `openai`, `groq`
- Web: `fastapi`, `uvicorn`, `gradio`, `selenium`
- Data: `pandas`, `numpy`, `rapidfuzz`
- Cloud: `boto3`, `google-api-python-client`, `gdown`

---

## 📅 Session Log — April 12, 2026

### Google Docs Pipeline — Script + Entity + Clip Association

Today we built and iterated on a **Google Docs generation pipeline** for the script/entity/clip workflow. Key evolution:

#### What Was Built
1. **Script generation** via Ollama (`gemma3:4b`) — full text ~250 words with specific facts
2. **Entity extraction** — 3 categories:
   - **Frasi Importanti**: Top 5 key sentences from the script
   - **Nomi Speciali**: Proper nouns (capitalized, >2 chars, not stopwords)
   - **Parole Importanti**: Frequent meaningful words (>4 chars, not stopwords)
3. **Stock Drive folder linking** — Finds the matching Stock folder (e.g., `Stock/Boxe/Andrewtate`) and links the **entire folder**, not individual clips (clips are short, folder link is more useful)
4. **Artlist clip association** — Concept-to-tag mapping:
   - Maps Italian concepts in sentences → Artlist search terms
   - Example: `"kickbox"` → `gym workout`, `"Romania"` → `city`, `"TikTok"` → `smartphone usage`
   - Only associates when it makes sense (not forced)
5. **Phrase-to-clip associations** — Each important sentence gets either:
   - 📦 **Stock**: Link to the Stock folder (all clips are there)
   - 🟢 **Artlist**: Specific clip + concept match reason

#### Document Format (Final)
```
📝 [Title]
Topic: X | Durata: 1:20 | DD/MM/YYYY
================================================================================

[Full script text ~250 words]

================================================================================

⏱️ Durata: 0:00 - 1:20

================================================================================

🔍 ENTITÀ ESTRATTE

📌 FRASI IMPORTANTI (5)
   1. [sentence]
   ...

👤 NOMI SPECIALI (10)
   [comma-separated names]

🔑 PAROLE IMPORTANTI (10)
   [comma-separated keywords]

================================================================================

📦 STOCK DRIVE

📁 Stock/Boxe/Andrewtate
🔗 https://drive.google.com/drive/folders/[folder_id]

--------------------------------------------------------------------------------

🎬 ASSOCIAZIONI FRASE → CLIP

1. 💬 "[sentence]..."
   📦 Stock: Stock/Boxe/Andrewtate
   🔗 https://drive.google.com/drive/folders/[folder_id]

2. 💬 "[sentence]..."
   🟢 Artlist: city_timelapse_clip.mp4
   📁 Artlist
   🔍 Concept: 'arresto'

================================================================================
```

#### Key Design Decisions
- **Stock folder link** (not individual clips) — clips are short, better to give the full folder
- **Artlist only when it makes sense** — concept-to-tag mapping, not forced associations
- **No "ragni" (spiders)** — Artlist clips are searched by relevant tags, not random
- **Max 3 Artlist clips** — only for sentences with matching concepts
- **Single timestamp** (0:00 - 1:20) — not broken into segments when topic doesn't change
- **Token refresh via API** — Google token refresh works via the Go server API, not browser OAuth

#### Stock Drive Structure
The main Stock folder is at: `https://drive.google.com/drive/u/1/folders/1wt4hqmHD5qEsNhpUUBszlRkSHhyFgtGh`

Categories:
- **Boxe** (9 subfolders): Floyd, Anthony Joshua, MUhamadali, Andrewtate, Gevonta Jake, Stock Gervonta...
- **Crimine** (6): Alito, Carlos Manzo, Marcola, escobar, elchapo, Bolsonaro
- **Discovery** (10): Mr bean, Client Eastowod, Dicaprio, Robert redford, Tom Cruise, Brucelee, Chuck Norris, princess Diana, GeorgeClooney...
- **Wwe** (11): Vince Mchamon, Cody Rhodes, Broke Lesnar, Triple H, BIgShow, JeyUso, CmPunk, Undertaker, LIv, Jacob Fatu...

Each subfolder has ~5 short clips.

#### Artlist Search Terms (69 total)
Key ones used for concept mapping:
- `gym workout`, `city`, `city timelapse`, `people`, `presentation`
- `finance charts`, `smartphone usage`, `startup`, `office meeting`
- Full list in `src/node-scraper/artlist_videos.db` → `search_terms` table

#### Python Scripts Status
Several temporary Python scripts were created during today's session. The logic needs to be:
1. **Moved into Go business code** (`internal/service/`) as a proper service
2. **Temporary .py files deleted** after migration

Scripts to clean up:
- `scripts/upload_to_google_docs.py` — temporary, to be removed
- `scripts/upload_final_docs.py` — temporary, to be removed
- Logic should live in `internal/service/entityclip/` (new package)

#### TODO / Next Steps
1. ~~Google Token~~ ✅ **FIXED** — Don't pass `scopes` when creating Credentials; Google uses original scopes from refresh token
2. ~~Migrate to Go~~ ✅ **DONE** — New service `internal/service/scriptdocs/` with handler `internal/api/handlers/script_docs.go`
   - Endpoint: `POST /api/script-docs/generate`
   - Loads Artlist index from `data/artlist_stock_index.json`
   - Concept mapping: Italian keywords → Artlist terms (people, city, technology, nature)
3. ~~Clean up Python scripts~~ ✅ **DONE** — Removed 8 temp files
4. **Artlist clips on Drive** ✅ **DONE** — 25 clips (5 per term × 5 terms) downloaded from m3u8, converted to 1920x1080, uploaded to `Stock/Artlist/`

### Artlist → Stock Pipeline

**How it works:**
1. Read m3u8 URLs from `src/node-scraper/artlist_videos.db` (5 terms with 50 clips each)
2. Download via ffmpeg (max 15s per clip)
3. Convert to 1920x1080 MP4 (H.264, AAC audio)
4. Upload to Google Drive in `Stock/Artlist/{Term}/` subfolders
5. Save index to `data/artlist_stock_index.json`

**Current Artlist clips on Drive (25 total):**
- `Stock/Artlist/City/` — 5 clips (cityscapes, arrests, locations)
- `Stock/Artlist/Nature/` — 5 clips (nature B-roll)
- `Stock/Artlist/People/` — 5 clips (people, audiences, followers)
- `Stock/Artlist/Spider/` — 5 clips (spider web B-roll — legacy)
- `Stock/Artlist/Technology/` — 5 clips (tech, social media, platforms)

**Pipeline script:** `scripts/download_artlist_to_stock.py` — can be re-run with `CLIPS_PER_TERM` adjusted to download more.

### Go Service: Script Docs

**Package:** `internal/service/scriptdocs/`

**Types:**
- `ArtlistClip` — single clip with name, term, URL, folder
- `ArtlistIndex` — all clips grouped by term
- `ScriptDocRequest` — input: {topic, duration}
- `ScriptDocResult` — output: doc ID, URL, entities, associations
- `ClipAssociation` — phrase → clip mapping (STOCK or ARTLIST)

**Pipeline:**
1. Generate script via `ollama.Generator.Generate()`
2. Extract entities inline (sentences, proper nouns, keywords)
3. Associate clips via concept mapping (Italian keywords → Artlist terms)
4. Build formatted document text
5. Create Google Doc via `drive.DocClient.CreateDoc()`

**Endpoint:** `POST /api/script-docs/generate`
```json
{"topic": "Andrew Tate", "duration": 80}
```

### Files Created Today
- `src/go-master/internal/service/scriptdocs/service.go` — Core service
- `src/go-master/internal/api/handlers/script_docs.go` — HTTP handler
- `src/go-master/internal/api/handlers/drive.go` — Added `GetDocClient()` method
- `data/artlist_stock_index.json` — Artlist clip index (25 clips)
- `scripts/download_artlist_to_stock.py` — Download Artlist clips to Drive

### Files Removed Today
- `scripts/upload_to_google_docs.py`
- `scripts/upload_final_docs.py`
- `scripts/export_docs_for_google.py`
- `scripts/generate_and_upload_docs.py`
- `scripts/entity_classifier.py`
- `scripts/Generatevideoparallelized.py`
- `scripts/setup_tracing_integration.py`
- `scripts/standalone_multi_video.py`
