# Script Pipeline API - Documentazione Completa

## Panoramica del Servizio

**Server unico**: Il servizio Go è gestito da un unico server (`go-master/cmd/server/main.go`) che espone tutti gli endpoint HTTP sulla porta configurata (default: 8000).

**Modulo Script Pipeline**: Il file `src/go-master/internal/api/handlers/script_pipeline.go` (1017 righe) contiene tutta la logica dei 9 endpoint REST per la gestione degli script.

---

## Endpoint Script Pipeline

Tutti gli endpoint sono prefissati con `/api/script-pipeline`

| # | Metodo | Endpoint | Descrizione |
|---|--------|----------|-------------|
| 1 | POST | `/generate-text` | Genera script text con Ollama |
| 2 | POST | `/divide` | Divide lo script in segmenti |
| 3 | POST | `/extract-entities` | Estrae entità, keywords, immagini |
| 4 | POST | `/associate-stock` | Associa clip dallo StockDB |
| 5 | POST | `/associate-artlist` | Associa clip da Artlist |
| 6 | POST | `/find-keyphrases` | Trova key phrases nel testo |
| 7 | POST | `/download-clips` | Prepara download clip |
| 8 | POST | `/translate` | Traduce testo in multilingua |
| 9 | POST | `/create-doc` | Crea documento finale Google Docs |

---

## 1. POST /api/script-pipeline/generate-text

Genera uno script usando Ollama LLM.

### Request
```json
{
  "topic": "Gervonta Davis",
  "duration": 80,
  "language": "italian",
  "tone": "professional",
  "template": "biography",
  "model": "gemma3:4b"
}
```

**Campi:**
- `topic` (required): Argomento dello script
- `duration` (default: 60): Durata stimata in secondi
- `language` (default: "italian"): Lingua di output
- `tone` (default: "professional"): Tono dello script
- `template` (default: "biography"): Template da usare
- `model` (default: "gemma3:4b"): Modello Ollama

### Response
```json
{
  "ok": true,
  "script": "Gervonta Davis è nato nel...",
  "word_count": 245,
  "est_duration": 82,
  "model": "gemma3:4b",
  "language": "italian"
}
```

---

## 2. POST /api/script-pipeline/divide

Divide lo script in segmenti temporali.

### Request
```json
{
  "script": "Gervonta Davis è nato nel 1997... [testo completo]",
  "max_segments": 3
}
```

### Response
```json
{
  "ok": true,
  "segments": [
    {
      "index": 0,
      "text": "Gervonta Davis è nato nel 1997...",
      "start_time": 0,
      "end_time": 20
    }
  ],
  "count": 3
}
```

---

## 3. POST /api/script-pipeline/extract-entities

Endpoint fondamentale - estrae TUTTE le entità dal testo.

**Corrisponde a**: `full_entity_script.py` - `extract_all_entities_with_ollama()`

### Request
```json
{
  "segments": [
    {"text": "Gervonta Davis, born on November 7, 1997, in Baltimore, Maryland"}
  ],
  "max_entities": 3
}
```

### Response
```json
{
  "ok": true,
  "segment_data": [
    {
      "segment_index": 0,
      "text": "Gervonta Davis...",
      "entities": [
        {"type": "person", "value": "Gervonta Davis", "source": "proper_noun"}
      ]
    }
  ],
  "all_entities": ["Gervonta Davis", "Baltimore", "Maryland"],
  "keywords": ["Gervonta Davis", "Baltimore", "Maryland"],
  "frasi_importanti": ["Gervonta Davis, born on November 7..."],
  "nomi_speciali": ["Gervonta Davis", "Baltimore"],
  "parole_importanti": ["nato", "campione"],
  "entita_con_immagine": [
    {"entity": "Gervonta Davis", "image_url": "https://duckduckgo.com/i/5f7feee61ffe03f8.jpg"}
  ]
}
```

