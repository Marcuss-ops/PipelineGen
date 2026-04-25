# Script + Clips End-to-End Testing Guide

## Overview

The new `/api/script/generate-with-clips` endpoint implements a **complete automated pipeline**:

```
Source Text → Script Generation → Entity Extraction → Clip Search → Download → Drive Upload
```

## What It Does

1. **Generates a video script** from your source text using Ollama (AI)
2. **Segments the script** into time-based chunks (~20 seconds each)
3. **Extracts entities** from each segment (people, places, things, keywords)
4. **Searches for stock clips** on YouTube for each entity
5. **Downloads the best clip** using yt-dlp
6. **Uploads to Google Drive** with proper folder structure
7. **Returns complete mapping**: script segment → entities → clip URLs

---

## API Endpoint

### `POST /api/script/generate-with-clips`

**Request:**
```json
{
  "source_text": "Elon Musk ha fondato Tesla nel 2003 per accelerare il trasporto sostenibile. Oggi Tesla è leader mondiale nelle auto elettriche.",
  "title": "Tesla Revolution",
  "duration": 60,
  "language": "italian",
  "tone": "professional",
  "model": "gemma3:4b",
  "entity_count_per_segment": 12
}
```

**Response:**
```json
{
  "ok": true,
  "script": "Elon Musk ha fondato Tesla nel 2003...",
  "word_count": 142,
  "est_duration": 60,
  "model": "gemma3:4b",
  "segments": [
    {
      "segment_index": 0,
      "text": "Elon Musk ha fondato Tesla nel 2003...",
      "start_time": "00:00:00",
      "end_time": "00:00:10",
      "entities": {
        "frasi_importanti": ["Elon Musk ha fondato Tesla"],
        "nomi_speciali": ["Elon Musk", "Tesla"],
        "parole_importanti": ["tecnologia", "futuro"],
        "entity_senza_testo": {}
      },
      "clip_mappings": [
        {
          "entity": "Elon Musk",
          "search_query_en": "Elon Musk",
          "clip_found": true,
          "clip_status": "downloaded_and_uploaded",
          "youtube_url": "https://youtube.com/watch?v=abc123",
          "drive_url": "https://drive.google.com/file/d/xyz789",
          "drive_file_id": "xyz789"
        }
      ]
    }
  ],
  "total_clips_found": 5,
  "total_clips_missing": 2,
  "processing_time_seconds": 45.3
}
```

---

## Testing the Endpoint

### **Prerequisites**

Before testing, ensure these services are running:

1. **Ollama** (local AI server)
   ```bash
   # Check if Ollama is running
   curl http://localhost:11434/api/tags
   
   # If not running, start it:
   ollama serve
   ```

2. **yt-dlp** (video downloader)
   ```bash
   # Check installation
   yt-dlp --version
   
   # Install if missing:
   sudo curl -L https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp -o /usr/local/bin/yt-dlp
   sudo chmod +x /usr/local/bin/yt-dlp
   ```

3. **Google Drive Credentials**
   - `credentials.json` and `token.json` must exist in `src/go-master/`
   - Drive API must be enabled in Google Cloud Console

4. **Go Master Server** running on port 8080
   ```bash
   cd src/go-master
   ./start.sh
   ```

---

### **Test 1: Basic Functionality Test**

Test with a simple script about Tesla:

```bash
curl -X POST http://localhost:8080/api/script/generate-with-clips \
  -H "Content-Type: application/json" \
  -d '{
    "source_text": "Tesla è stata fondata da Elon Musk nel 2003. Le auto elettriche Tesla stanno rivoluzionando il mercato.",
    "title": "Tesla Revolution",
    "duration": 60,
    "language": "italian",
    "tone": "professional"
  }'
```

**Expected behavior:**
- ✅ Script generated (140-160 words)
- ✅ 2-3 segments created
- ✅ Entities extracted (Elon Musk, Tesla, auto elettriche)
- ✅ Clips searched on YouTube
- ✅ Clips downloaded and uploaded to Drive
- ✅ Response contains complete mapping

---

### **Test 2: Multi-Entity Test**

Test with multiple entities to verify parallel clip searches:

```bash
curl -X POST http://localhost:8080/api/script/generate-with-clips \
  -H "Content-Type: application/json" \
  -d '{
    "source_text": "Apple ha lanciato il nuovo iPhone con chip A17. Samsung risponde con Galaxy S24. Google presenta Pixel 8 con intelligenza artificiale.",
    "title": "Smartphone Wars 2024",
    "duration": 90,
    "language": "italian",
    "entity_count_per_segment": 15
  }'
```

