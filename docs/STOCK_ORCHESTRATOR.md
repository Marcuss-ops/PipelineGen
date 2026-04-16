# Stock Orchestrator - Full Pipeline Implementation

## Overview

Implementazione completa del pipeline **YouTube → Download → Entity Extraction → Drive Upload** con organizzazione in sottocartelle.

---

## 🎯 Cosa Fa

Un **unico endpoint API** che esegue tutto il pipeline automaticamente:

```
YouTube Search → Download Video → Estrai Entità → Crea Cartelle Drive → Upload
```

---

## 📍 Endpoint

### `POST /api/stock/orchestrate`

**Richiesta:**
```json
{
  "query": "Tesla electric cars",
  "max_videos": 5,
  "quality": "best",
  "extract_entities": true,
  "entity_count": 12,
  "upload_to_drive": true,
  "create_folders": true,
  "folder_structure": "Stock Videos/{topic}/{date}"
}
```

**Risposta:**
```json
{
  "ok": true,
  "query": "Tesla electric cars",
  "youtube_results": [
    {
      "id": "TBrIxvzWB0o",
      "title": "Tesla Flying Car Is Finally Here",
      "url": "https://www.youtube.com/watch?v=TBrIxvzWB0o",
      "duration": 638,
      "thumbnail": "https://i.ytimg.com/vi/TBrIxvzWB0o/hq720.jpg"
    }
  ],
  "downloaded_videos": [
    {
      "video_id": "TBrIxvzWB0o",
      "title": "Tesla Flying Car Is Finally Here",
      "local_path": "/tmp/velox/downloads/stock_1234567890_TBrIxvzWB0o.mp4",
      "file_size": 52428800,
      "duration": 638,
      "resolution": "1920x1080",
      "youtube_url": "https://www.youtube.com/watch?v=TBrIxvzWB0o"
    }
  ],
  "entity_analysis": {
    "total_entities": 15,
    "frasi_importanti": ["Tesla flying car revolution"],
    "nomi_speciali": ["Tesla", "Elon Musk"],
    "parole_importanti": ["electric", "flying", "car"]
  },
  "uploaded_to_drive": [
    {
      "filename": "Tesla_Flying_Car_Is_Finally_Here_TBrIxvzWB0o.mp4",
      "file_id": "1abc123xyz",
      "drive_url": "https://drive.google.com/file/d/1abc123xyz",
      "folder_path": "Stock Videos/Tesla electric cars/2026-04-12",
      "original_title": "Tesla Flying Car Is Finally Here"
    }
  ],
  "processing_time_seconds": 45.3
}
```

---

## 🔧 Parametri

| Parametro | Tipo | Default | Descrizione |
|-----------|------|---------|-------------|
| `query` | string | **required** | Query di ricerca YouTube |
| `max_videos` | int | 5 | Numero max video da scaricare (1-20) |
| `quality` | string | `"best"` | Qualità: `best`, `720p`, `4k` |
| `extract_entities` | bool | `true` | Estrai entità con Ollama |
| `entity_count` | int | 12 | Numero entità per segmento |
| `upload_to_drive` | bool | `true` | Upload su Google Drive |
| `create_folders` | bool | `true` | Crea sottocartelle per topic |
| `folder_structure` | string | `"Stock Videos/{topic}"` | Struttura cartelle. Placeholders: `{topic}`, `{date}` |

---

## 📋 Test Commands

### **Test 1: Pipeline Completa**

```bash
curl -X POST http://localhost:8080/api/stock/orchestrate \
  -H "Content-Type: application/json" \
  -d '{
    "query": "Tesla electric cars",
    "max_videos": 3,
    "quality": "best",
    "extract_entities": true,
    "entity_count": 12,
    "upload_to_drive": true,
    "create_folders": true,
    "folder_structure": "Stock Videos/{topic}/{date}"
  }' | jq '.'
```

