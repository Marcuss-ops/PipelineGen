# PipelineGen API Documentation (Auto-Generated)

**Status:** GENERATED - Auto-generated from live router.
**Base URL:** `http://127.0.0.1:8080`

## /api/scripts

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/scripts` | List scripts |
| GET | `/api/scripts/:id` | Get script by ID |

## /api/media

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/media/:source/folders` | List media folders |
| GET | `/api/media/:source/folders/:id/status` | Get folder status |
| GET | `/api/media/:source/clips` | List clips |
| POST | `/api/media/:source/clips/:id/reupload` | Reupload clip |
| POST | `/api/media/:source/clips/:id/reprocess` | Reprocess clip |
| POST | `/api/media/:source/clips/:id/status` | Get clip status |
| POST | `/api/media/:source/clips/:id/verify` | Verify clip |
| POST | `/api/media/:source/clips/:id/trash` | Trash clip |
| POST | `/api/media/:source/clips/:id/delete` | Delete clip |
| POST | `/api/media/:source/cleanup-orphans` | Cleanup orphaned files |
| POST | `/api/media/:source/folders/:id/regenerate-manifest` | Regenerate folder manifest |
| POST | `/api/media/:source/folders/:id/trash` | Trash folder |
| POST | `/api/media/:source/folders/:id/delete` | Delete folder |
| POST | `/api/media/:source/drive-file/trash` | Trash Drive file |
| POST | `/api/media/:source/drive-file/delete` | Delete Drive file |
| POST | `/api/media/:source/reconcile` | Reconcile media |

## /api/health

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/health` | Health check (API prefix) |

## /api/assets

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/assets/search` | Search assets |
| GET | `/api/assets/stats` | Get asset statistics |

## /api/youtube-clips

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/youtube-clips/folders` | List folders |
| GET | `/api/youtube-clips/folders/search` | Search folders |
| GET | `/api/youtube-clips/folders/:id` | Get folder details |
| GET | `/api/youtube-clips/folders/:id/clips` | Get folder clips |
| POST | `/api/youtube-clips/extract` | Extract YouTube clip |

## /api/system

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/system/doctor` | System diagnostics |

## /api/voiceover

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/voiceover/generate` | Generate voiceover |
| POST | `/api/voiceover/batch` | Batch generate voiceovers |

## /api/jobs

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/jobs` | List jobs or enqueue new job |
| GET | `/api/jobs/:id` | Get job by ID |
| GET | `/api/jobs/:id/full` | Get full job details |
| GET | `/api/jobs/:id/events` | Get job events |
| POST | `/api/jobs` | List jobs or enqueue new job |
| POST | `/api/jobs/:id/cancel` | Cancel a job |
| POST | `/api/jobs/:id/retry` | Retry a failed job |
| POST | `/api/jobs/:id/action` | Perform action on job |

## /

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |

## /api/scraper

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/scraper/search` | Search using scraper |

## /api/artlist

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/artlist/runs/:run_id` | Get run status by ID |
| GET | `/api/artlist/stats` | Get Artlist statistics |
| GET | `/api/artlist/diagnostics` | Check system diagnostics |
| POST | `/api/artlist/search` | Search Artlist catalog |
| POST | `/api/artlist/search/live` | Perform live Artlist search |
| POST | `/api/artlist/sync-catalogs` | Sync catalogs |
| POST | `/api/artlist/run` | Start Artlist pipeline for a term |
| POST | `/api/artlist/run-smart` | Start Artlist pipeline (smart mode) |

## /api/script-docs

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/script-docs/modes` | Get script generation modes |
| POST | `/api/script-docs/generate` | Generate script |
| POST | `/api/script-docs/preview` | Preview script generation |
| POST | `/api/script-docs/association-candidates` | Get association candidates |

## /api/images

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/images/search` | Search images |
| POST | `/api/images/sync` | Sync images |

## /api/internal

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/internal/slug` | Generate URL slug from text |

## /assets/*filepath

| Method | Path | Description |
|--------|------|-------------|
| GET | `/assets/*filepath` | endpoint |
| HEAD | `/assets/*filepath` | endpoint |

