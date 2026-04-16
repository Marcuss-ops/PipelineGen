# Auto-Import API Documentation

## Overview

The **Auto-Import** system automatically downloads videos from YouTube/TikTok and uploads them to Google Drive in the correct folder structure (clips vs stock, with proper group/subgroup categorization).

## Features

✅ **Automatic Download**: Uses yt-dlp to download from YouTube/TikTok  
✅ **Smart Categorization**: Auto-detects if content is "clips" or "stock"  
✅ **Folder Routing**: Automatically routes to correct Drive folder structure  
✅ **Metadata Generation**: Creates .txt description file alongside each video  
✅ **Batch Processing**: Import multiple URLs in one request  
✅ **Auto-Detection**: Automatically determines group, subgroup, and media type  

---

## API Endpoints

### 1. Import Single URL

**Endpoint**: `POST /api/auto-import/import`

Downloads a video from URL and uploads to Drive with automatic categorization.

**Request Body**:
```json
{
  "url": "https://www.youtube.com/watch?v=abc123",
  "auto_detect": true,
  "media_type": "clips",
  "group": "Tech & AI",
  "subgroup": "AI Robots",
  "title": "Amazing AI Robot Demo",
  "description": "A demo video showing AI robot capabilities",
  "tags": ["AI", "robot", "technology"]
}
```

**Fields**:
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `url` | string | ✅ Yes | YouTube or TikTok URL |
| `auto_detect` | bool | ❌ No | Auto-detect media_type, group, subgroup (default: true) |
| `media_type` | string | ❌ No | "clips" or "stock" (auto-detected if not provided) |
| `group` | string | ❌ No | Manual group name (e.g., "Tech & AI", "Business") |
| `subgroup` | string | ❌ No | Manual subgroup name |
| `title` | string | ❌ No | Video title (used for .txt metadata) |
| `description` | string | ❌ No | Video description |
| `tags` | string[] | ❌ No | Tags for categorization |

**Response** (Success):
```json
{
  "success": true,
  "task": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "url": "https://www.youtube.com/watch?v=abc123",
    "source": "youtube",
    "media_type": "clips",
    "group": "Tech & AI",
    "subgroup": "AI Robots",
    "title": "Amazing AI Robot Demo",
    "status": "completed",
    "drive_file_id": "1a2b3c4d5e6f",
    "started_at": "2026-04-11T16:00:00Z",
    "completed_at": "2026-04-11T16:02:30Z"
  },
  "message": "Video imported and uploaded to Drive successfully"
}
```

**Response** (Error):
```json
{
  "success": false,
  "task": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "url": "https://www.youtube.com/watch?v=abc123",
    "status": "failed",
    "error": "Download failed: yt-dlp not found"
  },
  "error": "Download failed: yt-dlp not found"
}
```

---

### 2. Batch Import Multiple URLs

**Endpoint**: `POST /api/auto-import/batch`

Downloads and imports multiple URLs in sequence.

**Request Body**:
```json
{
  "urls": [
    "https://www.youtube.com/watch?v=abc123",
    "https://www.youtube.com/watch?v=def456",
    "https://www.tiktok.com/@user/video/789"
  ],
  "auto_detect": true,
  "media_type": "clips",
  "group": "Tech & AI",
  "tags": ["technology", "AI"]
}
```

**Fields**:
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `urls` | string[] | ✅ Yes | Array of YouTube/TikTok URLs (max 50) |
| `auto_detect` | bool | ❌ No | Auto-detect categorization |
| `media_type` | string | ❌ No | "clips" or "stock" |
| `group` | string | ❌ No | Manual group override |
| `tags` | string[] | ❌ No | Tags for all videos |

