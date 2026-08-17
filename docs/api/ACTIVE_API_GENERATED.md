# PipelineGen API Documentation (Auto-Generated)

**Status:** GENERATED — auto-generated from live router.
**Base URL:** `{BASE_URL}` (overridable via `VELOX_PORT` env var)

## /

| Method | Path | Description |
|--------|------|-------------|
| GET | `/` | API root (redirects or 404) |
| GET | `/health` | Unified health check (?deep=true for component checks) |
| GET | `/metrics` | Prometheus metrics endpoint |
| GET | `/models` | ⚠️ MISSING DESCRIPTION |
| GET | `/ready` | Readiness probe |

## /admin/*filepath

| Method | Path | Description |
|--------|------|-------------|
| GET | `/admin/*filepath` | ⚠️ MISSING DESCRIPTION |
| HEAD | `/admin/*filepath` | ⚠️ MISSING DESCRIPTION |

## /api/admin

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/admin/auth/me` | ⚠️ MISSING DESCRIPTION |
| GET | `/api/admin/entities` | ⚠️ MISSING DESCRIPTION |
| GET | `/api/admin/entities/:entity` | ⚠️ MISSING DESCRIPTION |
| GET | `/api/admin/entities/:entity/:id` | ⚠️ MISSING DESCRIPTION |
| GET | `/api/admin/entities/:entity/schema` | ⚠️ MISSING DESCRIPTION |
| PATCH | `/api/admin/entities/:entity/:id` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/admin/auth/login` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/admin/auth/logout` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/admin/entities/:entity/:id/actions/:action` | ⚠️ MISSING DESCRIPTION |

## /api/artlist

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/artlist/diagnostics` | Artlist diagnostics |
| GET | `/api/artlist/job-consumer` | ⚠️ MISSING DESCRIPTION |
| GET | `/api/artlist/runs/:run_id` | Get Artlist pipeline run status |
| GET | `/api/artlist/search/live` | Search Artlist catalog (live, no cache) |
| GET | `/api/artlist/stats` | Get Artlist statistics |
| POST | `/api/artlist/import` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/artlist/recommend` | Get Artlist recommendations for a term |
| POST | `/api/artlist/run` | Start Artlist pipeline for a term |
| POST | `/api/artlist/search` | Search Artlist catalog (cached) |
| POST | `/api/artlist/sync-catalogs` | Sync Artlist catalogs to media DB |

## /api/assets

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/assets/operator/assets` | ⚠️ MISSING DESCRIPTION |
| GET | `/api/assets/operator/assets/:id` | ⚠️ MISSING DESCRIPTION |
| GET | `/api/assets/operator/assets/:id/preview` | ⚠️ MISSING DESCRIPTION |
| GET | `/api/assets/operator/facets` | ⚠️ MISSING DESCRIPTION |
| GET | `/api/assets/operator/index-health` | ⚠️ MISSING DESCRIPTION |
| GET | `/api/assets/operator/operations/errors` | ⚠️ MISSING DESCRIPTION |
| GET | `/api/assets/operator/outbox/events` | ⚠️ MISSING DESCRIPTION |
| GET | `/api/assets/operator/outbox/status` | ⚠️ MISSING DESCRIPTION |
| GET | `/api/assets/operator/summary` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/assets/operator/assets/:id/reindex` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/assets/operator/assets/:id/verify-index` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/assets/operator/bulk` | ⚠️ MISSING DESCRIPTION |

## /api/capabilities

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/capabilities` | ⚠️ MISSING DESCRIPTION |

## /api/clips

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/clips/diagnostics` | Clips diagnostics |
| GET | `/api/clips/info` | Get YouTube video metadata |
| GET | `/api/clips/search` | Search and rank YouTube videos by topic |
| POST | `/api/clips/process` | Download and process clips |
| POST | `/api/clips/stock` | ⚠️ MISSING DESCRIPTION |

## /api/drive

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/drive/files` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/drive/canary-upload` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/drive/cleanup` | Clean up empty Drive folders |
| POST | `/api/drive/folders` | List Drive folders |
| POST | `/api/drive/move` | Move Drive files |
| POST | `/api/drive/reconcile` | Reconcile Drive metadata |
| POST | `/api/drive/rename` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/drive/resolve-by-id` | Resolve Drive folder by ID |

## /api/history

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/history` | ⚠️ MISSING DESCRIPTION |

## /api/images

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/images/diagnostics` | Images diagnostics |
| GET | `/api/images/generated/search` | Search generated images |
| GET | `/api/images/generated/styles` | List generated image styles |
| GET | `/api/images/retrieved/search` | Search retrieved images |
| GET | `/api/images/search` | Search images by territory |
| POST | `/api/images/batch-generate` | Batch generate AI images asynchronously (items or mode=sections) |
| POST | `/api/images/generated/generate` | Generate an AI image |
| POST | `/api/images/sync` | Sync images to Drive |
| POST | `/api/images/upload` | Upload an image |

