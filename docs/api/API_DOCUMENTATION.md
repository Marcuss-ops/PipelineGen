# VeloxEditing API Documentation

## Overview

The VeloxEditing API is a **Go-based** backend (Gin framework) that orchestrates automated video content creation. It manages script generation, voiceover synthesis, stock clip management, video processing (via Rust binary), job/worker orchestration, and publishing to Google Drive/YouTube.

**Base URL:** `http://localhost:8080` (default)

**Default Port:** 8080 (configurable via `VELOX_PORT` env var)

---

## Table of Contents

1. [Health & System](#health--system)
2. [Video Processing](#video-processing)
3. [Script Generation](#script-generation)
4. [Voiceover](#voiceover)
5. [Job Management](#job-management)
6. [Worker Management](#worker-management)
7. [Stock Management](#stock-management)
   - [Projects](#stock-projects)
   - [Search](#stock-search)
   - [Processing](#stock-processing)
8. [Clip Management](#clip-management)
9. [Download Management](#download-management)
10. [YouTube Integration](#youtube-integration)
11. [Google Drive](#google-drive)
12. [NLP](#nlp)
13. [Scraper](#scraper)
14. [Dashboard](#dashboard)
15. [Stats](#stats)
16. [Admin](#admin)
17. [Swagger UI](#swagger-ui)

---

## Health & System

### GET `/health`
Basic health check.

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

### GET `/api/status`
Detailed server status with worker and job counts.

**Response:**
```json
{
  "ok": true,
  "status": "running",
  "workers": {},
  "jobs": {
    "total": 10,
    "counts": { "pending": 3, "running": 2, "completed": 5 }
  },
  "config": {
    "new_jobs_paused": false
  }
}
```

### GET `/api/metrics`
Server metrics.

**Response:**
```json
{
  "ok": true,
  "metrics": {
    "workers_total": 2,
    "jobs_total": 10
  }
}
```

---

## Video Processing

### POST `/api/video/create-master`
Main entry point for video creation. Generates script, extracts entities, creates voiceover, and dispatches to workers.

**Request:**
```json
{
  "video_name": "My Video",
  "project_name": "My Project",
  "script_text": "Optional pre-written script",
  "youtube_url": "https://youtube.com/watch?v=...",
  "source": "Source description",
  "language": "en",
  "duration": 300,
  "voiceover_drive_folder": "DRIVE_FOLDER_ID",
  "skip_gdocs": false,
  "entity_count": 12
}
```

**Response (202):**
```json
{
  "ok": true,
  "job_id": "job_abc123",
  "video_name": "My Video",
  "project_name": "My Project",
  "status": "processing",
  "script": {
    "generated": true,
    "script_text": "...",
    "word_count": 500,
    "model": "llama3"
  },
  "entities": {
    "total_segments": 2,
    "entity_count_per_segment": 12,
    "total_entities": 24,
    "segment_entities": [...]
  },
  "voiceover": {
    "generated": true,
    "items": [...]
  },
  "video": {
    "created": false,
    "output": "",
    "processing": false
  }
}
```

### GET `/api/video/health`
Check if video processing service is healthy.

**Response:**
```json
{
  "ok": true,
  "status": "healthy",
  "service": "video-processor"
}
```

### GET `/api/video/info`
Get information about video processing capabilities.

**Response:**
```json
{
  "ok": true,
  "service": {
    "name": "velox-video-processor",
    "version": "1.0.0-go",
    "backend": "rust"
  },
  "capabilities": [
    "video_processing",
    "stock_clip_generation",
    "effects_application",
    "audio_mixing",
    "script_generation",
    "voiceover_generation"
  ]
}
```

---

## Script Generation

### POST `/api/script/generate`
Generate a video script from source text using Ollama.

**Request:**
```json
{
  "title": "Video Title",
  "source_text": "Source text or topic",
  "language": "en",
  "duration": 300,
  "model": "llama3"
}
```

**Response:**
```json
{
  "ok": true,
  "script": "Generated script text...",
  "word_count": 500,
  "est_duration": 180,
  "model": "llama3"
}
```

### POST `/api/script/from-youtube`
Generate a script from a YouTube URL.

**Request:**
```json
{
  "youtube_url": "https://youtube.com/watch?v=...",
  "title": "Video Title",
  "language": "en",
  "duration": 300,
  "model": "llama3"
}
```

**Note:** This endpoint returns 501 (Not Implemented) — YouTube transcript download is not yet implemented. Use `/api/script/from-transcript` with a pre-fetched transcript instead.

### POST `/api/script/from-transcript`
Generate a script from a pre-fetched YouTube transcript.

**Request:**
```json
{
  "youtube_url": "https://youtube.com/watch?v=...",
  "transcript": "Full transcript text...",
  "title": "Video Title",
  "language": "en",
  "duration": 300,
  "model": "llama3"
}
```

**Response:**
```json
{
  "ok": true,
  "script": "Generated script text...",
  "word_count": 500,
  "est_duration": 180,
  "model": "llama3"
}
```

### POST `/api/script/regenerate`
Regenerate an existing script with improvements.

**Request:**
```json
{
  "original_script": "Original script text...",
  "title": "Video Title",
  "tone": "professional",
  "language": "en",
  "duration": 300,
  "model": "llama3"
}
```

**Response:**
```json
{
  "ok": true,
  "script": "Regenerated script text...",
  "word_count": 520,
  "est_duration": 185,
  "model": "llama3"
}
```

### GET `/api/script/models`
List available Ollama models.

**Response:**
```json
{
  "ok": true,
  "models": ["llama3", "gemma:12b", "mistral"],
  "count": 3
}
```

### GET `/api/script/health`
Check if Ollama service is reachable.

**Response:**
```json
{
  "ok": true,
  "healthy": true,
  "service": "ollama"
}
```

### POST `/api/script/summarize`
Summarize text using Ollama.

**Request:**
```json
{
  "text": "Long text to summarize...",
  "max_words": 100
}
```

**Response:**
```json
{
  "ok": true,
  "summary": "Short summary..."
}
```

---

## Voiceover

### POST `/api/voiceover/generate`
Generate voiceover audio from text using EdgeTTS.

**Request:**
```json
{
  "text": "Text to convert to audio",
  "language": "en",
  "voice": "en-US-AriaNeural"
}
```

**Response:**
```json
{
  "ok": true,
  "file_name": "output_abc123.mp3",
  "file_path": "/path/to/output.mp3",
  "duration": 15.2,
  "word_count": 50,
  "voice": "en-US-AriaNeural",
  "language": "en"
}
```

### GET `/api/voiceover/languages`
List available languages for voiceover.

**Response:**
```json
{
  "ok": true,
  "languages": [
    { "code": "en", "name": "English", "voices": ["en-US-AriaNeural"] },
    { "code": "it", "name": "Italian", "voices": ["it-IT-ElsaNeural"] }
  ],
  "count": 2
}
```

### GET `/api/voiceover/voices`
List all available voices.

**Response:**
```json
{
  "ok": true,
  "voices": {
    "en": "en-US-AriaNeural",
    "it": "it-IT-ElsaNeural",
    "es": "es-ES-ElviraNeural",
    "fr": "fr-FR-DeniseNeural",
    "de": "de-DE-KatjaNeural"
  }
}
```

### GET `/api/voiceover/download/:file`
Download a generated voiceover file (MP3).

### GET `/api/voiceover/health`
Check if EdgeTTS is available.

**Response:**
```json
{
  "ok": true,
  "status": "healthy",
  "edge_tts_available": true,
  "output_dir": "/path/to/voiceovers"
}
```

---

## Job Management

### GET `/api/jobs`
List all jobs. Supports query parameters: `status`, `type`, `worker_id`.

**Response:**
```json
{
  "ok": true,
  "jobs": [...],
  "count": 10
}
```

### POST `/api/jobs`
Create a new job.

**Request:**
```json
{
  "type": "video_generation",
  "project": "My Project",
  "payload": { ... }
}
```

**Response (201):**
```json
{
  "ok": true,
  "job_id": "job_abc123",
  "job": { ... }
}
```

### GET `/api/jobs/:id`
Get a specific job by ID.

**Response:**
```json
{
  "ok": true,
  "job": {
    "id": "job_abc123",
    "type": "video_generation",
    "status": "running",
    "progress": 50,
    "project": "My Project",
    "worker_id": "worker_1",
    "created_at": "2024-01-15T10:30:00Z"
  }
}
```

### PUT `/api/jobs/:id/status`
Update a job's status.

**Request:**
```json
{
  "status": "completed",
  "progress": 100,
  "result": { "output": "/path/to/video.mp4" },
  "error": ""
}
```

**Response:**
```json
{
  "ok": true,
  "job_id": "job_abc123",
  "status": "completed"
}
```

### DELETE `/api/jobs/:id`
Delete a job.

**Response:**
```json
{
  "ok": true,
  "job_id": "job_abc123"
}
```

### POST `/api/jobs/:id/assign`
Assign a job to a worker.

**Request:**
```json
{
  "worker_id": "worker_1"
}
```

**Response:**
```json
{
  "ok": true,
  "job_id": "job_abc123",
  "worker_id": "worker_1"
}
```

### POST `/api/jobs/:id/lease`
Renew a job's lease (keep it active).

**Response:**
```json
{
  "ok": true,
  "job_id": "job_abc123"
}
```

---

## Worker Management

### GET `/api/workers`
List all workers.

**Response:**
```json
{
  "ok": true,
  "workers": [...],
  "count": 2
}
```

### POST `/api/workers/register`
Register a new worker.

**Request:**
```json
{
  "name": "worker-1",
  "capabilities": ["video_processing", "stock_clips"]
}
```

**Response (201):**
```json
{
  "ok": true,
  "worker_id": "worker_abc123",
  "token": "worker_token_xyz",
  "worker": { ... }
}
```

### GET `/api/workers/:id`
Get a specific worker.

### POST `/api/workers/:id/heartbeat`
Send a heartbeat from a worker.

**Request:**
```json
{
  "worker_id": "worker_abc123",
  "status": "idle",
  "disk_free_gb": 50.0,
  "current_job_id": ""
}
```

### POST `/api/worker/poll`
Worker polling endpoint. Workers call this to check for new jobs and commands.

**Request:**
```json
{
  "worker_id": "worker_abc123",
  "status": "idle",
  "disk_free_gb": 50.0
}
```

**Response:**
```json
{
  "ok": true,
  "commands": [...]
}
```

### GET `/api/workers/:id/commands`
Get pending commands for a worker.

### POST `/api/workers/:id/commands/:command_id/ack`
Acknowledge a command.

### POST `/api/workers/:id/command`
Send a command to a worker.

**Request:**
```json
{
  "type": "restart",
  "payload": {}
}
```

### POST `/api/workers/:id/revoke`
Revoke a worker.

### POST `/api/workers/:id/quarantine`
Quarantine a worker.

### POST `/api/workers/:id/unquarantine`
Unquarantine a worker.

---

## Stock Management

### Stock Projects

#### GET `/api/stock/projects`
List all stock projects.

**Response:**
```json
{
  "ok": true,
  "projects": [...],
  "count": 3
}
```

#### POST `/api/stock/project`
Create a new stock project.

**Request:**
```json
{
  "name": "my-project"
}
```

**Response (201):**
```json
{
  "ok": true,
  "project": {
    "name": "my-project",
    "status": "active",
    "video_count": 0
  }
}
```

#### GET `/api/stock/project/:name`
Get a stock project by name.

#### DELETE `/api/stock/project/:name`
Delete a stock project.

#### POST `/api/stock/project/:name/video`
Add a video to a project.

**Request:**
```json
{
  "video_path": "/path/to/video.mp4",
  "source_url": "https://youtube.com/watch?v=...",
  "title": "Video Title"
}
```

#### GET `/api/stock/project/:name/videos`
List videos in a project.

#### DELETE `/api/stock/project/:name/video/:id`
Delete a video from a project.

### Stock Search

#### GET `/api/stock/search`
Search for stock videos. Query parameter `q` required.

**Query Parameters:**
- `q` (required): Search query
- `max` (optional): Max results (default: 20)

**Response:**
```json
{
  "ok": true,
  "query": "spider",
  "results": [...],
  "count": 10
}
```

#### GET `/api/stock/search/youtube`
Search YouTube for stock videos.

**Query Parameters:**
- `q` (required): Search query
- `max` (optional): Max results (default: 10)

**Response:**
```json
{
  "ok": true,
  "query": "spider",
  "results": [...],
  "count": 10,
  "platform": "youtube"
}
```

#### POST `/api/stock/search/download`
Download a video from search results.

**Request:**
```json
{
  "url": "https://youtube.com/watch?v=...",
  "project_id": "my-project",
  "quality": "best"
}
```

**Response:**
```json
{
  "ok": true,
  "file": "/path/to/downloaded/video.mp4",
  "quality": "best",
  "downloaded_at": "2024-01-15T10:30:00Z"
}
```

### Stock Processing

#### POST `/api/stock/process`
Process stock videos using the Rust binary (transitions, effects, audio mixing).

**Request:**
```json
{
  "project_id": "my-project",
  "video_paths": ["/path/to/video1.mp4", "/path/to/video2.mp4"],
  "output_path": "/path/to/output.mp4",
  "transition_type": "fade",
  "effects": ["zoom", "pan"],
  "music_path": "/path/to/music.mp3"
}
```

**Response:**
```json
{
  "ok": true,
  "project_name": "my-project",
  "videos_processed": 2,
  "output_files": ["/path/to/output.mp4"],
  "processing_time": "45.2s"
}
```

#### POST `/api/stock/process/batch`
Batch process multiple video sets.

**Request:**
```json
{
  "requests": [
    {
      "project_id": "project-1",
      "video_paths": ["/path/to/video1.mp4"]
    },
    {
      "project_id": "project-2",
      "video_paths": ["/path/to/video2.mp4"]
    }
  ]
}
```

**Response (202):**
```json
{
  "ok": true,
  "results": [
    { "index": 0, "status": "success", "result": { ... } },
    { "index": 1, "status": "success", "result": { ... } }
  ],
  "count": 2
}
```

#### POST `/api/stock/studio`
Create a studio project.

**Request:**
```json
{
  "project_id": "my-project",
  "name": "Studio Project Name"
}
```

#### GET `/api/stock/health`
Check stock processing health (Rust binary status).

**Response:**
```json
{
  "ok": true,
  "rust_binary": "/path/to/video-stock-creator.bundle",
  "binary_exists": true,
  "effects_dir": "/path/to/effects",
  "service": "stock-processor"
}
```

---

## Clip Management

### POST `/api/clip/search-folders`
Search for clip folders in Google Drive.

**Request:**
```json
{
  "query": "nature",
  "group": "discovery",
  "parent_id": "DRIVE_FOLDER_ID",
  "max_depth": 2,
  "max_results": 50
}
```

**Response:**
```json
{
  "ok": true,
  "folders": [
    {
      "id": "folder_abc",
      "name": "Nature Clips",
      "link": "https://drive.google.com/drive/folders/folder_abc",
      "parent_id": "root",
      "depth": 1,
      "clip_count": 25,
      "subfolders": [...]
    }
  ],
  "total": 5,
  "query": "nature",
  "group": "discovery",
  "search_time": 1200
}
```

### POST `/api/clip/read-folder-clips`
Read clips from a Google Drive folder.

**Request:**
```json
{
  "folder_id": "folder_abc",
  "folder_name": "Nature Clips",
  "include_subfolders": true
}
```

**Response:**
```json
{
  "ok": true,
  "folder_id": "folder_abc",
  "folder_name": "Nature Clips",
  "clips": [
    {
      "id": "clip_xyz",
      "name": "Spider Web",
      "drive_link": "https://drive.google.com/file/d/clip_xyz/view",
      "size": 10485760,
      "mime_type": "video/mp4",
      "thumbnail": "https://drive.google.com/thumbnail?id=clip_xyz"
    }
  ],
  "videos": [...],
  "subfolders": [...],
  "total_clips": 25,
  "total_videos": 25,
  "total_subfolders": 3
}
```

### POST `/api/clip/suggest`
Suggest clips for a video title using AI matching.

**Request:**
```json
{
  "title": "The Amazing World of Spiders",
  "script": "Optional script text...",
  "group": "discovery",
  "max_results": 10,
  "min_score": 10.0
}
```

**Response:**
```json
{
  "ok": true,
  "title": "The Amazing World of Spiders",
  "suggestions": [
    {
      "clip": { "id": "clip_xyz", "name": "Spider Web" },
      "score": 95.5,
      "reason": "High relevance to title"
    }
  ],
  "total": 10,
  "group": "discovery",
  "processing_time": 850
}
```

### POST `/api/clip/create-subfolder`
Create a new subfolder for clips.

**Request:**
```json
{
  "folder_name": "New Clip Folder",
  "group": "discovery",
  "parent_id": "DRIVE_FOLDER_ID"
}
```

**Response:**
```json
{
  "ok": true,
  "folder_name": "New Clip Folder",
  "folder_id": "folder_new",
  "parent_id": "root",
  "link": "https://drive.google.com/drive/folders/folder_new"
}
```

### POST `/api/clip/subfolders`
List subfolders with clip counts.

**Request:**
```json
{
  "parent_id": "root",
  "max_depth": 3,
  "max_results": 100
}
```

**Response:**
```json
{
  "ok": true,
  "parent_id": "root",
  "subfolders": [...],
  "total": 15
}
```

### GET `/api/clip/health`
Check clip service health.

**Response:**
```json
{
  "ok": true,
  "status": "healthy",
  "service": "clip-service",
  "drive_status": "connected",
  "cache_stats": { ... }
}
```

### GET `/api/clip/groups`
Get available clip groups.

**Response:**
```json
{
  "ok": true,
  "groups": ["discovery", "interview", "documentary"],
  "count": 3
}
```

### POST `/api/clip/download`
Download a clip from YouTube and upload to Google Drive.

**Request:**
```json
{
  "youtube_url": "https://youtube.com/watch?v=...",
  "title": "Clip Title",
  "group": "discovery",
  "drive_folder": "folder_name",
  "start_time": "00:01:30",
  "end_time": "00:02:00"
}
```

**Response:**
```json
{
  "ok": true,
  "message": "Clip downloaded and uploaded successfully",
  "youtube_url": "https://youtube.com/watch?v=...",
  "title": "Clip Title",
  "file_id": "DRIVE_FILE_ID",
  "drive_link": "https://drive.google.com/file/d/DRIVE_FILE_ID/view",
  "folder_id": "DRIVE_FOLDER_ID"
}
```

### POST `/api/clip/upload`
Upload a local clip file to Google Drive.

**Request:**
```json
{
  "clip_path": "/path/to/local/video.mp4",
  "title": "Clip Title",
  "group": "discovery",
  "drive_folder": "folder_name"
}
```

**Response:**
```json
{
  "ok": true,
  "message": "Clip uploaded successfully",
  "clip_path": "/path/to/local/video.mp4",
  "title": "Clip Title",
  "file_id": "DRIVE_FILE_ID",
  "drive_link": "https://drive.google.com/file/d/DRIVE_FILE_ID/view",
  "folder_id": "DRIVE_FOLDER_ID"
}
```

---

## Download Management

### POST `/api/download`
Download a video from YouTube or TikTok.

**Request:**
```json
{
  "url": "https://youtube.com/watch?v=VIDEO_ID"
}
```

**Response:**
```json
{
  "ok": true,
  "platform": "youtube",
  "video_id": "VIDEO_ID",
  "title": "Video Title",
  "file": "/path/to/downloads/youtube/VIDEO_ID/video.mp4",
  "duration": 300.5,
  "author": "Channel Name"
}
```

### GET `/api/download/platforms`
List supported download platforms and their features.

**Response:**
```json
{
  "ok": true,
  "platforms": [
    {
      "name": "YouTube",
      "code": "youtube",
      "url_pattern": "youtube.com/watch?v=... | youtu.be/...",
      "features": ["1080p", "4K", "Playlist", "Metadata"]
    },
    {
      "name": "TikTok",
      "code": "tiktok",
      "url_pattern": "tiktok.com/@user/video/... | vm.tiktok.com/...",
      "features": ["No watermark", "HD", "Metadata"]
    }
  ]
}
```

### GET `/api/download/library`
List all downloaded videos by platform.

**Response:**
```json
{
  "ok": true,
  "downloads": {
    "youtube": ["VIDEO_ID_1", "VIDEO_ID_2"],
    "tiktok": ["TIKTOK_ID_1"]
  }
}
```

### GET `/api/download/library/:platform`
List downloads for a specific platform.

**Response:**
```json
{
  "ok": true,
  "videos": ["/path/to/video1.mp4", "/path/to/video2.mp4"],
  "count": 2
}
```

### DELETE `/api/download/library/:platform/:videoID`
Delete a downloaded video.

**Response:**
```json
{
  "ok": true,
  "message": "Download deleted"
}
```

---

## YouTube Integration

### GET `/api/youtube/subtitles`
Get subtitles from a YouTube video.

### POST `/api/youtube/search`
Search YouTube for videos.

### POST `/api/youtube/search/interviews`
Search YouTube for interview videos.

### POST `/api/youtube/remote/search`
Remote search YouTube videos (compatibility endpoint).

### POST `/api/youtube/remote/channel-videos`
Get channel videos.

### POST `/api/youtube/remote/video-info`
Get video information.

### POST `/api/youtube/remote/thumbnail`
Get video thumbnail.

### POST `/api/youtube/remote/trending`
Get trending videos.

### POST `/api/youtube/remote/channel-analytics`
Get channel analytics data.

### POST `/api/youtube/remote/related-videos`
Get related videos for a given video.

### POST `/api/youtube/stock/search`
Search YouTube for stock video candidates.

---

## Google Drive

### GET `/api/drive/folders-tree`
Get the folder tree structure from Google Drive.

### GET `/api/drive/folder-content`
Get content of a specific Drive folder.

### POST `/api/drive/create-folder`
Create a folder in Google Drive.

**Request:**
```json
{
  "folder_name": "New Folder",
  "parent_id": "PARENT_FOLDER_ID"
}
```

**Response:**
```json
{
  "ok": true,
  "folder_id": "FOLDER_ID",
  "folder_name": "New Folder"
}
```

### POST `/api/drive/create-folder-structure`
Create a nested folder structure in Google Drive.

### POST `/api/drive/create-doc`
Create a Google Doc.

### POST `/api/drive/append-doc`
Append text to an existing Google Doc.

### POST `/api/drive/upload-clip`
Upload a clip file to Google Drive.

### POST `/api/drive/upload-clip-simple`
Simplified clip upload to Google Drive.

### POST `/api/drive/download-and-upload-clip`
Download a clip from URL and upload to Google Drive.

### GET `/api/drive/groups`
Get available Drive groups.

---

## NLP

### POST `/api/nlp/extract-moments`
Extract key moments from VTT subtitle content.

**Request:**
```json
{
  "vtt_content": "WEBVTT\n\n00:00:01.000 --> 00:00:05.000\nHello world...",
  "keywords": ["spider", "nature"],
  "topic": "Nature Documentary",
  "max_moments": 5
}
```

**Response:**
```json
{
  "ok": true,
  "moments": [...],
  "count": 3,
  "total_segments": 50
}
```

### POST `/api/nlp/analyze`
Perform complete text analysis (tokenization, keyword extraction, readability).

**Request:**
```json
{
  "text": "Text to analyze..."
}
```

**Response:**
```json
{
  "ok": true,
  "analysis": {
    "word_count": 100,
    "sentence_count": 5,
    "avg_word_length": 4.5,
    "unique_words": 80,
    "keywords": [...],
    "readability": 65.2
  }
}
```

### POST `/api/nlp/keywords`
Extract keywords from text.

**Query Parameters:**
- `top_n` (optional): Number of keywords (default: 10)

**Request:**
```json
{
  "text": "Text to extract keywords from..."
}
```

**Response:**
```json
{
  "ok": true,
  "keywords": [
    { "word": "spider", "score": 15.2 },
    { "word": "nature", "score": 12.8 }
  ],
  "count": 2
}
```

### POST `/api/nlp/summarize`
Summarize text using extractive summarization.

**Request:**
```json
{
  "text": "Long text to summarize...",
  "max_sentences": 3
}
```

**Response:**
```json
{
  "ok": true,
  "summary": "Key sentences extracted...",
  "sentence_count": 3,
  "total_sentences": 20
}
```

### POST `/api/nlp/tokenize`
Tokenize text into words.

**Request:**
```json
{
  "text": "Text to tokenize..."
}
```

**Response:**
```json
{
  "ok": true,
  "tokens": ["text", "tokenize"],
  "tokens_all": ["text", "to", "tokenize"],
  "count": 2,
  "count_all": 3
}
```

### POST `/api/nlp/segment`
Segment text into chunks for parallel processing.

**Request:**
```json
{
  "text": "Long text to segment...",
  "target_words_per_segment": 800
}
```

**Response:**
```json
{
  "ok": true,
  "segments": [...],
  "count": 3,
  "words_per_segment": 800,
  "total_words": 2400
}
```

### POST `/api/nlp/entities`
Extract entities from text using NLP + Ollama.

**Request:**
```json
{
  "text": "Script text...",
  "entity_count": 12,
  "target_words_per_segment": 800
}
```

**Response:**
```json
{
  "ok": true,
  "total_segments": 3,
  "entity_count_per_segment": 12,
  "total_entities": 36,
  "segment_entities": [...]
}
```

---

## Scraper

### POST `/api/scraper/search`
Search for stock clips using the Node.js scraper (Artlist, Pixabay, Pexels).

**Request:**
```json
{
  "search_term": "spider",
  "max_pages": 5,
  "source": "artlist",
  "category": "nature"
}
```

**Response:**
```json
{
  "search_term": "spider",
  "source": "artlist",
  "max_pages": 5,
  "clips_found": 50,
  "clips": [
    {
      "source": "artlist",
      "id": 12345,
      "name": "Spider Web Close-up",
      "duration": 12.5,
      "width": 1920,
      "height": 1080,
      "url": "https://artlist.io/...",
      "thumbnail": "https://..."
    }
  ]
}
```

### GET `/api/scraper/stats`
Get scraper statistics.

**Response:**
```json
{
  "scraper": "node-scraper",
  "status": "ready",
  "output_dir": "src/node-scraper/Output",
  "database": "src/node-scraper/artlist_videos.db"
}
```

### POST `/api/scraper/seed`
Seed the scraper database with initial data.

**Response:**
```json
{
  "status": "success",
  "message": "Database seeded successfully",
  "output": "..."
}
```

### GET `/api/scraper/categories`
List available scraper categories.

**Response:**
```json
{
  "categories": "..."
}
```

### POST `/api/scraper/download`
Download pending video clips from the database.

**Request:**
```json
{
  "category": "nature"
}
```

**Response:**
```json
{
  "status": "success",
  "output": "..."
}
```

---

## Dashboard

### GET `/api/dashboard/metrics`
Get dashboard metrics (job and worker statistics).

**Response:**
```json
{
  "ok": true,
  "uptime_seconds": 3600,
  "jobs": {
    "total": 50,
    "pending": 10,
    "running": 5,
    "completed": 30,
    "failed": 5
  },
  "workers": {
    "total": 3,
    "idle": 1,
    "busy": 2,
    "offline": 0
  },
  "timestamp": "2024-01-15T10:30:00Z"
}
```

### GET `/api/dashboard/status`
Get server status and uptime.

**Response:**
```json
{
  "ok": true,
  "status": "running",
  "uptime_seconds": 3600
}
```

### GET `/api/dashboard/overview`
Get overview with recent jobs and active workers.

**Response:**
```json
{
  "ok": true,
  "recent_jobs": [...],
  "active_workers": [...]
}
```

### GET `/api/dashboard/jobs/recent`
Get recent 50 jobs.

### GET `/api/dashboard/workers/summary`
Get worker summary.

---

## Stats

### GET `/api/stats/jobs`
Get job statistics (by status, type, project).

**Response:**
```json
{
  "ok": true,
  "stats": {
    "total": 50,
    "by_status": { "pending": 10, "running": 5, "completed": 30, "failed": 5 },
    "by_type": { "video_generation": 40, "script": 10 },
    "by_project": { "project-a": 20, "project-b": 30 },
    "average_duration_seconds": 120.5
  }
}
```

### GET `/api/stats/workers`
Get worker statistics.

**Response:**
```json
{
  "ok": true,
  "stats": {
    "total": 3,
    "by_status": { "idle": 1, "busy": 2, "offline": 0 },
    "active_jobs": 2,
    "total_disk_free_gb": 150.0,
    "average_disk_free_gb": 50.0
  }
}
```

### GET `/api/stats/performance`
Get performance statistics.

**Response:**
```json
{
  "ok": true,
  "stats": {
    "jobs_per_hour": 12,
    "jobs_per_minute": 0.2,
    "average_process_time_seconds": 120.5,
    "throughput_jobs_per_hour": 12
  }
}
```

### GET `/api/stats/errors`
Get error statistics and recent errors.

**Response:**
```json
{
  "ok": true,
  "stats": {
    "total_errors": 5,
    "recent_errors": [
      {
        "job_id": "job_abc",
        "worker_id": "worker_1",
        "error": "Rust binary not found",
        "timestamp": "2024-01-15T10:30:00Z"
      }
    ]
  }
}
```

---

## Admin

### POST `/api/admin/jobs/pause`
Pause all new job creation.

**Response:**
```json
{
  "ok": true,
  "status": "paused",
  "message": "New job creation is now paused"
}
```

### POST `/api/admin/jobs/resume`
Resume job creation.

**Response:**
```json
{
  "ok": true,
  "status": "resumed",
  "message": "New job creation is now resumed"
}
```

### POST `/api/admin/jobs/:id/pause`
Pause a specific job.

**Response:**
```json
{
  "ok": true,
  "job_id": "job_abc123",
  "status": "paused",
  "message": "Job paused successfully"
}
```

### POST `/api/admin/jobs/:id/resume`
Resume a specific job.

**Response:**
```json
{
  "ok": true,
  "job_id": "job_abc123",
  "status": "pending",
  "message": "Job resumed successfully"
}
```

### POST `/api/admin/workers/:id/restart`
Send restart command to a worker.

**Response:**
```json
{
  "ok": true,
  "worker_id": "worker_abc123",
  "command_id": "cmd_xyz",
  "message": "Restart command sent to worker"
}
```

### POST `/api/admin/workers/:id/update`
Send update command to a worker (download new code).

**Response:**
```json
{
  "ok": true,
  "worker_id": "worker_abc123",
  "command_id": "cmd_xyz",
  "message": "Update command sent to worker"
}
```

---

## Swagger UI

Interactive API documentation is available at:

```
http://localhost:8080/api/docs/index.html
```

---

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `VELOX_HOST` | `0.0.0.0` | Server bind address |
| `VELOX_PORT` | `8080` | Server port |
| `VELOX_LOG_LEVEL` | `info` | Log level (debug, info, warn, error) |
| `VELOX_LOG_FORMAT` | `json` | Log format (json, console) |
| `VELOX_DATA_DIR` | `./data` | Canonical runtime data directory for JSON/SQLite state |
| `VELOX_ENABLE_AUTH` | `false` | Enable authentication middleware |
| `VELOX_ADMIN_TOKEN` | `` | Admin API token |
| `VELOX_MAX_PARALLEL_PER_PROJECT` | `2` | Max parallel jobs per project |
| `VELOX_CLIP_ROOT_FOLDER` | `root` | Google Drive root folder for clips |

---

## Quick Start

```bash
cd src/go-master
go build -o server ./cmd/server
./server

# Health check
curl http://localhost:8080/health

# Create a video
curl -X POST http://localhost:8080/api/video/create-master \
  -H 'Content-Type: application/json' \
  -d '{
    "video_name": "Test Video",
    "source": "Test source",
    "language": "en",
    "duration": 300
  }'
```
