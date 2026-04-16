# Script FROM Clips - Generazione Script Basata su Clip Esistenti

## Overview

Questo nuovo endpoint genera **script video basati sulle clip che hai già su Drive e Artlist**, invece di cercare e scaricare nuove clip.

### **Differenza tra i due endpoint:**

| Endpoint | Cosa Fa | Use Case |
|----------|---------|----------|
| `/api/script/generate-with-clips` | Genera script → cerca clip → scarica → carica su Drive | Quando NON hai clip e le vuoi scaricare |
| `/api/script/generate-from-clips` | Genera script BASANDOSI sulle clip esistenti | Quando HAI già clip e vuoi usarle |

---

## API Endpoint

### `POST /api/script/generate-from-clips`

**Request:**
```json
{
  "topic": "Tesla e le auto elettriche",
  "language": "italian",
  "tone": "professional",
  "target_duration": 60,
  "clips_per_segment": 3,
  "use_artlist": true,
  "use_drive_clips": true
}
```

**Response:**
```json
{
  "ok": true,
  "topic": "Tesla e le auto elettriche",
  "script": "Tesla ha rivoluzionato il mercato delle auto elettriche...",
  "word_count": 142,
  "est_duration": 60,
  "model": "gemma3:4b",
  "segments": [
    {
      "segment_index": 0,
      "text": "Tesla ha rivoluzionato il mercato delle auto elettriche...",
      "start_time": "00:00:00",
      "end_time": "00:00:10",
      "entities": {
        "frasi_importanti": ["Tesla ha rivoluzionato il mercato"],
        "nomi_speciali": ["Tesla"],
        "parole_importanti": ["auto", "elettriche"]
      },
      "artlist_clips": [
        {
          "id": "artlist_12345",
          "name": "electric car charging",
          "drive_link": "https://artlist.io/...",
          "duration": 15.5,
          "resolution": "1920x1080",
          "tags": ["electric", "car", "charging"]
        },
        {
          "id": "artlist_12346",
          "name": "tesla model s",
          "drive_link": "https://artlist.io/...",
          "duration": 12.0,
          "resolution": "1920x1080",
          "tags": ["tesla", "model s"]
        },
        {
          "id": "artlist_12347",
          "name": "electric vehicle technology",
          "drive_link": "https://artlist.io/...",
          "duration": 18.0,
          "resolution": "3840x2160",
          "tags": ["electric", "vehicle", "technology"]
        }
      ],
      "drive_clips": [
        {
          "id": "drive_clip_001",
          "name": "Tesla Factory Tour",
          "drive_link": "https://drive.google.com/file/d/xxx",
          "folder_path": "Stock Clips/Tesla",
          "duration": 25.0,
          "resolution": "1920x1080"
        }
      ],
      "total_clips": 4
    }
  ],
  "total_artlist_clips": 9,
  "total_drive_clips": 3,
  "processing_time_seconds": 12.5
}
```

---

## Come Funziona

### **Flusso Completo:**

```
1. Topic Input: "Tesla e le auto elettriche"
   ↓
2. Scan Clip Esistenti:
   - Drive: Cosa c'è su Drive? (es. "Tesla Factory Tour", "Elon Musk Interview")
   - Artlist: Cosa c'è nel DB Artlist? (es. "electric car", "charging station")
   ↓
3. Build Source Text:
   "Crea uno script su: Tesla e le auto elettriche.
    Clip disponibili per: Tesla, Elon Musk, Technology.
    Sono disponibili 15 clip video."
   ↓
4. Generate Script (Ollama):
   → Script generato BASATO sulle clip disponibili
   ↓
5. Segment Script:
   → Segment 0: "Tesla ha rivoluzionato..." (0-10s)
   → Segment 1: "...con il Model S e Model 3" (10-30s)
   → Segment 2: "...il futuro è elettrico" (30-60s)
   ↓
6. Extract Entities per Segment:
   → Segment 0: [Tesla, auto elettriche]
   → Segment 1: [Model S, Model 3]
   → Segment 2: [futuro, elettrico]
   ↓
7. For EACH Segment → Find 3 Artlist Clips:
   → Segment 0:
     - Artlist: "electric car charging" ✓
     - Artlist: "tesla model s" ✓
     - Artlist: "electric vehicle technology" ✓
   → Segment 1:
     - Artlist: "model s interior" ✓
     - Artlist: "model 3 launch" ✓
     - Artlist: "autonomous driving" ✓
   → Segment 2:
     - Artlist: "sustainable energy" ✓
     - Artlist: "electric future" ✓
     - Artlist: "green technology" ✓
   ↓
8. Match Drive Clips:
   → Segment 0: "Tesla Factory Tour" (from Drive folder "Tesla")
   → Segment 1: "Elon Musk Interview" (from Drive folder "Tesla")
   ↓
9. Return Complete Response:
   → Script + Segments + 3 Artlist clips/segment + Drive clips
```

