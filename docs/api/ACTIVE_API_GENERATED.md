# PipelineGen API Documentation (Auto-Generated)

**Status:** GENERATED - Auto-generated from live router.
**Base URL:** `http://127.0.0.1:8080`

## /api/channels

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/channels` | endpoint |
| GET | `/api/channels/categories` | endpoint |
| GET | `/api/channels/:id` | endpoint |
| POST | `/api/channels` | endpoint |
| POST | `/api/channels/bulk-upsert` | endpoint |
| DELETE | `/api/channels/:id` | endpoint |

## /api/health

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/health` | Health check (API prefix) |
| GET | `/api/health/deep` | endpoint |
| GET | `/api/health/ollama-timeout` | endpoint |

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
| POST | `/api/artlist/recommend` | endpoint |

## /

| Method | Path | Description |
|--------|------|-------------|
| GET | `/` | endpoint |
| GET | `/health` | Health check |
| GET | `/metrics` | endpoint |

## /api/media

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/media/search` | endpoint |
| GET | `/api/media/semantic-search` | endpoint |
| GET | `/api/media/voiceover/groups` | endpoint |
| GET | `/api/media/diagnostics` | endpoint |
| GET | `/api/media/index-health` | endpoint |
| GET | `/api/media/qdrant/health` | endpoint |
| GET | `/api/media/:source/clips` | List clips |
| GET | `/api/media/:source/clips/:id` | endpoint |
| GET | `/api/media/:source/clips/:id/duplicates` | endpoint |
| GET | `/api/media/:source/clips/:id/download` | endpoint |
| GET | `/api/media/:source/folders` | List media folders |
| GET | `/api/media/:source/folders/:id` | endpoint |
| GET | `/api/media/:source/folders/:id/children` | endpoint |
| GET | `/api/media/:source/tree` | endpoint |
| GET | `/api/media/:source/breadcrumb` | endpoint |
| POST | `/api/media/voiceover/generate` | Generate voiceover |
| POST | `/api/media/voiceover/generate-with-group` | endpoint |
| POST | `/api/media/voiceover/batch` | Batch generate voiceovers |
| POST | `/api/media/voiceover/promo` | endpoint |
| POST | `/api/media/voiceover/sync` | endpoint |
| POST | `/api/media/register-from-youtube` | endpoint |
| POST | `/api/media/register-batch` | endpoint |
| POST | `/api/media/recommend` | endpoint |
| POST | `/api/media/rename-drive-file` | endpoint |
| POST | `/api/media/search/advanced` | endpoint |
| POST | `/api/media/sound_effect/generate` | endpoint |
| POST | `/api/media/sync-drive-folder` | endpoint |
| POST | `/api/media/drive/move-files` | endpoint |
| POST | `/api/media/drive/create-folders` | endpoint |
| POST | `/api/media/drive/sync-to-subfolders` | endpoint |
| POST | `/api/media/enrich` | endpoint |
| POST | `/api/media/enrich/batch` | endpoint |
| POST | `/api/media/upload-video` | endpoint |
| POST | `/api/media/bulk-upload-youtube-clips` | endpoint |
| POST | `/api/media/qdrant/cleanup` | endpoint |
| POST | `/api/media/local-to-drive` | endpoint |
| POST | `/api/media/:source/clips` | List clips |
| POST | `/api/media/:source/clips/:id/reupload` | Reupload clip |
| POST | `/api/media/:source/clips/:id/reprocess` | Reprocess clip |
| POST | `/api/media/:source/clips/:id/reindex` | endpoint |
| POST | `/api/media/:source/clips/:id/status` | Get clip status |
| POST | `/api/media/:source/clips/:id/verify` | Verify clip |
| POST | `/api/media/:source/clips/:id/trash` | Trash clip |
| POST | `/api/media/:source/clips/:id/delete` | Delete clip |
| POST | `/api/media/:source/cleanup` | endpoint |
| POST | `/api/media/:source/folders/:id/manifest` | endpoint |
| POST | `/api/media/:source/folders/:id/trash` | Trash folder |
| POST | `/api/media/:source/folders/:id/delete` | Delete folder |
| POST | `/api/media/:source/bulk/tags/add` | endpoint |
| POST | `/api/media/:source/bulk/tags/remove` | endpoint |
| POST | `/api/media/:source/reconcile` | Reconcile media |
| PATCH | `/api/media/:source/clips/:id` | endpoint |

## /api/jobs

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/jobs` | List jobs or enqueue new job |
| GET | `/api/jobs/stats` | Get job by ID |
| GET | `/api/jobs/:id` | Get job by ID |
| GET | `/api/jobs/:id/full` | Get full job details |
| GET | `/api/jobs/:id/events` | Get job events |
| POST | `/api/jobs` | List jobs or enqueue new job |
| POST | `/api/jobs/:id/cancel` | Cancel a job |
| POST | `/api/jobs/:id/retry` | Retry a failed job |

## /api/scraper

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/scraper/search` | Search using scraper |

## /assets/*filepath

| Method | Path | Description |
|--------|------|-------------|
| GET | `/assets/*filepath` | endpoint |
| HEAD | `/assets/*filepath` | endpoint |

## /api/clips

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/clips/search` | Search and rank YouTube videos by topic (GET/POST) |
| GET | `/api/clips/stats` | endpoint |
| GET | `/api/clips/info` | Get YouTube video metadata |
| GET | `/api/clips/diagnostics` | endpoint |
| POST | `/api/clips/process` | Download and process clips |
| POST | `/api/clips/search` | Search and rank YouTube videos by topic (GET/POST) |

## /api/internal

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/internal/slug` | Generate URL slug from text |

## /api/fullimages

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/fullimages/video/generate` | endpoint |

## /api/search-queries

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/search-queries` | endpoint |
| GET | `/api/search-queries/active` | endpoint |
| GET | `/api/search-queries/:id` | endpoint |
| GET | `/api/search-queries/:id/results` | endpoint |
| POST | `/api/search-queries` | endpoint |
| DELETE | `/api/search-queries/:id` | endpoint |

## /api/images

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/images/search` | Search images |
| GET | `/api/images/diagnostics` | endpoint |
| POST | `/api/images/upload` | endpoint |
| POST | `/api/images/sync` | Sync images |
| POST | `/api/images/generate` | endpoint |
| POST | `/api/images/animate` | endpoint |
| POST | `/api/images/webhook/remote` | endpoint |

## /api/script

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/script/jobs/:job_id` | endpoint |
| GET | `/api/script/jobs/:job_id/full` | endpoint |
| GET | `/api/script/generate-batch/progress` | endpoint |
| POST | `/api/script/generate-from-clips` | endpoint |
| POST | `/api/script/generate-from-catalog` | endpoint |
| POST | `/api/script/generate-batch` | endpoint |
| POST | `/api/script/generate-with-images` | endpoint |
| POST | `/api/script/curate` | endpoint |
| POST | `/api/script/cache/evict` | endpoint |
| POST | `/api/script/:id/sections/:section_id/regenerate` | endpoint |

## /api/scripts

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/scripts` | List scripts |
| GET | `/api/scripts/:id` | Get script by ID |

## /api/system

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/system/doctor` | System diagnostics |

