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
| POST | `/api/admin/auth/login` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/admin/auth/logout` | ⚠️ MISSING DESCRIPTION |

## /api/artlist

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/artlist/diagnostics` | Artlist diagnostics |
| GET | `/api/artlist/job-consumer` | ⚠️ MISSING DESCRIPTION |
| GET | `/api/artlist/runs/:run_id` | Get Artlist pipeline run status |
| GET | `/api/artlist/search/live` | ⚠️ MISSING DESCRIPTION |
| GET | `/api/artlist/stats` | Get Artlist statistics |
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
| GET | `/api/assets/operator/index-health` | ⚠️ MISSING DESCRIPTION |
| GET | `/api/assets/operator/operations/errors` | ⚠️ MISSING DESCRIPTION |
| GET | `/api/assets/operator/outbox/events` | ⚠️ MISSING DESCRIPTION |
| GET | `/api/assets/operator/outbox/status` | ⚠️ MISSING DESCRIPTION |
| GET | `/api/assets/operator/summary` | ⚠️ MISSING DESCRIPTION |

## /api/capabilities

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/capabilities` | ⚠️ MISSING DESCRIPTION |

## /api/clips

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/clips/diagnostics` | Clips diagnostics |
| GET | `/api/clips/info` | Get YouTube video metadata |
| GET | `/api/clips/stats` | Get clips statistics |
| POST | `/api/clips/extract-important` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/clips/process` | Download and process clips |

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

## /api/fullimages

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/fullimages/image/generate` | Generate one image per section (fullimages image-only pipeline) |

## /api/images

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/images/diagnostics` | Images diagnostics |
| GET | `/api/images/generated/search` | Search generated images |
| GET | `/api/images/generated/styles` | List generated image styles |
| GET | `/api/images/retrieved/search` | Search retrieved images |
| GET | `/api/images/search` | Search images by territory |
| POST | `/api/images/batch-generate` | Batch generate AI images asynchronously |
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
| DELETE | `/api/media/clips/:source/clips/:id` | ⚠️ MISSING DESCRIPTION |
| DELETE | `/api/media/clips/:source/folders/:id` | ⚠️ MISSING DESCRIPTION |
| GET | `/api/media/clips/:source/breadcrumb` | ⚠️ MISSING DESCRIPTION |
| GET | `/api/media/clips/:source/clips` | ⚠️ MISSING DESCRIPTION |
| GET | `/api/media/clips/:source/clips/:id` | ⚠️ MISSING DESCRIPTION |
| GET | `/api/media/clips/:source/folders` | ⚠️ MISSING DESCRIPTION |
| GET | `/api/media/clips/:source/folders/:id` | ⚠️ MISSING DESCRIPTION |
| GET | `/api/media/clips/:source/folders/:id/children` | ⚠️ MISSING DESCRIPTION |
| GET | `/api/media/clips/:source/tree` | ⚠️ MISSING DESCRIPTION |
| GET | `/api/media/diagnostics` | Media diagnostics |
| GET | `/api/media/index-health` | Media index health check |
| PATCH | `/api/media/clips/:source/clips/:id` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/media/clips/:source/bulk/tags/add` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/media/clips/:source/bulk/tags/remove` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/media/clips/:source/cleanup` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/media/clips/:source/clips` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/media/clips/:source/clips/:id/download` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/media/clips/:source/clips/:id/duplicates` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/media/clips/:source/clips/:id/fix-hash` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/media/clips/:source/clips/:id/reindex` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/media/clips/:source/clips/:id/reprocess` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/media/clips/:source/clips/:id/reupload` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/media/clips/:source/clips/:id/status` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/media/clips/:source/clips/:id/verify` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/media/clips/:source/clips/bulk-upload-youtube-clips` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/media/clips/:source/folders/:id/manifest` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/media/clips/:source/reconcile` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/media/clips/enrich` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/media/clips/enrich/batch` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/media/clips/ingest/ai-stock` | Ingest an AI-generated stock clip from visual analysis + Drive video |
| POST | `/api/media/clips/upload-video` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/media/qdrant/cleanup` | Clean up stale Qdrant points |
| POST | `/api/media/register-batch` | Batch register assets |
| POST | `/api/media/register-from-youtube` | Register asset from YouTube URL |
| POST | `/api/media/search` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/media/sound_effect/generate` | Generate sound effect |
| POST | `/api/media/sync` | ⚠️ MISSING DESCRIPTION |
| POST | `/api/media/voiceover/generate` | Generate voiceover |

## /api/script

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/script/clips/search` | Search script clips by name |
| GET | `/api/script/jobs/:id` | Get script job status |
| POST | `/api/script/generate` | Generate a script asynchronously |
| POST | `/api/script/shorts/generate` | Generate a Remotion Shorts video |
| POST | `/api/script/shorts/render` | Render a Remotion Shorts video synchronously |
| POST | `/api/script/shorts/render/async` | Enqueue a Remotion Shorts render job |

## /api/script-assets

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/script-assets/catalog` | ⚠️ MISSING DESCRIPTION |

## /api/script-docs

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/script-docs/generate` | ⚠️ MISSING DESCRIPTION |

## /api/scripts

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/scripts` | List scripts |
| GET | `/api/scripts/:id` | Get script by ID |

## /api/system

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/system/doctor` | System diagnostics |

## /api/ui

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/ui/health` | ⚠️ MISSING DESCRIPTION |

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

