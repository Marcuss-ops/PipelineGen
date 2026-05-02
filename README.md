# PipelineGen

Automated Video Content Creation Backend — Go + Python (Ollama)

Updated: May 2026

---

## Status

**Active Prototype / Internal Automation Backend**

Stable modules:
- `script-docs` — Script generation with 3-zone pipeline
- `artlist` — Artlist clip management
- `youtube-clips` — YouTube clip extraction

Experimental:
- `jobs` — Job queue management
- `catalog` — Media catalog with FTS5
- `voiceover` — Voiceover generation
- `images` — Image asset management

---

## Project Structure

```
.
├── src/go-master/              # Go API Server (PRIMARY)
│   ├── cmd/                   # Entry points (server, tools)
│   ├── internal/              # Core logic (modularized by domain)
│   │   ├── api/               # HTTP handlers
│   │   ├── core/              # Business logic
│   │   ├── catalogdb/         # Unified SQLite catalog
│   │   └── ...
│   └── pkg/                   # Shared utilities
├── docs/                       # Documentation
│   ├── api/                   # Active API documentation
│   ├── architecture/           # System design docs
│   └── archive/               # Historical documentation
├── scripts/                    # Operational scripts
└── data/                       # Databases & runtime state (set via VELOX_DATA_DIR)
```

---

## Quick Start

### Prerequisites
- Go 1.21+
- Ollama running (`gemma3:4b` model)
- Google OAuth credentials (`credentials.json` + `token.json`)
- `yt-dlp` installed

### Start Server
```bash
cd src/go-master
VELOX_DATA_DIR=/path/to/data go run ./cmd/server
```

### Health Check
```bash
curl http://localhost:8080/health
```

---

## Active API Endpoints

### Health
- `GET /health`
- `GET /api/health`

### Protected API (mounted under `/api/`)
- `/script-docs/*` — Script generation & preview
- `/scripts/*` — Script history
- `/voiceover/*` — Voiceover generation
- `/images/*` — Image asset management
- `/youtube-clips/*` — YouTube clip extraction
- `/artlist/*` — Artlist clip management
- `/scraper/*` — Node.js scraper integration
- `/jobs/*` — Job queue management
- `/catalog/folders` — Catalog search (public)

For complete API reference, see:
- `docs/api/ACTIVE_API.md`

---

## Configuration

Set via environment variables:
- `VELOX_DATA_DIR` — Database and runtime state location
- `VELOX_PORT` — Server port (default: 8080)
- `VELOX_API_TOKEN` — API authentication token
- `VELOX_INTERNAL_TOKEN` — Internal service token

---

## Testing

```bash
cd src/go-master
make test
```

---

## CI/CD

GitHub Actions validates PRs touching `src/go-master/`:
- Format & Vet
- Unit & Integration Tests
- Build verification

---

*Internal project — not production-ready yet*
