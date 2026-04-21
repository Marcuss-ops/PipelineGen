# Clip Indexing API Documentation

## Overview

The Clip Indexing System provides intelligent clip suggestion capabilities by:

1. **Indexing** all clips from Google Drive with rich metadata (tags, groups, resolution, etc.)
2. **Semantic Matching** - Matching script sentences to clips using NLP + entity extraction
3. **Remote Access** - All endpoints accessible from any machine via HTTP API

**Base URL**: `http://<server-ip>:8080/api`

---

## Quick Start Example

### 1. Scan Your Drive for Clips

```bash
curl -X POST http://192.168.1.100:8080/api/clip/index/scan \
  -H "Authorization: Bearer YOUR_TOKEN"
```

This scans your Google Drive and builds a searchable index of all video clips.

### 2. Get Clip Suggestions for a Sentence

```bash
curl -X POST http://192.168.1.100:8080/api/clip/index/suggest/sentence \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -d '{
    "sentence": "Elon Musk saluta il pubblico",
    "max_results": 10,
    "min_score": 20
  }'
```

**Response**:
```json
{
  "ok": true,
  "sentence": "Elon Musk saluta il pubblico",
  "suggestions": [
    {
      "clip": {
        "id": "1a2b3c4d5e",
        "name": "Elon Musk Interview Highlights",
        "filename": "elon_musk_interview_001.mp4",
        "folder_path": "interviews/elon_musk",
        "group": "interviews",
        "drive_link": "https://drive.google.com/file/d/1a2b3c4d5e/view",
        "download_link": "https://drive.google.com/uc?export=download&id=1a2b3c4d5e",
        "resolution": "1920x1080",
        "tags": ["elon", "musk", "interview", "tesla", "spacex", "saluta"],
        "size": 15234567,
        "mime_type": "video/mp4"
      },
      "score": 85.5,
      "match_type": "entity_match",
      "match_terms": ["entity:Elon Musk", "saluta"],
      "match_reason": "Entity 'Elon Musk' (PERSON) matches tag 'elon'; Keyword 'saluta' matches tag 'saluta'"
    }
  ],
  "total": 1,
  "best_score": 85.5
}
```

---

## API Endpoints

### Index Management

#### 1. Trigger Full Scan

Scans Google Drive and rebuilds the clip index with metadata extraction.

**Endpoint**: `POST /clip/index/scan`

**Query Parameters**:
- `force` (bool, optional): Force scan even if recently synced. Default: `false`

**Example**:
```bash
curl -X POST "http://localhost:8080/api/clip/index/scan?force=true" \
  -H "Authorization: Bearer YOUR_TOKEN"
```

**Response**:
```json
{
  "ok": true,
  "message": "Clip index scan completed successfully",
  "stats": {
    "total_clips": 150,
    "total_folders": 25,
    "clips_by_group": {
      "interviews": 50,
      "broll": 40,
      "tech": 30,
      "business": 30
    },
    "last_scan_duration": 45000000000
  },
  "last_sync": "2026-04-10T15:30:00Z"
}
```

---

#### 2. Get Index Statistics

**Endpoint**: `GET /clip/index/stats`

**Example**:
```bash
curl http://localhost:8080/api/clip/index/stats \
  -H "Authorization: Bearer YOUR_TOKEN"
```

**Response**:
```json
{
  "ok": true,
  "stats": {
    "total_clips": 150,
    "total_folders": 25,
    "clips_by_group": {
      "interviews": 50,
      "broll": 40
    },
    "last_scan_duration": 45000000000
  },
  "last_sync": "2026-04-10T15:30:00Z",
  "index_age_minutes": 15.5
}
```

---

#### 3. Get Indexer Status

**Endpoint**: `GET /clip/index/status`

**Example**:
```bash
curl http://localhost:8080/api/clip/index/status \
  -H "Authorization: Bearer YOUR_TOKEN"
```

**Response**:
```json
{
  "ok": true,
  "initialized": true,
  "last_sync": "2026-04-10T15:30:00Z",
  "needs_sync": false,
  "total_clips": 150,
  "total_folders": 25,
  "clips_by_group": {
    "interviews": 50,
    "broll": 40,
    "tech": 30,
    "business": 30
  }
}
```

