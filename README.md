# VeloxEditing Backend — Production

> **Automated Video Content Creation Backend**
> **Stack:** Go + Rust + Python (Ollama)
> **Updated:** April 14, 2026

---

## 🎯 Project Structure

```
refactored/
├── src/                         # Source code
│   ├── go-master/              # Go API Server (PRIMARY)
│   │   ├── cmd/server/         # Entry point (42 lines, DI pattern)
│   │   ├── internal/           # Core logic (50+ packages)
│   │   │   ├── api/            # HTTP handlers (48 files) + middleware + routes
│   │   │   ├── di/             # Dependency injection container
│   │   │   ├── core/           # Job, worker, entity services
│   │   │   ├── service/        # Business services (7 sub-packages)
│   │   │   │   ├── scriptdocs/     # Script + entities + Google Docs pipeline
│   │   │   │   ├── scriptclips/    # Script → clip download → Drive upload
│   │   │   │   ├── stockorchestrator/  # YouTube → download → Drive
│   │   │   │   ├── asyncpipeline/    # Background job processing
│   │   │   │   ├── channelmonitor/   # YouTube channel monitoring cron
│   │   │   │   ├── pipeline/         # Video creation pipeline
│   │   │   │   └── maintenance/      # Zombie job cleanup
│   │   │   ├── storage/        # JSON file + SQLite storage
│   │   │   ├── upload/         # Google Drive + YouTube upload
│   │   │   ├── ml/ollama/      # Ollama AI integration
│   │   │   ├── nvidia/         # NVIDIA AI client
│   │   │   ├── gpu/            # GPU detection
│   │   │   ├── clip/           # Clip indexing, semantic matching
│   │   │   ├── stock/          # Stock video management
│   │   │   ├── youtube/        # YouTube client (yt-dlp)
│   │   │   └── ...             # 30+ more packages
│   │   ├── pkg/                # Shared models, config, logger
│   │   ├── data/               # JSON database (runtime)
│   │   ├── tests/              # Unit & integration tests
│   │   └── Makefile            # Build & test commands
│   ├── rust/                   # Rust video processing (Cargo project)
│   │   └── src/main.rs         # FFmpeg pipeline, transitions, effects
│   └── python/                 # Ollama text generation (script + transcript)
│
├── bin/                         # Compiled binaries
│   ├── server                  # Go server binary
│   └── video-stock-creator.bundle  # Rust binary
│
├── scripts/                     # Utility scripts
│   ├── generate_script_from_text.py  # Script generation via Go API
│   └── generate_gervonta_script.sh   # Gervonta test script
│
├── docs/                        # Documentation (32 files)
│   ├── API_DOCUMENTATION.md     # Full API docs
│   ├── API_ENDPOINTS.md         # Endpoint listing
│   ├── ARCHITETTURA_BACKEND.md  # Architecture overview
│   └── ...
│
├── src/node-scraper/            # Node.js Artlist scraper
│   ├── artlist_videos.db        # SQLite DB with Artlist clip URLs
│   └── package.json
│
├── data/                        # JSON database + output artifacts
│   ├── queue.json               # Job queue
│   ├── workers.json             # Worker registry
│   ├── artlist_stock_index.json # Artlist clip index (25 clips)
│   └── ...
│
├── assets/                      # Shared assets (audio, fonts, transitions)
├── effects/                     # Video effect definitions
├── config/                      # Configuration files
├── tests/                       # Test environments (lightpanda/, stock-links/)
├── requirements.txt             # Python dependencies (Ollama + requests)
└── .env                         # Environment variables (NVIDIA, etc.)
```

---

## 🚀 Quick Start

### Prerequisites
- Go 1.18+ (1.21+ recommended)
- Rust toolchain (for video processor)
- Ollama running (`gemma3:4b` model)
- Google OAuth credentials (Drive + YouTube)
- yt-dlp installed

### Start Go Master Server (Port 8080)
```bash
cd src/go-master
go build -o ../../bin/server ./cmd/server
../../bin/server

# Or directly:
cd src/go-master
go run cmd/server/main.go
```

### Health Check
```bash
curl http://localhost:8080/health
```

---

## 📡 API Endpoints

### Core Video
| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/video/create-master` | Main video creation entry point |
| POST | `/api/video/generate` | Generate video via Rust binary |
| POST | `/api/video/process` | Process existing video |
| GET | `/api/video/status/:id` | Get processing status |

### Script & Docs
| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/script/generate` | Generate script via Ollama |
| POST | `/api/script-docs/generate` | Script + entities + clip association → Google Docs |

### Script + Clips Pipeline
| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/script-clips/generate` | Script → entity extraction → clip download → Drive upload |
| POST | `/api/script-clips/from-clips` | Generate script FROM available Drive/Artlist clips |
| POST | `/api/script-clips/async` | Async pipeline with job polling |
| GET | `/api/script-clips/async/:id` | Get async job status |

### Stock & Clip
| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/stock/create` | Create stock clip |
| POST | `/api/stock/batch-create` | Batch stock creation |
| POST | `/api/stock/orchestrator` | Full stock pipeline: YouTube → download → Drive |
| POST | `/api/clip/suggest` | Get clip suggestions |
| POST | `/api/clip/search-folders` | Search clip folders |
| POST | `/api/clip/index/scan` | Trigger Drive index scan |
| POST | `/api/clip/approve` | AI-powered clip approval |

