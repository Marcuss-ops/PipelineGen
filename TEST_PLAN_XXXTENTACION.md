# Test Plan: XXXTentacion Long-Form Video Pipeline

**Date:** April 13, 2026  
**Target:** 20-minute music video about XXXTentacion  
**YouTube URL:** https://www.youtube.com/watch?v=EfLSYC0TGhs

---

## 🎯 What We Changed

### 1. **Long Script Support** (>1500 words)
- ✅ Increased `sanitizeInput` limit: 50,000 → 100,000 characters
- ✅ Increased duration max: 30 min → 60 min (1800s → 3600s)
- ✅ Enhanced prompt to explicitly request detailed, long scripts
- ✅ Updated all handler validations (script.go, script_clips.go, video_creation.go)

### 2. **Stock Fallback with YouTube Download**
- ✅ Added `topic` and `stockFallbackCount` parameters to `ScriptClipsService`
- ✅ Added `uploadToDriveWithTopic()` method - creates `Stock/{TopicName}/` folder structure
- ✅ Added `downloadMultipleClipsFromYouTube()` method for bulk fallback downloads
- ✅ When no existing stock found → searches YouTube → downloads 20 clips → uploads to `Stock/XXXTentacion/`

### 3. **Drive Folder Organization**
- ✅ Clips now uploaded to `Stock/{Topic}/` (e.g., `Stock/XXXTentacion/`)
- ✅ Returns full Drive folder link for timestamp section

### 4. **Voiceover**
- ✅ Already implemented in `/api/voiceover/generate`
- ✅ Saves to `/tmp/voiceovers/` directory
- ✅ Italian language default for XXXTentacion script

---

## 🧪 Test Sequence

### Prerequisites
```bash
# Ensure services are running
cd /home/pierone/Pyt/VeloxEditing/refactored/src/go-master
go build -o server ./cmd/server && ./server

# Check health
curl http://localhost:8080/health
curl http://localhost:8080/api/voiceover/health
```

---

### Test 1: Long Script Generation (20 min video)

**Endpoint:** `POST /api/script/generate`

```bash
curl -X POST http://localhost:8080/api/script/generate \
  -H "Content-Type: application/json" \
  -d '{
    "title": "XXXTentacion - La Storia Completa",
    "source_text": "XXXTentacion, nato Jahseh Dwayne Ricardo Onfroy, è stato un rapper, cantante e cantautore americano...",
    "language": "italian",
    "duration": 1200,
    "model": "gemma3:4b"
  }'
```

**Expected:**
- Script length: ~2,800 words (1200s * 140 wpm / 60)
- Response includes `word_count`, `est_duration`, `script` text
- Script should be detailed and comprehensive

**Validation:**
```bash
# Check word count in response
# Should be > 1500 words for a 20-minute video
```

---

### Test 2: Script with Clips + Stock Fallback (Main Pipeline)

**Endpoint:** `POST /api/script/generate-with-clips`

This is the **KEY TEST** - it will:
1. Generate long script from source text
2. Extract entities (Nomi Speciali, Frasi Importanti, Parole Importanti)
3. For each entity:
   - Search existing stock
   - If NOT found → Download 20 clips from YouTube
   - Upload to `Stock/XXXTentacion/` folder on Drive
4. Return complete mapping with Drive links

```bash
curl -X POST http://localhost:8080/api/script/generate-with-clips \
  -H "Content-Type: application/json" \
  -d '{
    "title": "XXXTentacion",
    "source_text": "XXXTentacion è nato a Plantation, Florida, nel 1998. La sua carriera musicale è iniziata su SoundCloud...",
    "language": "italian",
    "duration": 1200,
    "entity_count_per_segment": 12
  }'
```

**Expected Response Structure:**
```json
{
  "ok": true,
  "script": "... long script text ...",
  "word_count": 2800,
  "est_duration": 1200,
  "segments": [
    {
      "segment_index": 0,
      "text": "...",
      "start_time": "00:00:00",
      "end_time": "00:00:20",
      "entities": {
        "nomi_speciali": ["XXXTentacion", "Florida", "SoundCloud"],
        "frasi_importanti": ["...", "..."],
        "parole_importanti": ["...", "..."]
      },
      "clip_mappings": [
        {
          "entity": "XXXTentacion",
          "search_query_en": "XXXTentacion",
          "clip_found": true,
          "clip_status": "downloaded_and_uploaded",
          "youtube_url": "https://youtube.com/watch?v=...",
          "drive_url": "https://drive.google.com/file/d/...",
          "drive_file_id": "..."
        }
      ]
    }
  ],
  "total_clips_found": 20,
  "total_clips_missing": 0
}
```

**What to Verify:**
1. ✅ Script is long (>1500 words)
2. ✅ Multiple segments extracted
3. ✅ Entities extracted correctly
4. ✅ Clips downloaded from YouTube (check `clip_status: "downloaded_and_uploaded"`)
5. ✅ Drive URLs point to `Stock/XXXTentacion/` folder
6. ✅ YouTube URLs are valid and accessible

---

### Test 3: Voiceover Generation

**Endpoint:** `POST /api/voiceover/generate`

```bash
# First get the script from Test 2, then:
curl -X POST http://localhost:8080/api/voiceover/generate \
  -H "Content-Type: application/json" \
  -d '{
    "text": "[SCRIPT FROM TEST 2]",
    "language": "it"
  }'
```