### **Test 2: Solo Download (no Drive)**

```bash
curl -X POST http://localhost:8080/api/stock/orchestrate \
  -H "Content-Type: application/json" \
  -d '{
    "query": "Artificial Intelligence technology",
    "max_videos": 2,
    "quality": "720p",
    "extract_entities": true,
    "upload_to_drive": false
  }' | jq '.downloaded_videos'
```

### **Test 3: Batch (Multiple Queries)**

```bash
curl -X POST http://localhost:8080/api/stock/orchestrate/batch \
  -H "Content-Type: application/json" \
  -d '{
    "queries": [
      {"query": "space exploration NASA", "max_videos": 2, "upload_to_drive": false},
      {"query": "renewable energy solar", "max_videos": 2, "upload_to_drive": false}
    ]
  }' | jq '.results[] | {query, status, youtube: (.result.youtube_results | length)}'
```

### **Test 4: Entity Extraction Only**

```bash
curl -X POST http://localhost:8080/api/stock/orchestrate \
  -H "Content-Type: application/json" \
  -d '{
    "query": "Elon Musk SpaceX Mars",
    "max_videos": 2,
    "extract_entities": true,
    "upload_to_drive": false
  }' | jq '.entity_analysis'
```

---

## 🏗️ Struttura Drive

Con `folder_structure: "Stock Videos/{topic}/{date}"`:

```
Google Drive/
└── Stock Videos/
    └── Tesla electric cars/
        └── 2026-04-12/
            ├── Tesla_Flying_Car_TBrIxvzWB0o.mp4
            ├── Electric_Vehicle_Revolution_abc123.mp4
            └── Future_of_Transportation_xyz789.mp4
```

---

## 📊 Entity Extraction

Ollama estrae automaticamente 4 categorie di entità dai titoli/descrizioni video:

| Categoria | Esempio | Uso |
|-----------|---------|-----|
| `frasi_importanti` | "Tesla flying car revolution" | Frasi complete significative |
| `nomi_speciali` | "Tesla", "Elon Musk", "SpaceX" | Nomi propri, brand, prodotti |
| `parole_importanti` | "electric", "flying", "future" | Keywords principali |
| `entity_senza_testo` | {"Tesla Logo": "URL"} | Entità con immagini associate |

---

## 🗂️ Pipeline Steps

### **Step 1: YouTube Search**
```bash
yt-dlp "ytsearch5:Tesla electric cars" --dump-json --flat-playlist --no-warnings
```
- Cerca su YouTube
- Estrae: ID, titolo, durata, thumbnail, uploader
- Ritorna fino a `max_videos` risultati

### **Step 2: Download Videos**
```bash
yt-dlp -f "bestvideo[height<=1080]+bestaudio/best[height<=1080]" -o "/tmp/velox/downloads/stock_%id.mp4" <URL>
```
- Scarica video con yt-dlp
- Formato: best up to 1080p (configurabile)
- Salva in `/tmp/velox/downloads/`

### **Step 3: Entity Extraction**
```go
ollamaClient.ExtractEntitiesFromSegment(ctx, EntityExtractionRequest{
    SegmentText: combined_titles,
    EntityCount: 12,
})
```
- Combina tutti i titoli video
- Estrae entità con Ollama (gemma3:4b)
- Ritorna: frasi, nomi, keywords

### **Step 4: Drive Upload**
```go
// Create folder structure
folderID = driveClient.GetOrCreateFolder(ctx, "Stock Videos", "root")
folderID = driveClient.GetOrCreateFolder(ctx, "Tesla electric cars", folderID)
folderID = driveClient.GetOrCreateFolder(ctx, "2026-04-12", folderID)

// Upload each video
driveClient.UploadFile(ctx, localPath, folderID, filename)
```
- Crea cartelle annidate
- Upload video con metadata
- Ritorna Drive URL per ogni file

---

## 🚨 Error Handling