### Channel Monitor
| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/channel-monitor/status` | Monitor status |
| POST | `/api/channel-monitor/run` | Trigger manual monitoring cycle |
| POST | `/api/channel-monitor/download-single` | Download single video |

### YouTube
| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/youtube/v2/search` | YouTube v2 search |
| POST | `/api/youtube/v2/metadata` | Get video metadata |
| POST | `/api/youtube/v2/transcript` | Extract transcript |

### Job & Worker Management
| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/jobs/create` | Create new job |
| GET | `/api/jobs/:id` | Get job details |
| GET | `/api/jobs` | List jobs |
| POST | `/api/jobs/:id/complete` | Complete job (worker) |
| POST | `/api/jobs/:id/cancel` | Cancel job |
| GET | `/api/workers` | List workers |
| POST | `/api/workers/register` | Register worker |
| POST | `/api/workers/heartbeat` | Heartbeat from worker |

### Voiceover & NLP
| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/voiceover/generate` | Generate voiceover (EdgeTTS) |
| POST | `/api/nlp/extract-entities` | Extract entities from text |

### Drive & Upload
| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/drive/folders` | List Drive folders |
| POST | `/api/drive/upload` | Upload file to Drive |
| GET | `/api/drive/token/refresh` | Refresh OAuth token |

### GPU & AI
| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/gpu/status` | GPU status |
| POST | `/api/gpu/textgen` | AI text generation with GPU |
| POST | `/api/nvidia/verify` | NVIDIA AI clip verification |

### Timestamp Mapping
| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/timestamp/map-clips` | Map text segments to clips with timestamps |

### Health & Docs
| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/health` | Health check |
| GET | `/api/docs/*` | Swagger UI |
| GET | `/api/metrics` | Prometheus metrics |
| GET | `/api/stats` | Server statistics |
| GET | `/api/dashboard` | Admin dashboard |

---

## 🏗️ Architecture

```
┌─────────────────────────────────────────────────────┐
│  GO MASTER (Port 8080)                              │
│  ├─ API HTTP (Gin, 60+ endpoints)                   │
│  ├─ DI Container (internal/di/)                     │
│  ├─ Job / Worker Management                         │
│  ├─ Script Generation (Ollama)                      │
│  ├─ Entity Extraction (Ollama + NLP)               │
│  ├─ Voiceover (EdgeTTS)                             │
│  ├─ Clip Indexing (Drive scanning + semantic match) │
│  ├─ Stock Orchestrator (YouTube → download → Drive) │
│  ├─ Channel Monitor (cron, AI folder classification)│
│  ├─ Google Drive / YouTube Upload                   │
│  └─ GPU / NVIDIA AI Integration                     │
└─────────────────────┬───────────────────────────────┘
                      │
                      ▼ Calls
┌─────────────────────────────────────────────────────┐
│  RUST BINARY (video-stock-creator)                  │
│  ├─ FFmpeg video assembly                           │
│  ├─ Transitions & effects                           │
│  └─ Audio mixing                                    │
└─────────────────────────────────────────────────────┘
```

---

## 🧪 Testing

```bash
# Go tests
cd src/go-master
go test ./... -v

# With race detection
go test -race ./...

# Coverage report
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# Health check
curl http://localhost:8080/health
```

### Makefile Targets (in src/go-master/)
```bash
make build          # Compile
make test           # Run all tests
make test-unit      # Unit tests with race detection
make test-integration  # Integration tests
make coverage       # HTML coverage report
make lint           # golangci-lint
make fmt            # go fmt
make vet            # go vet
make swagger        # Swagger docs
make clean          # Clean build artifacts
make deps           # Download dependencies
```

---

## ⚙️ Configuration

### Environment Variables
| Variable | Default | Description |
|----------|---------|-------------|
| `VELOX_HOST` | `0.0.0.0` | Server bind address |
| `VELOX_PORT` | `8080` | Server port |
| `VELOX_LOG_LEVEL` | `info` | Log level |
| `VELOX_DATA_DIR` | `./data` | Data directory |
| `VELOX_ENABLE_AUTH` | `false` | Enable auth middleware |
| `VELOX_ADMIN_TOKEN` | `` | Admin API token |
| `NVIDIA_API_KEY` | `` | NVIDIA AI API key |
| `NVIDIA_BASE_URL` | `https://integrate.api.nvidia.com/v1` | NVIDIA endpoint |

### OAuth Setup
Place `credentials.json` and `token.json` in `src/go-master/` or set `VELOX_CREDENTIALS_PATH` / `VELOX_TOKEN_PATH`.

---

## ⚠️ Notes

- **Go Master** is the only required component
- **Go Worker** is optional (Master executes jobs synchronously)
- **Rust Binary** must be available in PATH
- **Python files** in `src/python/` are for Ollama text generation only (script + transcript)
- **Ollama** must be running with `gemma3:4b` model loaded
- **yt-dlp** must be installed for YouTube operations

---

*Production Ready — Updated April 14, 2026*