**Campi chiave:**
- `frasi_importanti`: Frasi significative per i clip importanti
- `nomi_speciali`: Nomi propri per matching Drive
- `parole_importanti`: Keywords per matching Artlist
- `entita_con_immagine`: Entità con URL immagine (Wikipedia + DuckDuckGo)

---

## 4. POST /api/script-pipeline/associate-stock

Associa clip dallo StockDB alle frasi.

**Corrisponde a**: `full_entity_script.py` - `match_drive_clips_by_entities()`

### Request
```json
{
  "segments": [{"index": 0, "text": "Gervonta Davis..."}],
  "entities": ["Gervonta Davis", "boxing"],
  "topic": "Gervonta Davis"
}
```

### Response
```json
{
  "ok": true,
  "segment_data": [
    {
      "segment_index": 0,
      "clips": [
        {
          "clip_id": "abc123",
          "filename": "boxing_fight.mp4",
          "folder_path": "sports/boxing",
          "drive_link": "https://drive.google.com/file/d/abc123/view",
          "confidence": 0.8,
          "matched_term": "Gervonta Davis"
        }
      ]
    }
  ],
  "all_clips": [...],
  "stock_folder": "sports",
  "stock_folder_url": "https://drive.google.com/drive/..."
}
```

---

## 5. POST /api/script-pipeline/associate-artlist

Associa clip Artlist alle frasi.

**Corrisponde a**: `full_entity_script.py` - `match_artlist_clips()`

### Request
```json
{
  "segments": [{"index": 0, "text": "Gervonta Davis won the championship"}],
  "entities": ["championship", "boxing"]
}
```

### Response
```json
{
  "ok": true,
  "segment_data": [
    {
      "segment_index": 0,
      "clips": [
        {
          "clip_id": "artlist_001",
          "name": "Epic Champion Win.mp4",
          "term": "Gervonta Davis won the championship",
          "url": "https://drive.google.com/file/d/...",
          "folder": "Artlist/Sports"
        }
      ],
      "search_terms": ["people", "champion"]
    }
  ],
  "all_clips": [...]
}
```

**Keyword mapping interno:**
- `

boxing

`, 

fight

 → "people"
- `

city

`, `

urban

` → "city"
- `

technology

`, `

computer

` → "technology"

---

## 6. POST /api/script-pipeline/find-keyphrases

Trova key phrases per ricerca video.

### Request
```json
{
  "script": "Gervonta Davis è un campione...",
  "entities": ["Gervonta Davis", "champion"]
}
```

### Response
```json
{
  "ok": true,
  "key_phrases": [
    {"phrase": "Gervonta Davis", "type": "direct", "confidence": 1.0},
    {"phrase": "campionato", "type": "related", "confidence": 0.7}
  ],
  "count": 2
}
```

---

## 7. POST /api/script-pipeline/download-clips

Prepara i link per il download delle clip.

### Request
```json
{
  "clips": [
    {"clip_id": "abc123", "drive_link": "https://drive.google.com/..."}
  ],
  "artlist_clips": [
    {"clip_id": "art_001", "url": "https://drive.google.com/..."}
  ],
  "destination": "/tmp/clips"
}
```

### Response
```json
{
  "ok": true,
  "downloaded": [
    "https://drive.google.com/...",
    "https://drive.google.com/..."
  ],
  "failed": [],
  "download_url": "https://drive.google.com/drive/folders/..."
}
```

---

## 8. POST /api/script-pipeline/translate

Traduce testo in multilingua parallelo (goroutines).

### Request
```json
{
  "text": "Gervonta Davis è un campione di boxe",
  "languages": ["en", "es", "fr", "de"]
}
```

### Response
```json
{
  "ok": true,
  "translations": [
    {"language": "en", "text": "Gervonta Davis is a boxing champion"},
    {"language": "es", "text": "Gervonta Davis es un campeón de boxeo"}
  ]
}
```

---

## 9. POST /api/script-pipeline/create-doc

Crea documento finale Google Docs con tutti i dati.

