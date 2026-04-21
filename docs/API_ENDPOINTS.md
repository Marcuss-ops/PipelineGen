# API Endpoints Reference

Complete reference of all API endpoints in the VeloxEditing Go backend.

**Base URL:** `http://localhost:8080`

---

## Endpoints by Category

### Health & System
| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/health` | Basic health check |
| GET | `/api/health` | Health check (API group) |
| GET | `/api/status` | Detailed server status |
| GET | `/api/metrics` | Server metrics |

### Video Processing
| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/video/create-master` | Main video creation endpoint |
| GET | `/api/video/health` | Video service health |
| GET | `/api/video/info` | Video service info |

### Script Generation
| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/script/generate` | Generate script from text |
| POST | `/api/script/from-youtube` | Generate script from YouTube (501, not implemented) |
| POST | `/api/script/from-transcript` | Generate script from pre-fetched transcript |
| POST | `/api/script/regenerate` | Regenerate existing script |
| GET | `/api/script/models` | List available Ollama models |
| GET | `/api/script/health` | Ollama health check |
| POST | `/api/script/summarize` | Summarize text |

### Voiceover
| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/voiceover/generate` | Generate voiceover from text |
| GET | `/api/voiceover/languages` | List available languages |
| GET | `/api/voiceover/voices` | List available voices |
| GET | `/api/voiceover/download/:file` | Download a voiceover file |
| GET | `/api/voiceover/health` | Voiceover service health |

### Job Management
| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/jobs` | List jobs (query: status, type, worker_id) |
| POST | `/api/jobs` | Create job |
| GET | `/api/jobs/:id` | Get job |
| PUT | `/api/jobs/:id/status` | Update job status |
| DELETE | `/api/jobs/:id` | Delete job |
| POST | `/api/jobs/:id/assign` | Assign job to worker |
| POST | `/api/jobs/:id/lease` | Renew job lease |

### Worker Management
| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/workers` | List workers |
| POST | `/api/workers/register` | Register worker |
| GET | `/api/workers/:id` | Get worker |
| POST | `/api/workers/:id/heartbeat` | Worker heartbeat |
| GET | `/api/workers/:id/commands` | Get worker commands |
| POST | `/api/workers/:id/commands/:command_id/ack` | Acknowledge command |
| POST | `/api/workers/:id/revoke` | Revoke worker |
| POST | `/api/workers/:id/quarantine` | Quarantine worker |
| POST | `/api/workers/:id/unquarantine` | Unquarantine worker |
| POST | `/api/workers/:id/command` | Send command to worker |
| POST | `/api/worker/poll` | Worker polling endpoint |

### Stock - Projects
| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/stock/projects` | List projects |
| POST | `/api/stock/project` | Create project |
| GET | `/api/stock/project/:name` | Get project |
| DELETE | `/api/stock/project/:name` | Delete project |
| POST | `/api/stock/project/:name/video` | Add video to project |
| GET | `/api/stock/project/:name/videos` | List project videos |
| DELETE | `/api/stock/project/:name/video/:id` | Delete video from project |

### Stock - Search
| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/stock/search` | Search stock videos (query: q, max) |
| GET | `/api/stock/search/youtube` | Search YouTube for stock videos |
| POST | `/api/stock/search/download` | Download a video from search |

### Stock - Processing
| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/stock/process` | Process videos with Rust binary |
| POST | `/api/stock/process/batch` | Batch process videos |
| POST | `/api/stock/studio` | Create studio project |
| GET | `/api/stock/health` | Stock processor health |

### Clip Management
| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/clip/search-folders` | Search clip folders in Drive |
| POST | `/api/clip/read-folder-clips` | Read clips from a folder |
| POST | `/api/clip/suggest` | Suggest clips for a title |
| POST | `/api/clip/create-subfolder` | Create clip subfolder |
| POST | `/api/clip/subfolders` | List subfolders |
| GET | `/api/clip/health` | Clip service health |
| GET | `/api/clip/groups` | Get available clip groups |
| POST | `/api/clip/download` | Download from YouTube to Drive |
| POST | `/api/clip/upload` | Upload local clip to Drive |