**Expected:**
```json
{
  "ok": true,
  "file_name": "voiceover_1234567890_Italian_it-IT-GiuseppeNeural.mp3",
  "file_path": "/tmp/voiceovers/voiceover_1234567890_Italian_it-IT-GiuseppeNeural.mp3",
  "duration": 1200,
  "word_count": 2800,
  "voice": "it-IT-GiuseppeNeural",
  "language": "it"
}
```

**Verify:**
```bash
# Check file exists
ls -lh /tmp/voiceovers/voiceover_*.mp3

# Play to verify (optional)
ffplay /tmp/voiceovers/voiceover_*.mp3
```

---

### Test 4: Stock Orchestrator (Alternative Pipeline)

**Endpoint:** `POST /api/stock/orchestrate`

This tests the YouTube → Download → Drive pipeline independently:

```bash
curl -X POST http://localhost:8080/api/stock/orchestrate \
  -H "Content-Type: application/json" \
  -d '{
    "query": "XXXTentacion interview",
    "max_videos": 20,
    "quality": "best",
    "extract_entities": true,
    "upload_to_drive": true,
    "create_folders": true,
    "folder_structure": "Stock Videos/XXXTentacion"
  }'
```

**Expected:**
- 20 videos downloaded from YouTube
- Uploaded to `Stock Videos/XXXTentacion/` on Drive
- Entity extraction from video titles

---

### Test 5: Full Video Creation Pipeline

**Endpoint:** `POST /api/video/create-master`

This is the complete end-to-end pipeline:

```bash
curl -X POST http://localhost:8080/api/video/create-master \
  -H "Content-Type: application/json" \
  -d '{
    "video_name": "XXXTentacion - La Storia",
    "project_name": "XXXTentacion",
    "source": "XXXTentacion è nato a Plantation, Florida...",
    "language": "it",
    "duration": 1200,
    "entity_count": 12,
    "skip_gdocs": false
  }'
```

**Expected Flow:**
1. Script generation (Ollama) → long script
2. Entity extraction → segments with entities
3. Voiceover generation → MP3 file in `/tmp/voiceovers/`
4. Video job creation → dispatched to worker/Rust binary

---

## 🔍 Debugging & Monitoring

### Check Logs
```bash
# Watch server logs
tail -f /home/pierone/Pyt/VeloxEditing/refactored/src/go-master/server.log

# Or check systemd journal if running as service
journalctl -u veloxediting -f
```

### Check Drive Folders
```bash
# Check if Stock/XXXTentacion folder was created
curl http://localhost:8080/api/drive/folders-tree
```

### Check Downloaded Clips
```bash
# Check clip index
curl http://localhost:8080/api/clip/index/stats

# Search for XXXTentacion clips
curl -X POST http://localhost:8080/api/clip/index/search \
  -H "Content-Type: application/json" \
  -d '{"query": "XXXTentacion"}'
```

### Check Ollama
```bash
# Verify Ollama is running
curl http://localhost:11434/api/tags

# Check gemma3:4b model available
curl http://localhost:11434/api/tags | jq '.models[] | select(.name | contains("gemma3"))'
```

---

## ⚠️ Common Issues & Solutions

### Issue 1: Script Too Short (<500 words)
**Cause:** Ollama model not following prompt instructions  
**Solution:** 
- Try different model (e.g., `llama3:8b` if available)
- Increase duration parameter
- Add more detailed source text

### Issue 2: Clip Downloads Fail
**Cause:** yt-dlp not found or network issues  
**Solution:**
```bash
# Check yt-dlp
which yt-dlp
yt-dlp --version

# Update if needed
pip install --upgrade yt-dlp
```

### Issue 3: Drive Upload Fails
**Cause:** Token expired or permissions  
**Solution:**
```bash
# Check Drive health
curl http://localhost:8080/api/drive/health

# Re-authenticate if needed
# Follow OAuth flow to refresh token.json
```

### Issue 4: Voiceover Too Fast/Slow
**Cause:** Speech rate mismatch  
**Solution:** Adjust duration parameter or use different EdgeTTS voice

---

## 📊 Success Criteria

✅ **Script Generation:**
- Word count > 1500 for 20-minute video
- Script covers all key facts about XXXTentacion
- Natural, conversational tone

✅ **Stock Fallback:**
- 15-20 clips downloaded from YouTube
- All clips uploaded to `Stock/XXXTentacion/` on Drive
- Drive folder link returned in response

✅ **Voiceover:**
- MP3 file generated in `/tmp/voiceovers/`
- Duration matches script length (~1200s)
- Italian language, clear narration

✅ **Drive Organization:**
- `Stock/XXXTentacion/` folder created
- All clips properly named and organized
- Folder links accessible and shareable

---

## 🚀 Next Steps After Testing

1. **If all tests pass:**
   - Update documentation with new endpoints
   - Consider adding batch processing for multiple topics
   - Add progress tracking for long-running downloads

2. **If issues found:**
   - Check logs for specific errors
   - Test individual components in isolation
   - Adjust parameters (duration, entity count, etc.)

3. **Production deployment:**
   - Build optimized binary
   - Update systemd service file
   - Monitor resource usage during long downloads

---

## 📝 Notes

- **Topic:** XXXTentacion (music/rapper)
- **Expected entities:** XXXTentacion, Florida, SoundCloud, rap, hip-hop, Jahseh Onfroy, etc.
- **Stock categories:** Music, interviews, performance, biography
- **Language:** Italian (default voice: it-IT-GiuseppeNeural)
- **Target duration:** 20 minutes (1200 seconds)
- **Expected script length:** ~2,800 words
- **Expected clips:** 20 per entity (fallback from YouTube)

Good luck! 🎵
