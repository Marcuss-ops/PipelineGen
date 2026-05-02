# Artlist Pipeline Workflow

This document explains the complete Artlist pipeline for searching, downloading, processing, and uploading stock footage.

## Overview

The Artlist pipeline automates the entire workflow of:
1. Searching Artlist for stock clips based on tags
2. Downloading clips via yt-dlp
3. Processing clips with FFmpeg (trimming, resizing)
4. Uploading processed clips to Google Drive
5. Storing metadata in the database

## Job Type: `artlist.run`

## Payload Structure

**Input Payload:**
```json
{
  "tag": "technology",
  "limit": 10,
  "output_dir": "/path/to/raw",
  "processed_dir": "/path/to/processed",
  "drive_folder_id": "folder123",
  "clip_duration": 7,
  "output_width": 1920,
  "output_height": 1080,
  "output_fps": 30
}
```

**Result Payload:**
```json
{
  "processed": 10,
  "clips": [
    {
      "id": "clip-001",
      "title": "Technology Abstract",
      "drive_url": "https://drive.google.com/file/d/...",
      "local_path": "/path/to/processed/clip-001.mp4",
      "duration": 7.0
    }
  ]
}
```

## Pipeline Steps

### 1. Search Artlist (Adapter: ArtlistAdapter)
- Calls the Artlist scraper/node script
- Returns a list of ClipCandidates with URLs

**Events:**
- `job_started`
- `search_started`
- `search_finished`

### 2. Download Clips (Adapter: YTDLPAdapter)
- For each clip, download via yt-dlp
- Save to output_dir

**Events:**
- `download_started` (per clip)
- `download_finished` (per clip)

### 3. Process Clips (Adapter: FFmpegAdapter)
- Trim clips to specified duration
- Resize to output dimensions
- Set output FPS
- Save to processed_dir

**Events:**
- `ffmpeg_started` (per clip)
- `ffmpeg_finished` (per clip)

### 4. Upload to Drive (Adapter: DriveAdapter)
- Upload each processed clip to Google Drive
- Save the Drive File ID and URL

**Events:**
- `upload_started` (per clip)
- `upload_finished` (per clip)

### 5. Save Metadata
- Store clip metadata in database
- Update job result with all clip info

**Events:**
- `job_succeeded`

## Error Handling

If any step fails:
- Job is marked as `failed`
- Error message is saved
- If attempts < max_attempts, job is retried
- On retry, completed steps are skipped (idempotent)

## Job Handler Implementation

```go
type ArtlistRunJobHandler struct {
	artlist ArtlistAdapter
	ytdlp   YTDLPAdapter
	ffmpeg  FFmpegAdapter
	drive   DriveAdapter
	store   ArtlistStore
}

func (h *ArtlistRunJobHandler) Handle(ctx context.Context, payload ArtlistRunPayload) (*ArtlistRunResult, error) {
	clips, err := h.artlist.Search(ctx, SearchInput{
		Query: payload.Tag,
		Limit: payload.Limit,
	})
	if err != nil {
		return nil, err
	}

	for _, clip := range clips {
		downloaded, err := h.ytdlp.Download(ctx, DownloadInput{
			URL:      clip.URL,
			OutputDir: payload.OutputDir,
		})
		if err != nil {
			return nil, err
		}

		processed, err := h.ffmpeg.ProcessClip(ctx, ProcessClipInput{
			InputPath:  downloaded.LocalPath,
			OutputPath: payload.ProcessedDir,
			Duration:   payload.ClipDuration,
			Width:      payload.OutputWidth,
			Height:     payload.OutputHeight,
			FPS:        payload.OutputFPS,
		})
		if err != nil {
			return nil, err
		}

		uploaded, err := h.drive.Upload(ctx, UploadInput{
			LocalPath: processed.OutputPath,
			FolderID:  payload.DriveFolderID,
			Name:      clip.Title,
		})
		if err != nil {
			return nil, err
		}

		h.store.SaveClip(ctx, clip, uploaded.URL)
	}

	return &ArtlistRunResult{Processed: len(clips)}, nil
}
```

## API Endpoints

### Start Artlist Pipeline
`POST /api/artlist/run`

### Check Job Status
`GET /api/jobs/:id`

### Get Job Events
`GET /api/jobs/:id/events`

### Retry Failed Job
`POST /api/jobs/:id/retry`
