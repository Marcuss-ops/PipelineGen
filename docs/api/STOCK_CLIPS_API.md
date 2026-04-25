# Studio App - Stock & Clips API

Base URL: `http://localhost:5000`

---

## YouTube Endpoints

### 1. Search Stock Footage
```bash
curl -X POST http://localhost:5000/api/stock/search-youtube \
  -H "Content-Type: application/json" \
  -d '{"subject": "elon musk", "max_results": 10}'
```

### 2. Search Interviews
```bash
curl -X POST http://localhost:5000/api/stock/search-youtube-interviews \
  -H "Content-Type: application/json" \
  -d '{"subject": "elon musk", "max_results": 5}'
```

### 3. Get Subtitles (VTT)
```bash
# As JSON
curl "http://localhost:5000/api/subtitles?youtube_url=VIDEO_URL&language=en"

# Download file
curl "http://localhost:5000/api/subtitles/download?youtube_url=VIDEO_URL&language=en" -o subtitles.vtt
```

---

## NLP Endpoints

### 4. Extract Key Moments from VTT
```bash
curl -X POST http://localhost:5000/api/nlp/extract-moments \
  -H "Content-Type: application/json" \
  -d '{"vtt_content": "WEBVTT...", "topic": "elon musk", "max_moments": 10}'
```

### 5. Extract from YouTube URL
```bash
curl -X POST http://localhost:5000/api/nlp/extract-moments-from-url \
  -H "Content-Type: application/json" \
  -d '{"youtube_url": "VIDEO_URL", "topic": "elon musk", "max_moments": 5, "language": "en"}'
```

---

## Drive Endpoints

### 6. List Groups
```bash
curl http://localhost:5000/api/drive/groups
```

### 7. Create Folder Structure
```bash
curl -X POST http://localhost:5000/api/drive/create-folder-structure \
  -H "Content-Type: application/json" \
  -d '{"topic": "elon musk interview", "group": "elon_musk"}'
```

### 8. Upload Clip
```bash
curl -X POST http://localhost:5000/api/drive/upload-clip \
  -H "Content-Type: application/json" \
  -d '{
    "topic": "elon musk interview",
    "video_url": "VIDEO_URL",
    "video_title": "Elon Musk Interview",
    "group": "elon_musk",
    "moments": [
      {"start": "00:11:27", "end": "00:11:31", "text": "...", "duration": 4, "score": 45},
      {"start": "00:12:25", "end": "00:12:28", "text": "...", "duration": 3, "score": 47}
    ]
  }'
```

### 9. Upload Simple Clip
```bash
curl -X POST http://localhost:5000/api/drive/upload-clip-simple \
  -H "Content-Type: application/json" \
  -d '{"topic": "elon musk", "text": "Test clip", "timestamp": "00:15:30"}'
```

---

## Utility

### Health
```bash
curl http://localhost:5000/api/health
```

---

## Complete Workflow

```bash
# 1. Search stock
STOCK=$(curl -s -X POST http://localhost:5000/api/stock/search-youtube \
  -H "Content-Type: application/json" \
  -d '{"subject": "tesla", "max_results": 5}')
echo "$STOCK"

# 2. Get video ID and extract moments
curl -s -X POST http://localhost:5000/api/nlp/extract-moments-from-url \
  -H "Content-Type: application/json" \
  -d '{"youtube_url": "VIDEO_URL", "topic": "tesla", "max_moments": 10}' | jq '.moments'

# 3. Upload to Drive
curl -s -X POST http://localhost:5000/api/drive/upload-clip \
  -H "Content-Type: application/json" \
  -d '{"topic": "tesla", "moments": [{"start": "00:10", "end": "00:15", "text": "...", "duration": 5, "score": 40}]}'
```

## Drive Groups

| Key | Folder |
|-----|--------|
| elon_musk | Elon Musk |
| tech | Tech & AI |
| business | Business |
| interview | Interviews |
| podcast | Podcasts |
| news | News |
| science | Science |
| default | Stock Footage |