### Clip Index & Semantic Suggestions ⭐ NEW
| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/clip/index/scan` | Scan Drive and rebuild clip index |
| GET | `/api/clip/index/stats` | Get index statistics |
| GET | `/api/clip/index/status` | Get indexer status |
| DELETE | `/api/clip/index/clear` | Clear the clip index |
| POST | `/api/clip/index/search` | Search clips with filters |
| GET | `/api/clip/index/clips` | List all indexed clips |
| GET | `/api/clip/index/clips/:id` | Get specific clip by ID |
| POST | `/api/clip/index/suggest/sentence` | ⭐ Get clip suggestions for a sentence |
| POST | `/api/clip/index/suggest/script` | ⭐⭐ Get clip suggestions for entire script |

### Download Management
| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/download` | Download video (YouTube/TikTok) |
| GET | `/api/download/platforms` | List supported platforms |
| GET | `/api/download/library` | List all downloads |
| GET | `/api/download/library/:platform` | List platform downloads |
| DELETE | `/api/download/library/:platform/:videoID` | Delete a download |

### YouTube Integration
| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/youtube/subtitles` | Get video subtitles |
| POST | `/api/youtube/search` | Search YouTube |
| POST | `/api/youtube/search/interviews` | Search interviews |
| POST | `/api/youtube/remote/search` | Remote search |
| POST | `/api/youtube/remote/channel-videos` | Get channel videos |
| POST | `/api/youtube/remote/video-info` | Get video info |
| POST | `/api/youtube/remote/thumbnail` | Get thumbnail |
| POST | `/api/youtube/remote/trending` | Get trending videos |
| POST | `/api/youtube/remote/channel-analytics` | Get channel analytics |
| POST | `/api/youtube/remote/related-videos` | Get related videos |
| POST | `/api/youtube/stock/search` | Stock search on YouTube |

### Google Drive
| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/drive/folders-tree` | Get folder tree |
| GET | `/api/drive/folder-content` | Get folder content |
| POST | `/api/drive/create-folder` | Create folder |
| POST | `/api/drive/create-folder-structure` | Create folder structure |
| POST | `/api/drive/create-doc` | Create Google Doc |
| POST | `/api/drive/append-doc` | Append to Google Doc |
| POST | `/api/drive/upload-clip` | Upload clip |
| POST | `/api/drive/upload-clip-simple` | Simple clip upload |
| POST | `/api/drive/download-and-upload-clip` | Download and upload |
| GET | `/api/drive/groups` | Get Drive groups |

### NLP
| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/nlp/extract-moments` | Extract moments from VTT |
| POST | `/api/nlp/analyze` | Full text analysis |
| POST | `/api/nlp/keywords` | Extract keywords |
| POST | `/api/nlp/summarize` | Summarize text |
| POST | `/api/nlp/tokenize` | Tokenize text |
| POST | `/api/nlp/segment` | Segment text into chunks |
| POST | `/api/nlp/entities` | Extract entities |

### Scraper
| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/scraper/search` | Search clips (Artlist/Pixabay/Pexels) |
| GET | `/api/scraper/stats` | Scraper statistics |
| POST | `/api/scraper/seed` | Seed database |
| GET | `/api/scraper/categories` | List categories |
| POST | `/api/scraper/download` | Download pending clips |

### Dashboard
| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/dashboard/metrics` | Dashboard metrics |
| GET | `/api/dashboard/status` | Server status |
| GET | `/api/dashboard/overview` | Overview with recent jobs |
| GET | `/api/dashboard/jobs/recent` | Recent 50 jobs |
| GET | `/api/dashboard/workers/summary` | Worker summary |

### Stats
| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/stats/jobs` | Job statistics |
| GET | `/api/stats/workers` | Worker statistics |
| GET | `/api/stats/performance` | Performance stats |
| GET | `/api/stats/errors` | Error statistics |

