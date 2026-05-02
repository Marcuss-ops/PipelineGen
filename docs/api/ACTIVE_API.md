# PipelineGen Active API Reference

Base URL: `http://localhost:8080` (configurable via `VELOX_PORT`)

Authentication: All `/api/` routes except health require `Authorization: Bearer <VELOX_API_TOKEN>` header.

Internal routes require additional `X-Internal: true` or `X-Velox-Internal: true` header.

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
Health check via API group.

**Response:**
```json
{
  "status": "healthy",
  "ok": true
}
```

---

## Script Docs (`/api/script-docs`)

### POST `/api/script-docs/generate`
Generate script and optionally upload to Google Docs.

**Request:**
```json
{
  "topic": "Gervonta Davis",
  "text": "Full article text...",
  "language": "it",
  "template": "default",
  "drive_folder_id": "optional_folder_id"
}
```

**Response:**
```json
{
  "ok": true,
  "script_id": 123,
  "doc_url": "https://docs.google.com/...",
  "preview_path": "/path/to/preview"
}
```

### POST `/api/script-docs/preview`
Generate local preview without Google Docs upload.

**Request:** Same as `generate`.

### POST `/api/script-docs/association-candidates`
Get clip association candidates for a script.

### GET `/api/script-docs/modes`
List available output modes.

**Response:**
```json
{
  "ok": true,
  "modes": ["default", "preview"]
}
```

---

## Script History (`/api/scripts`)

### GET `/api/scripts`
List scripts with pagination.

**Query Parameters:**
- `limit` (default: 20)
- `offset` (default: 0)
- `language`
- `template`

### GET `/api/scripts/:id`
Get script by ID.

---

## Voiceover (`/api/voiceover`)

### POST `/api/voiceover/generate`
Generate voiceover from text.

**Request:**
```json
{
  "text": "Text to synthesize",
  "language": "it",
  "filename": "output.mp3"
}
```

### POST `/api/voiceover/batch`
Batch generate voiceovers.

---

## Images (`/api/images`)

### GET `/api/images/search?q=<query>`
Search and download image for a subject.

### POST `/api/images/sync`
Sync image assets.

---

## YouTube Clips (`/api/youtube-clips`)

### POST `/api/youtube-clips/extract`
Extract YouTube clip with processing.

### GET `/api/youtube-clips/folders`
List YouTube folders.

### GET `/api/youtube-clips/folders/search?q=<query>`
Search YouTube folders.

### GET `/api/youtube-clips/folders/:id`
Get folder details.

### GET `/api/youtube-clips/folders/:id/clips`
List clips in a folder.

---

## Artlist (`/api/artlist`)

### POST `/api/artlist/run`
Run Artlist pipeline for a tag.

**Request:**
```json
{
  "tag": "boxing",
  "limit": 10
}
```

### GET `/api/artlist/runs/:run_id`
Get run status.

### GET `/api/artlist/diagnostics`
Get Artlist diagnostics.

### POST `/api/artlist/search/live`
Search Artlist (live scraper).

### Internal Routes (require X-Internal header)

- `GET /api/artlist/stats` — Get statistics
- `POST /api/artlist/search` — Search catalog
- `POST /api/artlist/sync-drive-folder` — Sync Drive folder
- `POST /api/artlist/sync-catalogs` — Sync catalogs
- `POST /api/artlist/import-scraper-db` — Import scraper DB
- `GET /api/artlist/clips/:id/status` — Get clip status
- `POST /api/artlist/clips/:id/download` — Download clip
- `POST /api/artlist/clips/:id/upload-drive` — Upload to Drive
- `POST /api/artlist/clips/process` — Process clip

---

## Scraper (`/api/scraper`)

### POST `/api/scraper/search`
Search using Node.js scraper.

**Request:**
```json
{
  "search_term": "boxing",
  "term": "boxing",
  "limit": 10,
  "save_db": false
}
```

---

## Jobs (`/api/jobs`)

### POST `/api/jobs`
Enqueue a new job.

**Request:**
```json
{
  "type": "artlist_run",
  "project": "project_name",
  "payload": {
    "tag": "boxing",
    "limit": 10
  }
}
```

### GET `/api/jobs`
List jobs with filtering.

### GET `/api/jobs/:id`
Get job details.

### POST `/api/jobs/:id/cancel`
Cancel a job.

### POST `/api/jobs/:id/retry`
Retry a failed job.

### GET `/api/jobs/:id/events`
Stream job events (Server-Sent Events).

---

## Catalog (Public)

### GET `/api/catalog/folders?q=<query>`
Search catalog folders (public endpoint).

---

## Internal Utilities (Public)

### GET `/api/internal/slug?text=<text>`
Generate URL slug from text (public, no auth required).