**Expected behavior:**
- ✅ Entities: Apple, iPhone, Samsung, Galaxy, Google, Pixel
- ✅ Multiple clips found and uploaded
- ✅ Each segment has 3-5 clip mappings

---

### **Test 3: Error Handling Test**

Test validation errors:

```bash
# Missing source_text
curl -X POST http://localhost:8080/api/script/generate-with-clips \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Test",
    "duration": 60
  }'
```

**Expected response:**
```json
{
  "ok": false,
  "error": "source_text is required"
}
```

---

### **Test 4: Long Script Test**

Test with a longer script (5 minutes):

```bash
curl -X POST http://localhost:8080/api/script/generate-with-clips \
  -H "Content-Type: application/json" \
  -d '{
    "source_text": "L'intelligenza artificiale sta trasformando ogni settore. Dal healthcare alla finanza, dalle auto autonome alla robotica. OpenAI ha sviluppato GPT-4, Google ha Gemini, Meta ha LLaMA. Il futuro è ora.",
    "title": "AI Revolution",
    "duration": 300,
    "language": "italian",
    "entity_count_per_segment": 10
  }'
```

**Expected behavior:**
- ✅ 10-15 segments created
- ✅ 20-40 entities extracted
- ✅ 15-30 clips found/downloaded
- ✅ Processing time: 2-5 minutes

---

## Monitoring Progress

### **Check Server Logs**

```bash
tail -f src/go-master/data/logs/server.log
```

Look for these log lines:
```
INFO  Starting script generation with clips
INFO  Script generated, starting entity extraction
INFO  Entity extraction completed  total_segments=3  total_entities=15
INFO  Searching clip for entity  entity=Tesla  query_en=Tesla
INFO  Clip downloaded and uploaded to Drive  entity=Tesla  drive_url=...
INFO  Script generation with clips completed  segments=3  clips_found=12  clips_missing=3
```

### **Check Google Drive**

Navigate to Drive → "Stock Clips" folder to see uploaded clips:
```
Stock Clips/
├── clip_Elon_Musk_1234567890.mp4
├── clip_Tesla_1234567891.mp4
└── clip_intelligenza_artificiale_1234567892.mp4
```

---

## Troubleshooting

### **Error: "ollama request failed"**

**Cause:** Ollama is not running or model not downloaded.

**Fix:**
```bash
ollama serve
ollama pull gemma3:4b
```

---

### **Error: "yt-dlp failed"**

**Cause:** yt-dlp not installed or YouTube blocking downloads.

**Fix:**
```bash
# Update yt-dlp to latest version
sudo yt-dlp --update

# Test manually:
yt-dlp --version
```

---

### **Error: "Failed to create Drive folder"**

**Cause:** Google Drive credentials invalid or expired.

**Fix:**
```bash
# Re-authenticate
rm src/go-master/token.json
# Restart server and re-authenticate via browser
```

---

### **Error: "No downloaded file found"**

**Cause:** yt-dlp completed but file not found in expected location.

**Fix:**
- Check `/tmp/velox/downloads/` for files
- Ensure sufficient disk space
- Check yt-dlp output in logs for errors

---

## Running Automated Tests

### **Unit + Integration Tests**

```bash
cd src/go-master

# Run script+clips tests only
go test ./tests/integration -run TestScriptClipsEndpoint -v

# Run all tests
go test ./... -v

# Run with race detection
go test -race ./tests/integration -run TestScriptClipsEndpoint
```

### **Test Coverage**

```bash
go test -coverprofile=coverage.out ./internal/service/scriptclips
go tool cover -html=coverage.out
```

---

## Performance Benchmarks

| Script Duration | Segments | Entities | Clips Found | Processing Time |
|----------------|----------|----------|-------------|-----------------|
| 30 seconds     | 1-2      | 8-12     | 5-10        | 30-60 seconds   |
| 60 seconds     | 2-3      | 15-25    | 10-20       | 1-2 minutes     |
| 5 minutes      | 10-15    | 50-80    | 30-60       | 3-8 minutes     |

**Note:** Processing time depends on:
- Ollama generation speed (GPU vs CPU)
- YouTube search/download speed
- Google Drive upload speed
- Number of entities per segment

---

## Next Steps / Future Enhancements