---

## Testing

### **1. Quick Test**

```bash
cd /home/pierone/Pyt/VeloxEditing/refactored
./test_script_from_clips.sh
```

### **2. Manual Test**

```bash
curl -X POST http://localhost:8080/api/script/generate-from-clips \
  -H "Content-Type: application/json" \
  -d '{
    "topic": "Tesla e le auto elettriche",
    "language": "italian",
    "tone": "professional",
    "target_duration": 60,
    "clips_per_segment": 3,
    "use_artlist": true,
    "use_drive_clips": true
  }' | jq '.segments[0]'
```

### **3. Check Artlist Clips per Segment**

```bash
curl -s -X POST http://localhost:8080/api/script/generate-from-clips \
  -H "Content-Type: application/json" \
  -d '{
    "topic": "Intelligenza Artificiale",
    "target_duration": 90,
    "clips_per_segment": 3
  }' | jq '{
    segments: [.segments[] | {
      segment: .segment_index,
      text: .text[:50],
      artlist_count: (.artlist_clips | length),
      artlist_names: [.artlist_clips[].name],
      drive_count: (.drive_clips | length),
      total: .total_clips
    }]
  }'
```

---

## Response Analysis

### **Cosa Controllare:**

✅ **Ogni segmento ha 3 clip Artlist?**
```bash
# Verifica che ogni segmento abbia 3 clip Artlist
curl -s -X POST http://localhost:8080/api/script/generate-from-clips \
  -H "Content-Type: application/json" \
  -d '{"topic": "Tesla", "clips_per_segment": 3}' \
  | jq '[.segments[] | (.artlist_clips | length)]'

# Expected: [3, 3, 3, ...]
```

✅ **Clip Drive corrispondenti?**
```bash
# Verifica clip da Drive
curl -s -X POST http://localhost:8080/api/script/generate-from-clips \
  -H "Content-Type: application/json" \
  -d '{"topic": "Tesla", "use_drive_clips": true}' \
  | jq '[.segments[] | {
    segment: .segment_index,
    drive_clips: [.drive_clips[] | {name, folder_path}]
  }]'
```

✅ **Entity → Clip Mapping corretto?**
```bash
# Controlla le entità estratte e le clip associate
curl -s -X POST http://localhost:8080/api/script/generate-from-clips \
  -H "Content-Type: application/json" \
  -d '{"topic": "Tesla"}' \
  | jq '.segments[0] | {
    entities: .entities.nomi_speciali,
    artlist_clips: [.artlist_clips[] | .name],
    drive_clips: [.drive_clips[] | .name]
  }'
```

---

## Parameters

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `topic` | string | **required** | Argomento principale dello script |
| `language` | string | `"italian"` | Lingua dello script |
| `tone` | string | `"professional"` | Tono: professional, casual, enthusiastic, calm, funny, educational |
| `target_duration` | int | `60` | Durata target in secondi (10-1800) |
| `clips_per_segment` | int | `3` | **Quante clip Artlist trovare per segmento** |
| `use_artlist` | bool | `true` | Cerca clip su Artlist |
| `use_drive_clips` | bool | `true` | Usa clip da Drive |
| `model` | string | `"gemma3:4b"` | Modello Ollama |

---

## Examples

### **Example 1: Short Video (30s)**

```json
{
  "topic": "Apple iPhone 15",
  "target_duration": 30,
  "clips_per_segment": 3,
  "tone": "enthusiastic"
}
```

