# YouTube Clip Extraction — E2E Verification Runbook

## Purpose

This runbook defines the **complete verification checklist** for every YouTube clip
extraction via `POST /api/clips/process`. A `SUCCEEDED` job status is **necessary
but NOT sufficient** — the true "DONE" requires all 13 checkpoints below to pass.

**Canonical route**: `POST /api/clips/process` (NOT `/api/media/clips/process`).
The YouTube module is registered on the `/api` parent group with prefix `/clips`
(see `internal/api/assets/youtube/module.go::Build` → `api.NewRouteModule("clips", ..., "/clips", ...)`).

---

## 1. Pre-Check Diagnostics (BEFORE any extraction)

```bash
curl -s http://127.0.0.1:8000/api/clips/diagnostics \
  -H "Authorization: Bearer $VELOX_ADMIN_TOKEN"
```

**Expected response:**
```json
{
  "ok": true,
  "checks": {
    "service": true,
    "jobs": true,
    "ytdlp": "ok",
    "ffmpeg": "ok",
    "node": "ok",
    "cookies": "configured"
  }
}
```

**Gate**: ALL checks must be `true` / `"ok"` / `"configured"`. Any failure = do NOT
proceed with extraction — fix the dependency first.

---

## 2. Request Validation (BEFORE submission)

The request payload must produce a deterministic clip ID:

| Field | Value | Validation |
|-------|-------|------------|
| `url` | YouTube URL | Must be valid YouTube URL |
| `segments[].start` | `"00:02:26"` | Parsed to 146 seconds |
| `segments[].end` | `"00:02:35"` | Parsed to 155 seconds |
| `segments[].name` | descriptive name | Used for filename |
| `destination.folder_id` | Drive folder ID | Target Drive folder |

**Clip ID formula**: `yt_{videoID}_{startSec}_{endSec}_{policyVersion}`

For the above example: `yt_vdC5GXxS-qU_146_155_v1`

**Gate**: `start < end`, duration within SegmentPolicy bounds (default 4s–60s).

---

## 3. Job Enqueue Verification (AFTER submission)

```bash
curl -s 'http://127.0.0.1:8000/api/jobs?limit=3&type=youtube_clip.extract' \
  -H "Authorization: Bearer $VELOX_ADMIN_TOKEN"
```

**Verify:**
- Job type = `youtube_clip.extract`
- Status = `RUNNING` → `SUCCEEDED`
- Payload contains the full clip request
- Retry count = 0 (no transient failures)

---

## 4. Post-SUCCEEDED Verification Checklist

### 4.1 media_assets DB Record

```sql
SELECT id, source, media_type, drive_file_id, drive_link, file_hash,
       folder_id, folder_path, source_version, lifecycle_state, index_state,
       created_at, updated_at
FROM media_assets
WHERE id = '<clip_id>';
```

**Expected:**
| Field | Expected Value |
|-------|---------------|
| `id` | `yt_vdC5GXxS-qU_146_155_v1` |
| `source` | `youtube` |
| `media_type` | `video` |
| `drive_file_id` | non-empty (e.g., `1j1uBPf...`) |
| `drive_link` | `https://drive.google.com/file/d/...` |
| `file_hash` | MD5 hash (e.g., `2565a87ac...`) |
| `folder_id` | matches `destination.folder_id` |
| `folder_path` | **⚠️ KNOWN GAP: currently EMPTY for YouTube extractions** |
| `source_version` | equals `file_hash` |
| `lifecycle_state` | `ACTIVE` |
| `index_state` | `EMBEDDING_FAILED` or `INDEXING_SKIPPED_NO_INDEXER` when Qdrant/indexer unavailable |

**Known issue (2026-07-06)**: `folder_path` is NOT populated by the ClipAtomicWriter
for YouTube extractions. The `ClipAssetDrive.FolderPath` field is set from
`cmd.DriveFolderPath` in `process_segment_helpers.go::buildClipAsset`, but the
ProcessSegmentCommand carries it from `extraction_destination.go::resolveDestination`
which only populates `FolderPath` when the destination resolver returns it. When
a raw `folder_id` is passed via the API (as in direct calls), `FolderPath` stays empty.

### 4.2 outbox_events

```sql
SELECT id, event_type, aggregate_id, aggregate_type, event_key, status,
       last_error, created_at
FROM outbox_events
WHERE aggregate_id = '<clip_id>'
ORDER BY created_at DESC;
```

**Expected:**
| Field | Expected Value |
|-------|---------------|
| `event_type` | `asset.index.requested` |
| `aggregate_id` | clip ID |
| `aggregate_type` | `media_asset` |
| `status` | `pending` → `processing` → `completed` |
| `last_error` | empty (no errors) |