1. **Add Artlist integration** - Search Artlist stock library in addition to YouTube
2. **Add clip quality scoring** - Rate clips by relevance and quality
3. **Parallel downloads** - Download multiple clips simultaneously
4. **Resume failed downloads** - Retry logic for failed clip downloads
5. **Cache clips on Drive** - Reuse existing clips instead of re-downloading
6. **Add clip trimming** - Trim downloaded clips to exact segment duration
7. **Add fallback sources** - Pexels, Pixabay, Coverr as alternative stock sources

---

## Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│                      API Request                                │
│            POST /api/script/generate-with-clips                 │
└────────────────────────┬────────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────────┐
│              ScriptClipsService.GenerateScriptWithClips         │
│                                                                 │
│  Step 1: Generate Script                                        │
│  ┌───────────────────────────────────────────────────────────┐ │
│  │  Ollama.Generator.GenerateFromText()                      │ │
│  │  → Script: "Elon Musk founded Tesla in 2003..."          │ │
│  │  → Word Count: 142                                       │ │
│  │  → Est Duration: 60s                                     │ │
│  └───────────────────────────────────────────────────────────┘ │
│                                                                 │
│  Step 2: Extract Entities                                       │
│  ┌───────────────────────────────────────────────────────────┐ │
│  │  EntityService.AnalyzeScript()                            │ │
│  │  → Segment 0: "Elon Musk founded Tesla..." (0-10s)       │ │
│  │  → Segment 1: "...revolutionizing transport..." (10-30s) │ │
│  │  → Entities: [Elon Musk, Tesla, auto elettriche]         │ │
│  └───────────────────────────────────────────────────────────┘ │
│                                                                 │
│  Step 3: Find/Download Clips (per segment)                     │
│  ┌───────────────────────────────────────────────────────────┐ │
│  │  For each entity:                                         │ │
│  │    1. Translate IT→EN (ClipSearchTranslator)             │ │
│  │    2. Search YouTube (StockManager.SearchYouTube)        │ │
│  │    3. Download best clip (yt-dlp)                        │ │
│  │    4. Upload to Drive (drive.Client.UploadFile)          │ │
│  │    5. Return mapping {entity → clip URL}                 │ │
│  └───────────────────────────────────────────────────────────┘ │
│                                                                 │
│  Step 4: Return Complete Response                              │
│  ┌───────────────────────────────────────────────────────────┐ │
│  │  {                                                        │ │
│  │    "script": "...",                                       │ │
│  │    "segments": [                                          │ │
│  │      {                                                    │ │
│  │        "text": "...",                                     │ │
│  │        "start_time": "00:00:00",                          │ │
│  │        "end_time": "00:00:10",                            │ │
│  │        "entities": {...},                                 │ │
│  │        "clip_mappings": [                                 │ │
│  │          {                                                │ │
│  │            "entity": "Elon Musk",                         │ │
│  │            "clip_status": "downloaded_and_uploaded",      │ │
│  │            "youtube_url": "...",                          │ │
│  │            "drive_url": "..."                             │ │
│  │          }                                                │ │
│  │        ]                                                  │ │
│  │      }                                                    │ │
│  │    ],                                                     │ │
│  │    "total_clips_found": 5                                 │ │
│  │  }                                                        │ │
│  └───────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────┘
```

---

## Files Modified/Created

| File | Purpose |
|------|---------|
| `internal/service/scriptclips/service.go` | **NEW** - Core orchestration service |
| `internal/api/handlers/script_clips.go` | **NEW** - HTTP handler |
| `internal/api/routes.go` | Modified - Added ScriptClips handler |
| `internal/api/handlers/drive.go` | Modified - Added GetDriveClient() |
| `cmd/server/main.go` | Modified - Service initialization |
| `tests/integration/script_clips_test.go` | **NEW** - Integration tests |

---

## Summary

✅ **Script Generation**: Generates professional video scripts via Ollama AI  
✅ **Entity Extraction**: Automatically identifies key entities per segment  
✅ **Clip Search**: Searches YouTube for relevant stock clips  
✅ **Automatic Download**: Downloads best clips using yt-dlp  
✅ **Drive Upload**: Uploads clips to Google Drive with proper folder structure  
✅ **Complete Mapping**: Returns script + entities + clip URLs in one response  
✅ **Error Handling**: Graceful degradation if clips not found  
✅ **Testing**: Full integration test suite included  

**Status: READY FOR TESTING** 🚀