**Expected:**
- 1-2 segments
- 3-6 Artlist clips total
- 0-2 Drive clips (if available)

---

### **Example 2: Medium Video (2min)**

```json
{
  "topic": "Google Pixel e AI",
  "target_duration": 120,
  "clips_per_segment": 3,
  "tone": "educational"
}
```

**Expected:**
- 4-6 segments
- 12-18 Artlist clips total
- 2-5 Drive clips

---

### **Example 3: Long Video (5min)**

```json
{
  "topic": "Tesla e il futuro dell'energia",
  "target_duration": 300,
  "clips_per_segment": 3,
  "tone": "professional"
}
```

**Expected:**
- 10-15 segments
- 30-45 Artlist clips total
- 5-15 Drive clips

---

### **Example 4: Artlist Only (no Drive)**

```json
{
  "topic": "Nature and wildlife",
  "target_duration": 60,
  "clips_per_segment": 3,
  "use_artlist": true,
  "use_drive_clips": false
}
```

---

### **Example 5: Drive Only (no Artlist)**

```json
{
  "topic": "Tesla",
  "target_duration": 60,
  "clips_per_segment": 3,
  "use_artlist": false,
  "use_drive_clips": true
}
```

---

## Troubleshooting

### **Problem: No Artlist clips found**

**Cause:** Artlist DB not connected or no matching clips.

**Check:**
```bash
# Check if Artlist DB exists
ls -lh src/node-scraper/artlist_videos.db

# Check categories
curl -s http://localhost:8080/api/clip/index/categories
```

**Fix:**
- Ensure Artlist DB path is correct in config
- Check if DB has clips: `sqlite3 artlist_videos.db "SELECT COUNT(*) FROM video_links;"`

---

### **Problem: No Drive clips found**

**Cause:** Clip indexer not synced or no clips in Drive.

**Check:**
```bash
# Check indexer status
curl -s http://localhost:8080/api/clip/index/stats
```

**Fix:**
- Trigger reindex: `curl -X POST http://localhost:8080/api/clip/index/scan`
- Check Drive folders have clips

---

### **Problem: Script doesn't mention available clips**

**Cause:** Ollama might not integrate clip context well.

**Fix:**
- Use more specific topic: "Tesla Model S" instead of "cars"
- Increase `target_duration` for more context
- Check logs: `tail -f go-master/data/logs/server.log`

---

## Architecture

