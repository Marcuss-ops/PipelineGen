# PipelineGen API Documentation

## Overview
Minimal Go-based backend for script generation, voiceover, image/assets management, and YouTube clip processing.

**Base URL:** `http://localhost:8080` (configurable via `VELOX_PORT`)

---

## Authentication
Most endpoints require authentication via:
- Header `Authorization: Bearer <token>` OR
- Header `X-Velox-Admin-Token: <token>`

Set token via `VELOX_ADMIN_TOKEN` env var. Auth is disabled by default (enable via `VELOX_ENABLE_AUTH=true`).

Internal endpoints additionally require `X-Internal: true` or `X-Velox-Internal: true` header.

---

## Health & System

### GET `/health`
Public health check.

**Response:**
```json
{
  "status": "healthy",
  "ok": true
}
```

### GET `/api/health`
Alias for `/health`.

### GET `/api/internal/slug`
Generate URL slug from text (public, no auth).
- Query param: `text` (required)

### GET `/api/catalog/folders`
Search Drive folders (public, no auth).
- Query params: `q` (search query), `pageSize` (default 20)

---

## Protected Endpoints (Auth + Rate Limit)

### Script Docs (`/api/script-docs`)
- `POST /api/script-docs/generate` - Generate full script document
- `POST /api/script-docs/preview` - Generate script preview
- `POST /api/script-docs/association-candidates` - Get clip association candidates
- `GET /api/script-docs/modes` - List available script generation modes

### Script History (`/api/scripts`)
- `GET /api/scripts` - List generated scripts
- `GET /api/scripts/:id` - Get script by ID

### Voiceover (`/api/voiceover`)
- `POST /api/voiceover/generate` - Generate voiceover audio
- `POST /api/voiceover/batch` - Batch generate voiceovers

### Images (`/api/images`)
- `GET /api/images/search` - Search image assets
- `POST /api/images/sync` - Sync image assets from Drive

### YouTube Clips (`/api/youtube-clips`)
- `POST /api/youtube-clips/extract` - Extract YouTube clip metadata
- `GET /api/youtube-clips/folders` - List clip folders
- `GET /api/youtube-clips/folders/:id` - Get folder details
- `GET /api/youtube-clips/folders/:id/clips` - List clips in folder
- `GET /api/youtube-clips/folders/search` - Search clip folders

### Artlist (`/api/artlist`)
Public paths (auth required):
- `POST /api/artlist/run` - Run tag pipeline
- `GET /api/artlist/runs/:run_id` - Get pipeline run status
- `GET /api/artlist/diagnostics` - Get diagnostics
- `POST /api/artlist/search/live` - Live search

Internal paths (auth + `X-Internal: true`):
- `GET /api/artlist/internal/stats` - Get stats
- `POST /api/artlist/internal/search` - Search clips
- `POST /api/artlist/internal/sync-drive-folder` - Sync Drive folder
- `POST /api/artlist/internal/sync-catalogs` - Sync catalogs
- `POST /api/artlist/internal/import-scraper-db` - Import scraper DB
- `GET /api/artlist/internal/clips/:id/status` - Get clip status
- `POST /api/artlist/internal/clips/:id/download` - Download clip
- `POST /api/artlist/internal/clips/:id/upload-drive` - Upload clip to Drive
- `POST /api/artlist/internal/clips/process` - Process clip

### Scraper (`/api/scraper`)
- `POST /api/scraper/search` - Run Node.js scraper search

### Jobs (`/api/jobs`)
- `POST /api/jobs` - Enqueue new job
- `GET /api/jobs` - List jobs
- `GET /api/jobs/:id` - Get job details
- `POST /api/jobs/:id/cancel` - Cancel job
- `POST /api/jobs/:id/retry` - Retry failed job
- `GET /api/jobs/:id/events` - Stream job events (SSE)

---

## Environment Variables

### Server
- `VELOX_PORT` - Server port (default 8080)
- `VELOX_HOST` - Server host (default 127.0.0.1)

### Auth
- `VELOX_ENABLE_AUTH` - Enable auth (default false)
- `VELOX_ADMIN_TOKEN` - Admin token for auth

### Background Jobs
- `VELOX_ENABLE_BACKGROUND_JOBS` - Enable background cron jobs (default true)
- `VELOX_ENABLE_CHANNEL_MONITOR` - Enable YouTube channel monitor
- `VELOX_ENABLE_STOCK_SCHEDULER` - Enable stock scheduler
- `VELOX_ENABLE_TEST_JOB_HANDLERS` - Register test job handlers (default false)

### External Services
- `OLLAMA_ADDR` - Ollama API URL (default http://localhost:11434)
- `VELOX_CREDENTIALS_FILE` - Google credentials JSON path
- `VELOX_TOKEN_FILE` - Google token JSON path

---

## Notes
- All POST endpoints accept JSON bodies
- Rate limit: 100 requests/minute (configurable via `VELOX_RATE_LIMIT_REQUESTS`)
- CORS: Configurable via `VELOX_CORS_ORIGINS` (allows all by default)
