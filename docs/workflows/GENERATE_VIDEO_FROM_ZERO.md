# Generate a Video From Zero

**Status:** ACTIVE - Workflow documentation

This workflow explains how to generate a complete video package using PipelineGen.

## Overview

The video generation workflow involves multiple steps:
1. Generate a script from a topic
2. Search for stock footage clips
3. Process and edit the clips
4. Generate voiceover from the script
5. Combine everything into a final video
6. Upload assets to cloud storage

## 1. Generate Script

**Endpoint:** `POST /api/script-docs/generate`

**Input:**
```json
{
  "topic": "artificial intelligence in healthcare",
  "source_text": "Optional reference material...",
  "language": "en",
  "template": "documentary"
}
```

**Output:**
```json
{
  "script_id": "script-001",
  "doc_url": "https://docs.example.com/script-001",
  "preview_path": "/path/to/preview.pdf"
}
```

**Job Created:** `script.generate`

---

## 2. Search Stock Clips

**Endpoint:** `POST /api/artlist/run`

**Input:**
```json
{
  "tag": "technology",
  "limit": 10
}
```

**Output:**
```json
{
  "run_id": "run-001",
  "job_id": "job-001"
}
```

**Job Created:** `artlist.run`

This job will:
- Search Artlist for clips matching the tag
- Download selected clips via yt-dlp
- Process clips with FFmpeg (resize, trim, format)
- Upload processed clips to Google Drive
- Save metadata to database

---

## 3. Track Job Status

**Endpoint:** `GET /api/jobs/:id`

**Possible Statuses:**
- `created` - Job has been created
- `queued` - Job is waiting to be processed
- `running` - Job is currently executing
- `succeeded` - Job completed successfully
- `failed` - Job failed with error
- `cancelled` - Job was cancelled by user
- `retrying` - Job is being retried after failure

**Example Response:**
```json
{
  "id": "job-001",
  "type": "artlist.run",
  "status": "running",
  "payload_json": "{...}",
  "result_json": "{...}",
  "attempts": 1,
  "max_attempts": 3,
  "created_at": "2026-05-02T16:00:00Z",
  "started_at": "2026-05-02T16:00:05Z"
}
```

**Job Events:** `GET /api/jobs/:id/events`

Returns a timeline of events for the job:
- `job_created`
- `download_started` / `download_finished`
- `ffmpeg_started` / `ffmpeg_finished`
- `upload_started` / `upload_finished`
- `job_succeeded` / `job_failed`

---

## 4. Generate Voiceover

**Endpoint:** `POST /api/voiceover/generate`

**Input:**
```json
{
  "script_text": "The rapid advancement of artificial intelligence...",
  "language": "en",
  "filename": "voiceover-001.mp3"
}
```

**Output:**
```json
{
  "audio_path": "/path/to/voiceover-001.mp3",
  "duration": 120.5
}
```

**Job Created:** `voiceover.generate`

---

## 5. Export Metadata

After all processing is complete, export the metadata:

**Expected Output Package:**
```json
{
  "script_id": "script-001",
  "voiceover_path": "/path/to/voiceover-001.mp3",
  "clips": [
    {
      "id": "clip-001",
      "title": "Technology Abstract",
      "drive_url": "https://drive.google.com/file/d/...",
      "local_path": "/path/to/processed/clip-001.mp4",
      "duration": 7.0
    }
  ],
  "drive_links": ["https://drive.google.com/file/d/..."],
  "local_paths": ["/path/to/processed/..."]
}
```

---

## Error Handling

If a job fails:
1. Check the job error message: `GET /api/jobs/:id`
2. Check job events for details: `GET /api/jobs/:id/events`
3. Retry the job (if attempts < max_attempts): `POST /api/jobs/:id/retry`
4. Check zombie recovery: Jobs stuck in `running` for >15min are auto-recovered

---

## Architecture

```
API Handler
    ↓
Service Layer
    ↓
Create Job (persistent in SQLite)
    ↓
Job Runner (polls for queued jobs)
    ↓
Job Handler (registered by job type)
    ↓
Adapters (FFmpeg, yt-dlp, Drive, Ollama, Artlist)
    ↓
External Tools
```

This architecture ensures:
- Jobs are persistent and survive server restarts
- Failed jobs can be retried automatically
- Zombie jobs (stuck >15min) are auto-recovered
- Each external tool is behind an adapter interface for testability