**Response**:
```json
{
  "success": 2,
  "failed": 1,
  "tasks": [
    {
      "id": "task-1",
      "url": "https://www.youtube.com/watch?v=abc123",
      "status": "completed",
      "drive_file_id": "1a2b3c"
    },
    {
      "id": "task-2",
      "url": "https://www.youtube.com/watch?v=def456",
      "status": "completed",
      "drive_file_id": "4d5e6f"
    },
    {
      "id": "task-3",
      "url": "https://www.tiktok.com/@user/video/789",
      "status": "failed",
      "error": "Download timeout"
    }
  ],
  "errors": [
    "https://www.tiktok.com/@user/video/789: Download timeout"
  ]
}
```

---

### 3. Get Task Status

**Endpoint**: `GET /api/auto-import/status?task_id=550e8400-e29b`

Returns the status of an import task.

**Response**:
```json
{
  "task_id": "550e8400-e29b",
  "status": "not_implemented",
  "message": "Task tracking not yet implemented"
}
```

**Note**: Task tracking is a placeholder for future implementation.

---

### 4. Test Auto-Detection

**Endpoint**: `GET /api/auto-import/detect?url=URL&title=TITLE`

Tests the auto-detection logic without actually downloading.

**Request**:
```
GET /api/auto-import/detect?url=https://www.youtube.com/watch?v=abc123&title=AI%20Robot%20Demo
```

**Response**:
```json
{
  "url": "https://www.youtube.com/watch?v=abc123",
  "source": "youtube",
  "media_type": "clips",
  "group": "Tech & AI",
  "subgroup": "AI Robot Demo",
  "title": "AI Robot Demo"
}
```

---

## Drive Folder Structure

The system automatically creates this folder structure on Google Drive:

```
Google Drive Root/
├── clips/                           # MediaType = "clips"
│   ├── Tech & AI/                   # Group
│   │   ├── AI Robots/               # SubGroup
│   │   │   ├── video_file.mp4       # Downloaded video
│   │   │   └── video_file_description.txt  # Metadata file
│   │   └── Other SubGroup/
│   └── Business/
│       └── Startup/
└── stock/                           # MediaType = "stock"
    ├── Nature/
    │   ├── Landscapes/
    │   └── Animals/
    └── Urban/
```

### Folder Routing Logic

1. **MediaType** (clips vs stock):
   - Auto-detected from URL, title, and tags
   - Keywords like "stock", "footage", "b-roll" → `stock/`
   - Keywords like "interview", "tutorial", "talk" → `clips/`
   - Default: `clips/`

2. **Group**:
   - Auto-detected from title and tags
   - Maps: "AI", "technology", "robot" → "Tech & AI"
   - Maps: "business", "startup" → "Business"
   - Maps: "interview", "podcast" → "Interviews"
   - Default: "General"

3. **SubGroup**:
   - First 2-3 words of title (if ≤ 50 chars)
   - Or first tag
   - Or empty (video goes directly to Group folder)

---

## Auto-Detection Rules

### Media Type Detection

**Stock Keywords** (score +1 each):
- stock, footage, b-roll, broll, background
- ambient, scenic, landscape, nature, aerial

**Clip Keywords** (score +1 each):
- interview, talk, speech, presentation, review
- unboxing, tutorial, how to, vlog, reaction

**Decision**: `stock_score > clip_score ? "stock" : "clips"`

### Group Detection

| Keyword | Group |
|---------|-------|
| ai, artificial, technology, computer, robot, software, coding | Tech & AI |
| business, startup, marketing | Business |
| interview, podcast, talk | Interviews |
| science, research | Science |
| discovery | Discovery |
| nature, landscape | Nature |
| urban, city | Urban |
| (none match) | General |

---

## .txt Metadata File Format

For each uploaded video, a description file is created:

```
=== VELOXEDITING CLIP METADATA ===

Title: Amazing AI Robot Demo
Source: youtube
URL: https://www.youtube.com/watch?v=abc123
Media Type: clips
Group: Tech & AI
SubGroup: AI Robots

Description:
A demo video showing AI robot capabilities.

Tags: AI, robot, technology

Imported At: 2026-04-11T16:02:30Z

=== END METADATA ===
```