### Request
```json
{
  "title": "Gervonta Davis Script",
  "topic": "Gervonta Davis",
  "duration": 80,
  "template": "biography",
  "script": "Gervonta Davis è nato...",
  "language": "it",
  "segments": [{"index": 0, "text": "..."}],
  "entities": [{"type": "person", "value": "Gervonta Davis"}],
  "stock_clips": [{"clip_id": "abc", "filename": "...", "drive_link": "..."}],
  "frasi_importanti": ["..."],
  "nomi_speciali": ["Gervonta Davis"],
  "parole_importanti": ["campione"],
  "entita_con_immergine": [{"entity": "Gervonta Davis", "image_url": "..."}]
}
```

### Response
```json
{
  "ok": true,
  "doc_id": "1abc123...",
  "doc_url": "https://docs.google.com/document/d/1abc123..."
}
```

---

## Flow Completo

```
┌─────────────────────────────────────────────────────────────────┐
│                    PIPELINE COMPLETA                          │
└─────────────────────────────────────────────────────────────────┘
                          │
    ┌─────────────────────┼─────────────────────┐
    │                     │                     │
    ▼                     ▼                     ▼
┌──────────┐       ┌──────────┐         ┌──────────┐
│ /generate│       │  /divide  │         │/extract- │
│  -text   │──────▶│          │────────▶│ entities │
└──────────┘       └──────────┘         └────┬─────┘
                                              │
                    ┌──────────────────────────┼──────────────────┐
                    │                          │                  │
                    ▼                          ▼                  ▼
            ┌─────────────┐           ┌────────────┐    ┌────────────┐
            │/associate-   │           │/associate- │    │/translate  │
            │   stock       │           │  artlist   │    │            │
            └──────┬───────┘           └─────┬──────┘    └─────┬──────┘
                   │                         │                 │
                   └─────────────────────────┼─────────────────┘
                                             │
                                             ▼
                                  ┌─────────────────┐
                                  │  /create-doc    │
                                  │  (finale)       │
                                  └─────────────────┘
```

---

## Parità Python vs Go

| Feature | Python (`full_entity_script.py`) | Go API |
|---------|----------------------------------|--------|
| Generazione | `generate_script_with_ollama()` | POST `/generate-text` |
| Divisione | Segmenti da 20s | POST `/divide` |
| Entity Extraction | `extract_all_entities_with_ollama()` | POST `/extract-entities` |
| Generic Images | Wikipedia + DDG | Integrato in `/extract-entities` |
| Stock Matching | `match_drive_clips_by_entities()` | POST `/associate-stock` |
| Artlist Matching | `match_artlist_clips()` | POST `/associate-artlist` |
| YouTube Search | `search_youtube_for_sentences()` | Non in Go (usa `/clip/suggest`) |
| Documento | File locale JSON | POST `/create-doc` → Google Docs |

---

## Error Handling Standard

Tutti gli endpoint ritornano:

```json
// Successo
{ "ok": true, ... }

// Errore
{
  "ok": false,
  "error": "messaggio di errore"
}
```

**HTTP Status Codes:**
- `200 OK` - Successo
- `400 Bad Request` - Input non valido
- `500 Internal Server Error` - Errore server

---

## Avvio Server

```bash
cd /home/pierone/Pyt/VeloxEditing/refactored/go-master
export VELOX_PORT=8000
export OLLAMA_ADDR=http://localhost:11434

# Build
make build

# Run
./server

# O direttamente
go run cmd/server/main.go
```

**Log file:** `go-master/server.log`

---

## Testing

```bash
# Test entity extraction
curl -X POST http://localhost:8000/api/script-pipeline/extract-entities \
  -H "Content-Type: application/json" \
  -d '{
    "segments": [{"text": "Gervonta Davis is a boxer from Baltimore"}]
  }'

# Test full pipeline con script
curl -X POST http://localhost:8000/api/script-pipeline/generate-text \
  -H "Content-Type: application/json" \
  -d '{"topic": "Gervonta Davis", "duration": 80}'
```