### Admin
| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/admin/jobs/pause` | Pause all new jobs |
| POST | `/api/admin/jobs/resume` | Resume all new jobs |
| POST | `/api/admin/jobs/:id/pause` | Pause specific job |
| POST | `/api/admin/jobs/:id/resume` | Resume specific job |
| POST | `/api/admin/workers/:id/restart` | Restart worker |
| POST | `/api/admin/workers/:id/update` | Update worker |

### Documentation
| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/docs/*` | Swagger UI |

---

## Summary

- **Total endpoints:** ~100 (9 new clip indexing endpoints)
- **New in this refactoring:** 
  - `/api/clip/index/*` (9 endpoints) - Clip indexing and semantic suggestions
  - `/api/download/*` (5 endpoints) - Download management
  - `/api/scraper/*` (5 endpoints) - Web scraper
  - `/api/dashboard/*` (5 endpoints) - Dashboard metrics
  - `/api/stats/*` (4 endpoints) - Statistics
  - `/api/admin/*` (6 endpoints) - Admin controls
  - `/api/nlp/*` (7 endpoints) - NLP processing
- **Updated endpoints:** All stock routes now split across `/api/stock/projects`, `/api/stock/search`, and `/api/stock/process`
- **Note:** The old Python-based endpoints (`/api/script-from-youtube`, `/api/script-simple`, etc. on port 5000) have been replaced by Go-based equivalents on port 8080

---

## 🌐 Remote Access

The server listens on `0.0.0.0:8080`, making it accessible from any machine on the network.

**From another computer:**
```bash
# Replace with your server IP
curl http://192.168.1.100:8080/api/clip/index/suggest/sentence \
  -H "Content-Type: application/json" \
  -d '{"sentence": "Elon Musk saluta il pubblico"}'
```

---

## ⭐ Clip Indexing - Quick Reference

### Workflow
1. **Scan Drive** (once after adding clips):
   ```bash
   curl -X POST http://localhost:8080/api/clip/index/scan?force=true
   ```

2. **Check Stats**:
   ```bash
   curl http://localhost:8080/api/clip/index/stats
   ```

3. **Get Suggestions for Sentence**:
   ```bash
   curl -X POST http://localhost:8080/api/clip/index/suggest/sentence \
     -H "Content-Type: application/json" \
     -d '{"sentence": "Elon Musk saluta il pubblico", "max_results": 10}'
   ```

4. **Process Entire Script**:
   ```bash
   curl -X POST http://localhost:8080/api/clip/index/suggest/script \
     -H "Content-Type: application/json" \
     -d '{
       "script": "Elon Musk sale sul palco e saluta. Parla di Tesla.",
       "max_results_per_sentence": 5
     }'
   ```

### How Semantic Matching Works

The system matches script sentences to clips using:

| Match Type | Points | Description |
|------------|--------|-------------|
| Entity Match | 100 | Person/place name matches clip tag (e.g., "Elon Musk") |
| Entity in Name | 80 | Entity found in clip filename |
| Entity in Folder | 60 | Entity found in folder path |
| Keyword-Tag Match | 50 | Keyword matches clip tag |
| Keyword-Name Match | 40 | Keyword in clip name |
| Keyword-Folder Match | 30 | Keyword in folder path |
| Action Verb | 20-30 | Action verb detected (saluta, parla, etc.) |
| Phrase Match | 50 | Large phrase match in clip name |
| Group Match | 15 | Correct group context |

### Supported Languages

**Italian & English** action verbs:
- saluta/greet, parla/talk, cammina/walk, corre/run
- guida/drive, spiega/explain, mostra/show
- presenta/present, intervista/interview
- ride/laugh, sorride/smile, stringe/shake
- abbraccia/hug, balla/dance, canta/sing

### Predefined Groups

| Group | Description |
|-------|-------------|
| interviews | Interview clips and conversations |
| broll | B-Roll footage and generic scenes |
| highlights | Highlight and viral moments |
| general | General purpose stock clips |
| nature | Nature and landscape clips |
| urban | City and urban footage |
| tech | Tech and AI related clips |
| business | Business and startup clips |

For full documentation, see [`CLIP_INDEXING_API.md`](./CLIP_INDEXING_API.md)