---

#### 4. Clear Index

**Endpoint**: `DELETE /clip/index/clear`

**Example**:
```bash
curl -X DELETE http://localhost:8080/api/clip/index/clear \
  -H "Authorization: Bearer YOUR_TOKEN"
```

---

### Search & Browse

#### 5. Search Clips with Filters

**Endpoint**: `POST /clip/index/search`

**Request Body**:
```json
{
  "query": "elon musk",
  "group": "interviews",
  "folder_id": "",
  "min_duration": 0,
  "max_duration": 300,
  "resolution": "1920x1080",
  "tags": ["elon", "tesla"],
  "max_results": 50,
  "offset": 0,
  "min_score": 0
}
```

**Example**:
```bash
curl -X POST http://localhost:8080/api/clip/index/search \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -d '{
    "query": "elon musk",
    "group": "interviews",
    "max_results": 20
  }'
```

---

#### 6. List All Clips

**Endpoint**: `GET /clip/index/clips`

**Query Parameters**:
- `group` (string, optional): Filter by group
- `limit` (int, optional): Max results. Default: `100`
- `offset` (int, optional): Pagination offset. Default: `0`

**Example**:
```bash
curl "http://localhost:8080/api/clip/index/clips?group=interviews&limit=50" \
  -H "Authorization: Bearer YOUR_TOKEN"
```

---

#### 7. Get Single Clip

**Endpoint**: `GET /clip/index/clips/:id`

**Example**:
```bash
curl http://localhost:8080/api/clip/index/clips/1a2b3c4d5e \
  -H "Authorization: Bearer YOUR_TOKEN"
```

---

### Semantic Suggestions (MAIN FEATURE)

#### 8. Suggest Clips for Sentence ⭐

This is the **main endpoint** for your use case. It matches a sentence from your script to relevant clips using:

- **Entity Extraction** - Detects people, places, organizations
- **Keyword Matching** - Extracts important keywords
- **Action Verb Detection** - Identifies actions (saluta, parla, cammina, etc.)
- **Context Matching** - Considers folder path and group context

**Endpoint**: `POST /clip/index/suggest/sentence`

**Request Body**:
```json
{
  "sentence": "Elon Musk saluta il pubblico",
  "max_results": 10,
  "min_score": 20
}
```

**Example**:
```bash
curl -X POST http://localhost:8080/api/clip/index/suggest/sentence \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -d '{
    "sentence": "Elon Musk saluta il pubblico",
    "max_results": 10,
    "min_score": 20
  }'
```

**How Scoring Works**:
- **Entity Match** (100 pts): "Elon Musk" matches clip tag
- **Entity in Name** (80 pts): "Elon Musk" in clip filename
- **Entity in Folder** (60 pts): "Elon Musk" in folder path
- **Keyword-Tag Match** (50 pts): Keyword matches clip tag
- **Keyword-Name Match** (40 pts): Keyword in clip name
- **Keyword-Folder Match** (30 pts): Keyword in folder path
- **Action Verb** (20-30 pts): "saluta" matches action context
- **Phrase Match** (50 pts): Large phrase match in name
- **Group Match** (15 pts): Correct group context

---

#### 9. Suggest Clips for Entire Script ⭐⭐

Processes an entire script and returns suggestions for each sentence.

**Endpoint**: `POST /clip/index/suggest/script`

**Request Body**:
```json
{
  "script": "Elon Musk saluta il pubblico. Poi parla di Tesla e del futuro dell'energia sostenibile. Mostra il nuovo modello di auto elettrica.",
  "max_results_per_sentence": 5,
  "min_score": 20
}
```

**Example**:
```bash
curl -X POST http://localhost:8080/api/clip/index/suggest/script \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -d '{
    "script": "Elon Musk saluta il pubblico. Poi parla di Tesla e del futuro dell'\''energia sostenibile.",
    "max_results_per_sentence": 5,
    "min_score": 20
  }'
```

