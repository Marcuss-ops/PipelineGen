# Script to Assets Workflow

**Status:** ACTIVE - Workflow documentation

This workflow covers generating a script and converting it into video assets (voiceover + clips).

## Overview

1. **Generate Script** - Create a script from a topic using Ollama
2. **Generate Voiceover** - Convert script to audio using TTS
3. **Find Stock Clips** - Search Artlist for matching footage
4. **Combine Assets** - Metadata export with all asset paths

## Step 1: Generate Script

**Job Type:** `script.generate`

**Payload:**
```json
{
  "topic": "AI in modern healthcare",
  "source_text": "Reference material...",
  "language": "en",
  "template": "documentary"
}
```

**Handler uses:** `OllamaAdapter.Generate()`

**Output:**
```json
{
  "script_id": "script-001",
  "content": "The rapid advancement...",
  "doc_url": "https://docs.example.com/..."
}
```

---

## Step 2: Generate Voiceover

**Job Type:** `voiceover.generate`

**Payload:**
```json
{
  "script_text": "The rapid advancement of AI in healthcare...",
  "language": "en",
  "filename": "voiceover-001.mp3"
}
```

**Handler uses:** TTS service (could use Ollama or external TTS API)

**Output:**
```json
{
  "audio_path": "/path/to/voiceover-001.mp3",
  "duration": 120.5,
  "file_size": 2048000
}
```

---

## Step 3: Find Stock Clips

**Job Type:** `artlist.run` (see ARTLIST_PIPELINE.md for details)

**Payload:**
```json
{
  "tag": "healthcare technology",
  "limit": 5,
  "output_dir": "/path/to/raw",
  "processed_dir": "/path/to/processed",
  "drive_folder_id": "folder123"
}
```

**Handler uses:**
- `ArtlistAdapter.Search()`
- `YTDLPAdapter.Download()`
- `FFmpegAdapter.ProcessClip()`
- `DriveAdapter.Upload()`

---

## Step 4: Export Metadata

**Endpoint:** `GET /api/export/script/:scriptId/assets`

**Output:**
```json
{
  "script": {
    "id": "script-001",
    "content": "...",
    "doc_url": "..."
  },
  "voiceover": {
    "path": "/path/to/voiceover-001.mp3",
    "duration": 120.5,
    "drive_url": "https://drive.google.com/..."
  },
  "clips": [
    {
      "title": "Healthcare Tech",
      "local_path": "/path/to/processed/clip-001.mp4",
      "drive_url": "https://drive.google.com/...",
      "duration": 7.0
    }
  ],
  "total_duration": 35.0
}
```

---

## Job Orchestration

These jobs can be chained:

```
Script Generation (job-001)
    ↓ (on success)
Voiceover Generation (job-002)
    ↓ (on success)
Artlist Pipeline (job-003)
    ↓ (on success)
Metadata Export
```

Use job events to track progress:
`GET /api/jobs/job-001/events`

---

## Error Recovery

If any job fails:
1. Check job error: `GET /api/jobs/:id`
2. Retry if attempts < max: `POST /api/jobs/:id/retry`
3. Zombie jobs (>15min running) are auto-recovered

---

## Architecture Diagram

```
API Request
    ↓
Create Job (script.generate)
    ↓
Job Runner → Registry → Handler
    ↓
OllamaAdapter.Generate()
    ↓
Mark Succeeded (result_json = script)
    ↓
Webhook/Event triggers next job
    ↓
Create Job (voiceover.generate)
    ↓
... (similar flow)
```

This modular architecture allows:
- Independent scaling of job processing
- Easy addition of new job types
- Reliable retry and recovery
- Clear separation of concerns via adapters