```
┌──────────────────────────────────────────────────────────────┐
│                 API Request                                   │
│        POST /api/script/generate-from-clips                  │
└──────────────────────┬───────────────────────────────────────┘
                       │
                       ▼
┌──────────────────────────────────────────────────────────────┐
│        ScriptFromClipsService.GenerateScriptFromClips        │
│                                                              │
│  Step 1: Scan Existing Clips                                 │
│  ┌────────────────────────────────────────────────────────┐ │
│  │  Drive Clips (from Clip Indexer)                      │ │
│  │  - GetIndex() → all indexed clips                     │ │
│  │  - Extract topics from folder paths                   │ │
│  │  Example: ["Tesla", "Elon Musk", "Technology"]        │ │
│  │                                                       │ │
│  │  Artlist Clips (from SQLite DB)                       │ │
│  │  - GetAllCategories()                                 │ │
│  │  Example: ["automotive", "technology", "business"]    │ │
│  └────────────────────────────────────────────────────────┘ │
│                                                              │
│  Step 2: Build Source Text                                  │
│  ┌────────────────────────────────────────────────────────┐ │
│  │  "Crea uno script su: Tesla e le auto elettriche.     │ │
│  │   Clip disponibili per: Tesla, Elon Musk, Technology. │ │
│  │   Sono disponibili 15 clip video."                    │ │
│  └────────────────────────────────────────────────────────┘ │
│                                                              │
│  Step 3: Generate Script (Ollama)                           │
│  ┌────────────────────────────────────────────────────────┐ │
│  │  Script generated BASED on available clips context    │ │
│  └────────────────────────────────────────────────────────┘ │
│                                                              │
│  Step 4: Segment & Extract Entities                         │
│  ┌────────────────────────────────────────────────────────┐ │
│  │  Segment 0: "Tesla ha rivoluzionato..." (0-10s)       │ │
│  │  Entities: [Tesla, auto elettriche]                   │ │
│  │                                                       │ │
│  │  Segment 1: "...con il Model S" (10-30s)              │ │
│  │  Entities: [Model S, Model 3]                         │ │
│  └────────────────────────────────────────────────────────┘ │
│                                                              │
│  Step 5: For EACH Segment → Find 3 Artlist Clips           │
│  ┌────────────────────────────────────────────────────────┐ │
│  │  Segment 0:                                           │ │
│  │    - Search "Tesla" → 3 Artlist clips                 │ │
│  │    - Search "auto elettriche" → 3 Artlist clips       │ │
│  │                                                       │ │
│  │  Segment 1:                                           │ │
│  │    - Search "Model S" → 3 Artlist clips               │ │
│  │    - Search "Model 3" → 3 Artlist clips               │ │
│  └────────────────────────────────────────────────────────┘ │
│                                                              │
│  Step 6: Match Drive Clips                                  │
│  ┌────────────────────────────────────────────────────────┐ │
│  │  Segment 0:                                           │ │
│  │    - Match "Tesla" → Drive clip from "Tesla" folder   │ │
│  │                                                       │ │
│  │  Segment 1:                                           │ │
│  │    - Match "Model S" → Drive clip from "Tesla" folder │ │
│  └────────────────────────────────────────────────────────┘ │
│                                                              │
│  Step 7: Return Complete Response                          │
│  ┌────────────────────────────────────────────────────────┐ │
│  │  {                                                    │ │
│  │    "script": "...",                                   │ │
│  │    "segments": [                                      │ │
│  │      {                                                │ │
│  │        "text": "...",                                 │ │
│  │        "start_time": "00:00:00",                      │ │
│  │        "end_time": "00:00:10",                        │ │
│  │        "artlist_clips": [/* 3 clips */],              │ │
│  │        "drive_clips": [/* 1-2 clips */],              │ │
│  │        "total_clips": 4                               │ │
│  │      }                                                │ │
│  │    ],                                                 │ │
│  │    "total_artlist_clips": 9,                          │ │
│  │    "total_drive_clips": 3                             │ │
│  │  }                                                    │ │
│  └────────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────────┘
```

---

## Files Created/Modified

| File | Status | Purpose |
|------|--------|---------|
| `internal/service/scriptclips/script_from_clips.go` | ✅ **NEW** | Core service |
| `internal/api/handlers/script_from_clips.go` | ✅ **NEW** | HTTP handler |
| `cmd/server/main.go` | ✏️ Modified | Service initialization |
| `internal/api/routes.go` | ✏️ Modified | Route registration |
| `test_script_from_clips.sh` | ✅ **NEW** | Test script |

---

## Summary

✅ **Legge le clip da Drive** (quelle che abbiamo già indicizzate)  
✅ **Legge le clip da Artlist** (SQLite DB)  
✅ **Genera script BASATO sulle clip disponibili**  
✅ **Per ogni segmento → trova 3 clip Artlist**  
✅ **Matcha anche le clip Drive per entità**  
✅ **Ritorna mapping completo**: script → segmenti → 3 Artlist clips + Drive clips  

**Status: PRONTO PER IL TEST** 🚀

---

## Next Test Steps

1. **Avvia il server:**
   ```bash
   cd go-master
   ./start.sh
   ```

2. **Verifica clip indicizzate:**
   ```bash
   curl http://localhost:8080/api/clip/index/stats
   ```

3. **Esegui il test:**
   ```bash
   ./test_script_from_clips.sh
   ```

4. **Controlla che ogni segmento abbia 3 clip Artlist:**
   ```bash
   curl -s -X POST http://localhost:8080/api/script/generate-from-clips \
     -H "Content-Type: application/json" \
     -d '{"topic": "Tesla", "clips_per_segment": 3}' \
     | jq '[.segments[] | (.artlist_clips | length)]'
   ```

   **Expected output:** `[3, 3, 3, ...]`