**Response**:
```json
{
  "ok": true,
  "script_length": 95,
  "sentences_with_clips": 2,
  "total_clip_suggestions": 8,
  "suggestions": [
    {
      "sentence": "Elon Musk saluta il pubblico.",
      "suggestions": [
        {
          "clip": { /* clip data */ },
          "score": 85.5,
          "match_type": "entity_match",
          "match_terms": ["Elon Musk", "saluta"],
          "match_reason": "Entity 'Elon Musk' matches tag 'elon'"
        }
      ],
      "best_score": 85.5
    },
    {
      "sentence": "Poi parla di Tesla e del futuro dell'energia sostenibile.",
      "suggestions": [
        {
          "clip": { /* clip data */ },
          "score": 72.3,
          "match_type": "keyword_tag_match",
          "match_terms": ["tesla", "energia"],
          "match_reason": "Keyword 'tesla' matches tag 'tesla'"
        }
      ],
      "best_score": 72.3
    }
  ]
}
```

---

## Usage from Remote Machines

### Configuration

The server is already configured to listen on `0.0.0.0:8080`, making it accessible from any machine on the network.

**To access from another computer**:

1. Find server IP:
   ```bash
   ip addr show
   # Look for: inet 192.168.x.x
   ```

2. Use the IP in your API calls:
   ```bash
   curl http://192.168.1.100:8080/api/clip/index/stats
   ```

### Python Example (Remote Client)

```python
import requests

SERVER_URL = "http://192.168.1.100:8080/api"
API_TOKEN = "your-token-here"

headers = {
    "Authorization": f"Bearer {API_TOKEN}",
    "Content-Type": "application/json"
}

# 1. Trigger scan
response = requests.post(f"{SERVER_URL}/clip/index/scan?force=true", headers=headers)
print("Scan complete:", response.json())

# 2. Get suggestions for sentence
sentence = "Elon Musk saluta il pubblico"
response = requests.post(
    f"{SERVER_URL}/clip/index/suggest/sentence",
    headers=headers,
    json={
        "sentence": sentence,
        "max_results": 10,
        "min_score": 20
    }
)

suggestions = response.json()
for sug in suggestions["suggestions"]:
    print(f"Score: {sug['score']:.1f} - {sug['clip']['name']}")
    print(f"  Drive Link: {sug['clip']['drive_link']}")
    print(f"  Match: {sug['match_reason']}")
    print()

# 3. Process entire script
script = """
Elon Musk sale sul palco e saluta il pubblico.
Poi parla del futuro di Tesla e SpaceX.
Mostra il nuovo modello di auto elettrica.
Ride e scherza con l'intervistatore.
"""

response = requests.post(
    f"{SERVER_URL}/clip/index/suggest/script",
    headers=headers,
    json={
        "script": script,
        "max_results_per_sentence": 3,
        "min_score": 20
    }
)

results = response.json()
for sentence_sug in results["suggestions"]:
    print(f"\nSentence: {sentence_sug['sentence']}")
    print(f"Best clip score: {sentence_sug['best_score']:.1f}")
    if sentence_sug["suggestions"]:
        best = sentence_sug["suggestions"][0]
        print(f"  -> Clip: {best['clip']['name']}")
        print(f"  -> Link: {best['clip']['drive_link']}")
```

---

## Supported Languages

The system supports **both Italian and English**:

### Italian Action Verbs
- saluta, parla, cammina, corre, guida, spiega, mostra, presenta
- dimostra, intervista, risponde, chiede, ride, sorride, stringe
- abbraccia, balla, canta

### English Action Verbs
- greet, talk, walk, run, drive, explain, show, present
- demonstrate, interview, answer, ask, laugh, smile, shake
- hug, dance, sing

### Entity Detection
- **Person Names**: Capitalized words (e.g., "Elon Musk", "Mark Zuckerberg")
- **Places**: Capitalized location names
- **Organizations**: Company names

---

## Predefined Groups

| Group ID | Description | Example Content |
|----------|-------------|----------------|
| `interviews` | Interview clips and conversations | Elon Musk Joe Rogan |
| `broll` | B-Roll footage and generic scenes | City skyline, nature |
| `highlights` | Highlight and viral moments | Keynote moments |
| `general` | General purpose stock clips | Various |
| `nature` | Nature and landscape clips | Mountains, ocean |
| `urban` | City and urban footage | Streets, buildings |
| `tech` | Tech and AI related clips | Robots, computers |
| `business` | Business and startup clips | Office, meetings |

---