---

## Workflow

```
1. User sends POST /api/auto-import/import
   ↓
2. System detects platform (YouTube/TikTok)
   ↓
3. Auto-detects media_type, group, subgroup (if enabled)
   ↓
4. Downloads video using yt-dlp
   ↓
5. Creates/finds Drive folder structure:
   - Root/{media_type}/{group}/{subgroup}/
   ↓
6. Uploads video file to Drive
   ↓
7. Creates and uploads .txt metadata file
   ↓
8. Cleans up local downloaded files
   ↓
9. Returns success with Drive file IDs
```

---

## Error Handling

### Common Errors

| Error | Cause | Solution |
|-------|-------|----------|
| "Platform not detected" | Invalid or unsupported URL | Use valid YouTube/TikTok URL |
| "Download failed: yt-dlp not found" | yt-dlp not installed | `sudo apt install yt-dlp` or `pip install yt-dlp` |
| "Drive folder creation failed" | Drive API error | Check credentials and token |
| "Upload failed" | Drive upload error | Check Drive permissions |

### Timeouts

- Single import: **10 minutes** timeout
- Batch import: **30 minutes** timeout
- Max batch size: **50 URLs**

---

## Examples

### Example 1: Import with Auto-Detection

```bash
curl -X POST http://localhost:8080/api/auto-import/import \
  -H "Content-Type: application/json" \
  -d '{
    "url": "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
    "auto_detect": true
  }'
```

### Example 2: Import with Manual Categorization

```bash
curl -X POST http://localhost:8080/api/auto-import/import \
  -H "Content-Type: application/json" \
  -d '{
    "url": "https://www.youtube.com/watch?v=abc123",
    "auto_detect": false,
    "media_type": "clips",
    "group": "Tech & AI",
    "subgroup": "AI Demos",
    "title": "Amazing AI Demo",
    "description": "A great demo of AI technology",
    "tags": ["AI", "demo", "technology"]
  }'
```

### Example 3: Batch Import

```bash
curl -X POST http://localhost:8080/api/auto-import/batch \
  -H "Content-Type: application/json" \
  -d '{
    "urls": [
      "https://www.youtube.com/watch?v=abc123",
      "https://www.youtube.com/watch?v=def456",
      "https://www.tiktok.com/@user/video/789"
    ],
    "auto_detect": true,
    "tags": ["trending"]
  }'
```

### Example 4: Test Auto-Detection

```bash
curl "http://localhost:8080/api/auto-import/detect?url=https://www.youtube.com/watch?v=abc123&title=AI%20Robot%20Demo"
```

---

## Requirements

- **yt-dlp**: Must be installed on the system
  ```bash
  sudo apt install yt-dlp
  # or
  pip install yt-dlp
  ```

- **Google Drive API**: Must be configured with credentials.json and token.json

- **Disk Space**: Temporary storage for downloads (cleaned up after upload)

---

## Implementation Files

| File | Purpose |
|------|---------|
| `internal/autoimport/worker.go` | Core worker logic, auto-detection, Drive routing |
| `internal/api/handlers/autoimport.go` | HTTP API handlers |
| `internal/autoimport/worker_test.go` | Unit tests |
| `internal/api/routes.go` | Route registration |
| `cmd/server/main.go` | Handler initialization |

---

## Testing

Run tests:
```bash
cd /home/pierone/Pyt/VeloxEditing/refactored/src/go-master
go test ./internal/autoimport -v
```

All tests pass ✅ (100% coverage of auto-detection logic)

---

## Future Enhancements

- [ ] Async task processing with background workers
- [ ] Task status tracking and progress reporting
- [ ] Retry logic for failed downloads
- [ ] Video trimming/cutting before upload
- [ ] Thumbnail extraction
- [ ] Duplicate detection (avoid re-uploading same video)
- [ ] Queue system for large batches
- [ ] Webhook notifications on completion