## /api/jobs

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/jobs` | List jobs |
| GET | `/api/jobs/:id` | Get job by ID |
| GET | `/api/jobs/:id/events` | Get job event stream |
| GET | `/api/jobs/:id/full` | Get full job details |
| GET | `/api/jobs/stats` | Get job statistics |
| POST | `/api/jobs` | Enqueue a new job |
| POST | `/api/jobs/:id/cancel` | Cancel a job |
| POST | `/api/jobs/:id/retry` | Retry a failed job |

## /api/media

| Method | Path | Description |
|--------|------|-------------|
| DELETE | `/api/media/clips/:source/clips/:id` | Trash clip |
| DELETE | `/api/media/clips/:source/folders/:id` | Trash folder |
| GET | `/api/media/clips/:source/breadcrumb` | Get breadcrumb path to folder |
| GET | `/api/media/clips/:source/clips` | List clips by source |
| GET | `/api/media/clips/:source/clips/:id` | Get clip by ID |
| GET | `/api/media/clips/:source/folders` | List media folders by source |
| GET | `/api/media/clips/:source/folders/:id` | Get media folder by ID |
| GET | `/api/media/clips/:source/folders/:id/children` | List child folders |
| GET | `/api/media/clips/:source/tree` | Get folder tree by source |
| GET | `/api/media/diagnostics` | Media diagnostics |
| GET | `/api/media/index-health` | Media index health check |
| PATCH | `/api/media/clips/:source/clips/:id` | Update clip metadata |
| POST | `/api/media/clips/:source/cleanup` | Clean up source artifacts |
| POST | `/api/media/clips/:source/clips` | Create clip under source |
| POST | `/api/media/clips/:source/clips/:id/download` | Download clip |
| POST | `/api/media/clips/:source/clips/:id/duplicates` | Find duplicate clips |
| POST | `/api/media/clips/:source/clips/:id/fix-hash` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/media/clips/:source/clips/:id/reprocess` | Re-process clip |
| POST | `/api/media/clips/:source/clips/:id/reupload` | Re-upload clip to Drive |
| POST | `/api/media/clips/:source/clips/:id/status` | Get clip processing status |
| POST | `/api/media/clips/:source/clips/:id/verify` | Verify clip integrity |
| POST | `/api/media/clips/:source/clips/bulk-upload-youtube-clips` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/media/clips/:source/folders/:id/manifest` | Get folder manifest |
| POST | `/api/media/clips/:source/reconcile` | Reconcile source metadata |
| POST | `/api/media/clips/enrich` | Enrich a media asset with AI metadata |
| POST | `/api/media/clips/ingest/ai-stock` | Ingest an AI-generated stock clip from visual analysis + Drive video |
| POST | `/api/media/clips/upload-video` | Upload video clip |
| POST | `/api/media/qdrant/cleanup` | Clean up stale Qdrant points |
| POST | `/api/media/register-batch` | Batch register assets |
| POST | `/api/media/register-from-youtube` | Register asset from YouTube URL |
| POST | `/api/media/resolve/:asset_id` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/media/search` | Search media assets |
| POST | `/api/media/sound_effect/generate` | Generate sound effect |
| POST | `/api/media/sync` | Sync a Drive folder into media index |
| POST | `/api/media/voiceover/generate` | Generate voiceover |

## /api/media-memory

| Method | Path | Description |
|--------|------|-------------|
| DELETE | `/api/media-memory/bindings/:id` | ⚠️ MISSING DESCRIPTION |
| GET | `/api/media-memory/bindings` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/media-memory/bindings` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/media-memory/bindings/:id/approve` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/media-memory/bindings/:id/reject` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/media-memory/resolve` | ⚠️ MISSING DESCRIPTION |

## /api/scripts

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/scripts` | List scripts |
| GET | `/api/scripts/:id` | Get script by ID |

## /api/system

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/system/doctor` | System diagnostics |

## /assets/*filepath

| Method | Path | Description |
|--------|------|-------------|
| GET | `/assets/*filepath` | Serve static assets from data dir |
| HEAD | `/assets/*filepath` | HEAD check for static assets |

## /media/google-accounting

| Method | Path | Description |
|--------|------|-------------|
| GET | `/media/google-accounting/*filepath` | Serve Google Accounting media files |
| HEAD | `/media/google-accounting/*filepath` | HEAD check for Google Accounting media |

## /vlm/autotag

| Method | Path | Description |
|--------|------|-------------|
| POST | `/vlm/autotag/analyze-file` | ⚠️ MISSING DESCRIPTION |
