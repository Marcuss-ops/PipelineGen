# PipelineGen Active API Documentation

**Status:** ACTIVE - This is the single source of truth for API documentation.
**Last Updated:** 2026-05-03
**Base URL:** `http://127.0.0.1:8080`

## Authentication

Most API endpoints require authentication via one of these methods:
- **Header:** `Authorization: Bearer <token>` (where token is `VELOX_ADMIN_TOKEN` or `VELOX_API_TOKEN`)
- **Header:** `X-Velox-Admin-Token: <token>`
- **Feature Flag:** Set `VELOX_ENABLE_AUTH=false` to disable auth (development only)

Internal endpoints require additional header: `X-Internal: true` or `X-Velox-Internal: true`.

## Health Endpoints (Public)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| GET | `/api/health` | Health check (API prefix) |

## Public Endpoints (No Auth)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/internal/slug` | Generate URL slug from text (query param: `text`) |
| GET | `/api/catalog/folders` | Search folders in catalog |

## Artlist Endpoints

**Group:** `/api/artlist` (requires `ARTLIST_ENABLED=true`)

### Public Artlist Endpoints

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/artlist/run` | Start Artlist pipeline for a term |
| GET | `/api/artlist/runs/:run_id` | Get run status by ID |
| GET | `/api/artlist/diagnostics` | Check system diagnostics |
| POST | `/api/artlist/search/live` | Perform live Artlist search |

### Internal Artlist Endpoints (requires `X-Internal: true`)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/artlist/stats` | Get Artlist statistics |
| POST | `/api/artlist/search` | Search Artlist catalog |
| POST | `/api/artlist/sync-drive-folder` | Sync Drive folder |
| POST | `/api/artlist/sync-catalogs` | Sync catalogs |
| POST | `/api/artlist/import-scraper-db` | Import scraper database |
| GET | `/api/artlist/clips/:id/status` | Get clip status |
| POST | `/api/artlist/clips/:id/download` | Download clip |
| POST | `/api/artlist/clips/:id/upload-drive` | Upload clip to Drive |
| POST | `/api/artlist/clips/process` | Process clip |

## Jobs Endpoints

**Group:** `/api/jobs`

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/jobs` | Enqueue a new job |
| GET | `/api/jobs` | List jobs (query params: `status`, `type`, `worker_id`, `limit`, `offset`) |
| GET | `/api/jobs/:id` | Get job by ID |
| POST | `/api/jobs/:id/cancel` | Cancel a job |
| POST | `/api/jobs/:id/retry` | Retry a failed job |
| GET | `/api/jobs/:id/events` | Get job events |

### Job Types

- `media.artlist` - Artlist pipeline job
- `media.youtube_clip` - YouTube clip extraction job
- `media.voiceover` - Voiceover generation job
- `media.voiceover_sync` - Voiceover sync job
- `media.script_generate` - Script generation job

### Job Statuses

- `pending` - Job is pending
- `queued` - Job is queued
- `processing` / `running` - Job is running
- `completed` - Job completed successfully
- `failed` - Job failed
- `paused` - Job paused
- `cancelled` - Job cancelled
- `zombie` - Job timed out
- `retrying` - Job is being retried

## YouTube Clips Endpoints

**Group:** `/api/clips` (requires `YOUTUBE_ENABLED=true`)

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/clips/process` | Download and process YouTube clips |
| GET | `/api/clips/info` | Get YouTube video metadata |

## Voiceover Endpoints

**Group:** `/api/voiceover`

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/voiceover/generate` | Generate voiceover |
| POST | `/api/voiceover/batch` | Batch generate voiceovers |
| POST | `/api/voiceover/sync` | Sync voiceovers from Drive |

Note: `/api/voiceover/sync/status` was removed - was returning fake status (debt).

## Script Docs Endpoints

**Group:** `/api/script-docs` (requires `SCRIPT_DOCS_ENABLED=true`)

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/script-docs/generate` | Generate script |
| POST | `/api/script-docs/preview` | Preview script generation |

## Script History Endpoints

**Group:** `/api/scripts` (requires `SCRIPT_CLIPS_ENABLED=true`)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/scripts` | List scripts |
| GET | `/api/scripts/:id` | Get script by ID |
| DELETE | `/api/scripts/:id` | Delete script |

## Images Endpoints

**Group:** `/api/images`

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/images/search` | Search images |
| POST | `/api/images/sync` | Sync images |

## Media Endpoints

**Group:** `/api/media/:source` (where `:source` is `stock`, `youtube`, `artlist`, or `images`)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/media/:source/clips` | List clips for a source |
| GET | `/api/media/:source/clips/:id` | Get clip details |
| GET | `/api/media/:source/clips/:id/download` | **Stream/Proxy clip video file** (Local -> Drive fallback) |
| POST | `/api/media/:source/clips/:id/verify` | Verify clip integrity |
| POST | `/api/media/:source/clips/:id/reprocess` | Reprocess clip (normalize, upload to drive) |
| POST | `/api/media/:source/clips/:id/reupload` | Force re-upload to Drive |
| POST | `/api/media/:source/clips/:id/trash` | Move clip to Drive trash and remove from DB |
| POST | `/api/media/:source/clips/:id/delete` | Permanently delete clip from Drive and DB |
| GET | `/api/media/:source/clips/:id/duplicates` | Find potential duplicates by hash |
| GET | `/api/media/:source/folders` | List folders/categories for source |
| POST | `/api/media/:source/cleanup-orphans` | Cleanup orphaned files (diagnostics) |
| GET | `/api/media/manifest/export` | Export global media manifest |

## Admin Endpoints

**Group:** `/api/admin/:source` (Requires Admin Token)

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/admin/:source/clips` | Create a new clip record |
| PATCH | `/api/admin/:source/clips/:id` | Update an existing clip record |

## Scraper Endpoints

**Group:** `/api/scraper`

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/scraper/search` | Search using scraper |

## Static Assets

| Path | Description |
|------|-------------|
| `/assets/*filepath` | Static assets (images, etc.) served from data directory |

## Rate Limiting

All protected endpoints are subject to rate limiting. Default: 60 requests per minute per IP.

## Workspace Scope

All protected endpoints include workspace scope middleware that validates the `X-Workspace-ID` header when present.

## Error Responses

All endpoints return JSON error responses in the format:

```json
{
  "error": "error message",
  "code": "optional_error_code"
}
```

HTTP status codes:
- `200` - Success
- `400` - Bad Request
- `401` - Unauthorized
- `403` - Forbidden
- `404` - Not Found
- `409` - Conflict (e.g., duplicate job)
- `429` - Rate Limited
- `500` - Internal Server Error
- `202` - Accepted (for async jobs)

## Notes

- The `artlist_runs` table is **deprecated**. All Artlist runs now use the `jobs` table.
- Job system is the **only source of truth** for async operations.
- All timestamps are in RFC3339 format (UTC).