| Errore | Causa | Fix |
|--------|-------|-----|
| `"query is required"` | Query mancante | Aggiungi `"query": "..."` |
| `"no YouTube results found"` | yt-dlp non trova video | Prova query diversa o controlla yt-dlp |
| `"yt-dlp failed"` | yt-dlp non installato | `pip install yt-dlp` o aggiorna |
| `"failed to create folder"` | Drive credentials invalid | Rigenera token.json |
| `"upload failed"` | Spazio Drive esaurito | Libera spazio o usa altro account |

---

## 📁 Files Created

| File | Tipo | Descrizione |
|------|------|-------------|
| `internal/service/stockorchestrator/service.go` | ✅ NEW | Core orchestrator service |
| `internal/api/handlers/stock_orchestrator.go` | ✅ NEW | HTTP handler |
| `internal/stock/search.go` | ✏️ FIXED | YouTube search JSON parsing |
| `cmd/server/main.go` | ✏️ MOD | Service initialization |
| `internal/api/routes.go` | ✏️ MOD | Route registration |
| `test_stock_orchestrator.sh` | ✅ NEW | Test script |

---

## 🔍 Troubleshooting

### **YouTube Search returns 0 results**

```bash
# Test yt-dlp manually
yt-dlp "ytsearch3:Tesla" --dump-json --flat-playlist

# Check yt-dlp version
yt-dlp --version

# Update yt-dlp
pip install --upgrade yt-dlp
```

### **Drive Upload fails**

```bash
# Check credentials
ls -lh go-master/credentials.json go-master/token.json

# Re-authenticate
rm go-master/token.json
# Restart server and follow OAuth flow
```

### **Entity Extraction fails**

```bash
# Check Ollama
curl http://localhost:11434/api/tags

# Test model
curl http://localhost:11434/api/generate -d '{"model":"gemma3:4b","prompt":"test"}'
```

---

## 📈 Performance

| Operation | Time | Notes |
|-----------|------|-------|
| YouTube Search | 1-3s | Depends on network |
| Download (per video) | 10-60s | Depends on size/speed |
| Entity Extraction | 2-5s | Ollama inference |
| Drive Upload (per video) | 5-15s | Depends on size |
| **Total (3 videos)** | **45-120s** | End-to-end pipeline |

---

## ✅ Implementation Status

| Feature | Status | Notes |
|---------|--------|-------|
| YouTube Search | ✅ Working | Fixed JSON parsing for new yt-dlp format |
| Video Download | ✅ Ready | yt-dlp integration |
| Entity Extraction | ✅ Ready | Ollama gemma3:4b |
| Drive Upload | ✅ Ready | With folder structure |
| Folder Creation | ✅ Ready | Nested folders support |
| Batch Processing | ✅ Ready | Multiple queries |
| Quality Selection | ✅ Ready | best, 720p, 4k |
| Error Handling | ✅ Ready | Graceful degradation |

---

## 🎯 Next Steps (Optional)

1. **Video Processing** - Add transitions/effects after download
2. **Auto-Tagging** - Use extracted entities as Drive file tags
3. **Thumbnail Generation** - Extract frames from videos
4. **Progress Tracking** - Real-time pipeline progress via WebSocket
5. **Caching** - Cache YouTube search results to avoid re-downloading
6. **Format Conversion** - Auto-convert to specific codec/resolution

---

## 📝 Summary

✅ **YouTube API Integration** - Search with yt-dlp, correct URL extraction  
✅ **Video Download** - Automatic download with quality selection  
✅ **Entity Extraction** - Ollama AI extracts key entities from video metadata  
✅ **Drive Upload** - Organized folder structure with metadata  
✅ **Batch Support** - Process multiple queries in one request  
✅ **Error Handling** - Graceful degradation on partial failures  
✅ **Logging** - Comprehensive logging for debugging  

**Status: READY FOR TESTING** 🚀