**Gate**: Status must NOT be `dead_lettered`. Exactly 1 event (no duplicates).

### 4.3 Qdrant Indexing

```bash
curl -s 'http://127.0.0.1:6333/collections/media_assets/points/scroll' \
  -X POST -H 'Content-Type: application/json' \
  -d '{"filter":{"must":[{"key":"asset_id","match":{"value":"<clip_id>"}}]},
       "limit":5,"with_payload":true}'
```

**Expected:** Point present with payload containing `asset_id`, `media_type`,
`source`, `drive_file_id`, `file_hash`, `name`, `summary`.

**Known issue (2026-07-06)**: When `clipindexer.enabled=false` OR the
`IndexClip` call fails, `index_state` is set to `EMBEDDING_FAILED` and the
outbox event stays `pending`. The Qdrant point will NOT exist until the
indexer processes the event.

### 4.4 Drive Verification

Open the `drive_link` in a browser and verify:
- Video is visible and playable
- Duration ≈ expected (±5% tolerance)
- Clip covers the correct timestamp range
- Filename matches expected pattern

### 4.5 Idempotency Check

Re-submit the IDENTICAL request (same URL, same segments, same destination).

**Expected:**
- Second call returns same `clip_id` (deterministic)
- Cache hit: `status: "skipped"` (if strategy != replace)
- No duplicate `media_assets` row
- No duplicate Drive file (content hash matches → `UploadFileIfChanged` skips)
- No duplicate `outbox_events` entry (same `event_key`)

### 4.6 Cache Behavior

| Strategy | Behavior |
|----------|----------|
| `verify` (default) | Cache hit → `skipped`; cache miss → full pipeline |
| `skip` | Skip processing even on cache miss |
| `replace` | Bypass cache, re-run full pipeline, update existing record |

---

## 5. Alert Signals (Things That Should NOT Happen)

```
[ ] Status SUCCEEDED but media_assets row missing
[ ] Status SUCCEEDED but outbox_events has no asset.index.requested
[ ] media_assets.source_version is empty
[ ] drive_file_id present but folder_id empty
[ ] outbox status = dead_lettered
[ ] Qdrant has no point after outbox = completed
[ ] Same request creates two different Drive files
[ ] Different clip_id for same video/start/end/policy
[ ] folder_path empty when Drive subfolder was requested
```

---

## 6. Broner Clip Extraction — Verification Results (2026-07-06)

**Clip**: `yt_vdC5GXxS-qU_146_155_v1` (Pacquiao rant, 02:26–02:35, 9 sec)

| # | Checkpoint | Result | Notes |
|---|-----------|--------|-------|
| 1 | Pre-check diagnostics | ✅ PASS | yt-dlp=ok, ffmpeg=ok, node=ok, cookies=configured |
| 2 | Request validation | ✅ PASS | start=146, end=155, duration=9s (within 4–60s policy) |
| 3 | Job enqueue | ✅ PASS | type=youtube_clip.extract, status=SUCCEEDED, retry_count=0 |
| 4.1 | media_assets row | ✅ PASS | drive_file_id, drive_link, file_hash, source_version all present |
| 4.1 | folder_path | ⚠️ EMPTY | Known gap — raw folder_id from API doesn't populate FolderPath |
| 4.1 | lifecycle_state | ✅ ACTIVE | |
| 4.1 | index_state | ⚠️ EMBEDDING_FAILED | Indexer embedding call failed |
| 4.2 | outbox_events | ✅ PASS | 1 event, asset.index.requested, status=pending |
| 4.2 | outbox duplicates | ✅ PASS | Exactly 1 event (no duplicates) |
| 4.2 | outbox dead_letter | ✅ PASS | NOT dead_lettered |
| 4.3 | Qdrant point | ❌ MISSING | No point — outbox still pending, indexer hasn't processed |
| 4.4 | Drive link | ✅ PASS | https://drive.google.com/file/d/1j1uBPf05Fm0b2HsRuMApVdK0l-lII93D/view?usp=drivesdk |
| 4.5 | Idempotency | ✅ PASS | Re-submit = cache hit, no duplicates |
| 4.6 | Cache behavior | ✅ PASS | Second identical call returns skipped |

**Verdict**: Extraction pipeline works end-to-end for download → cut → normalize →
Drive upload → DB write. Two gaps remain: (1) folder_path not populated from raw
folder_id, (2) EMBEDDING_FAILED blocks Qdrant indexing. Both are pre-existing
known issues, not regressions of this extraction.