## Architecture

```
┌──────────────────────────────────────────────┐
│           Your Script / Application          │
│         (Remote Machine - Any Language)      │
└────────────────┬─────────────────────────────┘
                 │ HTTP POST
                 │ /clip/index/suggest/sentence
                 ▼
┌──────────────────────────────────────────────┐
│        VeloxEditing API Server                │
│        (0.0.0.0:8080)                        │
│                                              │
│  ┌───────────────────────────────────┐      │
│  │   SemanticSuggester               │      │
│  │   - Entity Extraction             │      │
│  │   - Keyword Matching              │      │
│  │   - Action Verb Detection         │      │
│  └───────────┬───────────────────────┘      │
│              │                               │
│  ┌───────────▼───────────────────────┐      │
│  │   ClipIndex (In-Memory Cache)     │      │
│  │   - 150 clips indexed             │      │
│  │   - Tags, groups, metadata        │      │
│  └───────────┬───────────────────────┘      │
│              │                               │
│  ┌───────────▼───────────────────────┐      │
│  │   ClipIndexStore (JSON File)      │      │
│  │   - data/clip_index.json          │      │
│  └───────────────────────────────────┘      │
└──────────────────────────────────────────────┘
                 │
                 │ Google Drive API
                 ▼
┌──────────────────────────────────────────────┐
│           Google Drive                        │
│   /interviews/elon_musk/                     │
│     ├── elon_musk_001.mp4                    │
│     ├── elon_musk_002.mp4                    │
│   /broll/tech/                               │
│     ├── tesla_factory.mp4                    │
└──────────────────────────────────────────────┘
```

---

## Performance Tips

1. **Scan Once, Query Many Times**: Run `/scan` once after adding clips to Drive, then query the local index
2. **Use min_score Filter**: Set `min_score: 20-30` to filter out irrelevant results
3. **Limit Results**: Use `max_results` to avoid large payloads
4. **Cache Locally**: Store suggestions on your client if processing the same script multiple times

---

## Troubleshooting

### "Clip indexer not initialized"
- Check that `credentials.json` and `token.json` exist in the server directory
- Verify Google Drive API scopes include `drive.readonly`

### "No clips in index"
- Run `POST /clip/index/scan?force=true` first
- Check stats with `GET /clip/index/stats`

### Slow suggestions
- First scan takes time (depends on clip count)
- Subsequent suggestions are fast (uses local index)

### Access denied from remote machine
- Ensure server is running with `VELOX_HOST=0.0.0.0`
- Check firewall allows port 8080
- Verify Authorization token is correct

---

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `VELOX_HOST` | `0.0.0.0` | Server bind address (accessible from network) |
| `VELOX_PORT` | `8080` | Server port |
| `VELOX_CREDENTIALS_FILE` | `credentials.json` | Google OAuth credentials path |
| `VELOX_TOKEN_FILE` | `token.json` | Google OAuth token path |
| `VELOX_CLIP_ROOT_FOLDER` | `root` | Google Drive root folder ID for clips |

---

## Next Steps

1. **Add Clips to Drive**: Organize your video clips in Google Drive folders
2. **Run Initial Scan**: `POST /clip/index/scan?force=true`
3. **Test Sentence Matching**: Try `/suggest/sentence` with various sentences
4. **Integrate into Workflow**: Use the API in your video creation pipeline

---

## Example: Complete Workflow

```bash
# Step 1: Scan Drive (do this once after adding new clips)
curl -X POST http://192.168.1.100:8080/api/clip/index/scan?force=true \
  -H "Authorization: Bearer YOUR_TOKEN"

# Step 2: Check index stats
curl http://192.168.1.100:8080/api/clip/index/stats \
  -H "Authorization: Bearer YOUR_TOKEN"

# Step 3: Get suggestions for your script
curl -X POST http://192.168.1.100:8080/api/clip/index/suggest/script \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -d '{
    "script": "Elon Musk sale sul palco e saluta. Parla di Tesla e del futuro.",
    "max_results_per_sentence": 5,
    "min_score": 20
  }'

# Step 4: Use the suggested clips in your video editing pipeline
# - Download via download_link
# - Or access directly via drive_link
```

---

**Happy clip matching! 🎬🚀**
