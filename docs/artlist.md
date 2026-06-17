# Artlist Integration — Documentazione Completa

## Indice

1. [Architettura Generale](#1-architettura-generale)
2. [Componenti e File Chiave](#2-componenti-e-file-chiave)
3. [Search Flow](#3-search-flow)
4. [Download Flow](#4-download-flow)
5. [Pipeline Artlist (download + Drive upload)](#5-pipeline-artlist-download--drive-upload)
6. [artlist_phrases Flow (script → Qdrant → Drive link)](#6-artlist_phrases-flow-script--qdrant--drive-link)
7. [Qdrant Vector Spaces](#7-qdrant-vector-spaces)
8. [Configurazione](#8-configurazione)
9. [Test e Diagnostica](#9-test-e-diagnostica)
10. [Known Issues & Troubleshooting](#10-known-issues--troubleshooting)

---

## 1. Architettura Generale

```
┌──────────────────────────────────────────────────────────────────┐
│                        Script Generation                         │
│  LLM → extract artlist_phrases IT → artlistSearchPhrase IT→EN    │
│  → searchScriptAssets → Qdrant → DriveLink                       │
└──────────────────────────┬───────────────────────────────────────┘
                           │
┌──────────────────────────▼───────────────────────────────────────┐
│                    Artlist Search Flow                            │
│  searchLiveWithFallbacks → fallback chain:                       │
│    1. DB Provider (clip già in database)                         │
│    2. Cached Scraper (cache in-memory + Node.js scraper server)  │
│    3. Pixabay API (fallback gratuito)                            │
│    4. Pexels API (fallback gratuito)                             │
└──────────────────────────┬───────────────────────────────────────┘
                           │
┌──────────────────────────▼───────────────────────────────────────┐
│                    Artlist Download Pipeline                       │
│  enqueueArtlistRun → Job Queue → mediaProcessor.Process           │
│  → downloadViaScraper (Node.js + Chromium) → Drive upload        │
└──────────────────────────────────────────────────────────────────┘
```

### SQLite + Qdrant Dual Store

- **`media.db.sqlite`** (`data/media/media.db.sqlite`): Canonical metadata store (tabella `media_assets`)
- **Qdrant** (porta 6333): Real-time semantic index (collezione `media_assets`)
- Ogni asset esiste in entrambi i DB con lo stesso `asset_id` / `id`

---

## 2. Componenti e File Chiave

### Node.js Scraper (`node-scraper/`)

| File | Ruolo |
|------|-------|
| `artlist_server.js` | **HTTP server** (porta 9123): `POST /search`, `POST /download`, `GET /health` |
| `artlist_search.js` | **Script CLI**: search Artlist da terminale, esporta `searchArtlist()` |
| `src/artlist/search-page.js` | **Search page logic**: API interception + scroll DOM + detail page batch |
| `src/artlist/api-interception.js` | **API response interception**: cattura risposte GraphQL/XHR di Artlist |
| `src/artlist/detail-page.js` | **Detail page fetcher**: apre singola clip page, cattura stream HLS |
| `src/artlist/download.js` | **Downloader**: browser interaction (scroll+play), HLS segment download |
| `src/artlist/browser.js` | **Browser lifecycle**: Chromium launch/close persistente |
| `src/artlist/cookies.js` | **Cookie export**: esporta cookies per yt-dlp |
| `src/artlist/scoring.js` | **Relevance scoring**: `scoreClipRelevance()`, `isRelevantClip()` |
| `src/artlist/url.js` | **URL utilities**: `extractClipId()`, `normalizeLinks()` |

### Go Backend (`internal/`)

| File | Ruolo |
|------|-------|
| `sources/artlist/service.go` | **Artlist service**: entry point, dipendenze |
| `sources/artlist/search_service.go` | **Search service**: `SearchLive()`, `SearchLiveAndSave()`, `DiscoverAndQueueRun()` |
| `sources/artlist/search_fallback.go` | **Fallback chain**: DB → Cached Scraper → Pixabay → Pexels |
| `sources/artlist/provider_scraper.go` | **Scraper provider**: chiama Node.js server `POST /search` |
| `sources/artlist/provider_cache.go` | **Cache provider**: in-memory live search cache |
| `sources/artlist/provider_db.go` | **DB provider**: cerca clip nel database |
| `sources/artlist/provider_pixabay.go` | **Pixabay fallback**: API Pixabay per video |
| `sources/artlist/provider_pexels.go` | **Pexels fallback**: API Pexels per video |
| `sources/artlist/search_cache.go` | **Persistent cache**: SQLite artlist_search_cache table |
| `sources/artlist/run_orchestrator_service.go` | **Pipeline orchestrator**: `RunTag()` — esecuzione pipeline completa |
| `sources/artlist/run_orchestrator_stages.go` | **Pipeline stages**: discover, resolve destination, build inputs, process batch, persist, enrich, index |
| `sources/artlist/job_handler.go` | **Job handler**: `HandleJob()` per `media.artlist` |
| `sources/artlist/job_codec.go` | **Job codec**: serializza/deserializza request/response per job queue |
| `sources/artlist/dto_run.go` | **DTOs**: `RunTagRequest`, `RunTagResponse`, `RunTagItem` |
| `sources/artlist/semantic_enricher.go` | **Semantic enrichment**: arricchisce clip con tag semantic in background |
| `sources/artlist/destination_service.go` | **Drive destination**: risolve folder Drive per termine di ricerca |
| `media/mediaasset/processor_download.go` | **Download step**: `downloadStep()`, `downloadViaScraper()` |
| `media/mediaasset/processor.go` | **Media processor**: `DownloadProcessUpload()` — download + process + hash + Drive upload |

### Script Flow Handlers (`internal/api/handlers/script/handlers/`)

| File | Ruolo |
|------|-------|
| `flow_insights.go` | **Insights builder**: `buildGeneratedScriptInsights()` — costruisce `ScriptInsights` con `ArtlistClipSuggestions` |
| `flow_clips.go` | **Clip search**: `searchArtlistClips()`, `artlistSearchPhrase()`, `searchScriptAssets()`, `filterSearchAssets()` |
| `flow_entities.go` | **Entity extraction**: `extractGeneratedScriptEntities()` — LLM estrae `artlist_phrases` dal script |

---

## 3. Search Flow

### 3.1 API Endpoint

```
POST /api/artlist/search/live?term=ancient+ruins&limit=3
```

Usa `c.Query("term")` — parametri query URL, non body JSON.

### 3.2 Fallback Chain

```
searchLiveWithFallbacks(term, limit)
│
├─ 1. DB Provider: cerca clip in `media_assets` per source='artlist'
│   └─ match LIKE su name/tags/search_terms
│
├─ 2. Cached Scraper Provider:
│   ├─ Check cache in-memory (TTL configurabile, default 24h)
│   │   ├─ HIT → ritorna clip cached (~14ms)
│   │   └─ MISS → chiama scraper server
│   └─ Background refresh quando cache > 75% TTL
│
├─ 3. Pixabay API: se API key configurata
│
└─ 4. Pexels API: se API key configurata
```

### 3.3 Node.js Scraper Search

```
searchArtlist(term, limit, profileDir, existingBrowser)
│
├─ Phase 1: API Interception (~1-2s)
│   ├─ setupApiInterception → cattura risposte GraphQL/XHR
│   ├─ extractClipsFromApiResponses → cerca clip-like objects
│   └─ Se trova clip con stream URL validi → FAST PATH (ritorna subito)
│
└─ Phase 2: Detail Pages (~20-30s) — se fast path non ha trovato stream
    ├─ Scroll DOM → collega link clip page
    └─ fetchClipDetailsBatch → apre ogni clip page in Chromium
       ├─ Cattura URL stream HLS da network requests
       ├─ Cerca nel DOM video.src, <source>, regex .m3u8/.mp4
       └─ Ritorna clip con primary_url (HLS stream)
```

**Nota (giugno 2026)**: Artlist NON espone stream URL nelle API di ricerca. Solo metadati (ID, titolo). La fast path non viene quasi mai presa — il sistema cade sempre in Phase 2.

### 3.4 SearchLiveAndSave

```go
func SearchLiveAndSave(ctx context.Context, term string, limit int) (*SearchResponse, error)
```

1. Chiama `SearchLive()` → fallback chain
2. Per ogni clip trovato:
   - Crea `MediaAsset` con `ID`, `Name`, `MediaType="video"`, `DownloadLink=PrimaryURL`, `ClipPageURL=ClipPageURL`
   - Preserva campi esistenti (LocalPath, DriveLink, ecc.) se clip già in DB
   - `UpsertClip()` → salva in `media_assets`
   - `UpdateSearchTerms()` → indicizza termini di ricerca
   - `EnrichAsync()` → arricchimento semantico in background
3. Ritorna `SearchResponse{Clips: []MediaAsset}`

---

## 4. Download Flow

### 4.1 Scraper Download Endpoint

```
POST http://127.0.0.1:9123/download
Content-Type: application/json

{
  "clip_page_url": "https://artlist.io/stock-footage/clip/.../561477",
  "clip_id": "561477",
  "output_dir": "/tmp/artlist_downloads"
}
```

### 4.2 Browser Interaction (download.js)

Artlist carica lo stream HLS solo dopo interazione utente. Il downloader:

```
downloadClipVideo(browser, clipPageUrl, clipId, outputDir)
│
├─ 1. Apre clip page in Chromium (waitUntil: 'networkidle2')
├─ 2. Aspetta video player (timeout 15s)
├─ 3. Scroll video into view
├─ 4. Click play (video.play() + button click fallback)
├─ 5. Aspetta 3s per catturare richiesta HLS .m3u8
├─ 6. Cattura URL stream da:
│   ├─ network listeners (request + response)
│   ├─ DOM video.src / video.currentSrc
│   └─ regex nel HTML (.m3u8, .mp4, cdn URLs)
├─ 7. Scarica segmenti HLS con cookies del browser
│   ├─ Parsing master playlist → media playlist (best bandwidth)
│   ├─ AES-128 key download (se criptato)
│   └─ Segment concatenation → .ts file
└─ 8. Ritorna { local_path, file_size }
```

**Fix giugno 2026**: Aggiunto scroll-to-view + click-play. Prima il downloader aspettava passivamente 2s → `streamUrls[]` vuoto → errore `No video stream URL found`.

### 4.3 Go → Scraper Bridge

```go
func (p *Processor) downloadViaScraper(ctx context.Context, input AssetInput, rawPath string) (string, error)
```

In `processor_download.go`:

1. Condizione per usare lo scraper:
   ```go
   if p.ffmpeg != nil && (p.isHLSURL(input.SourceURL) || input.ClipPageURL != "") && p.isArtlistURL(input.SourceURL)
   ```
   - `isHLSURL`: SourceURL contiene `.m3u8`
   - `isArtlistURL`: SourceURL contiene "artlist" o "cdn.artlist"
   - **Fix giugno 2026**: Aggiunto `|| input.ClipPageURL != ""` perché l'primary_url di Artlist a volte manca di `.m3u8`

2. Payload inviato allo scraper:
   ```json
   {
     "clip_page_url": "https://artlist.io/stock-footage/clip/...",
     "clip_id": "561477",
     "output_dir": "/data/media/tmp/"
   }
   ```

3. Timeout: 5 minuti per richiesta

4. Fallback chain se scraper fallisce:
   - `isArtlistURL` true → scraper → FFmpeg HLS remux → yt-dlp

---

## 5. Pipeline Artlist (download + Drive upload)

### 5.1 API Endpoint

```
POST /api/artlist/run
Content-Type: application/json

{
  "term": "ancient ruins",
  "limit": 1,
  "dry_run": false,
  "concurrency": 3
}
```

Usa body JSON, **non** query parameters.

### 5.2 Pipeline Stages

```
RunTag(ctx, req)
│
├─ Stage 1: Discover Clips
│   └─ SearchLiveAndSave(term, limit) → fallback chain → salva in DB
│
├─ Stage 2: Resolve Destination
│   └─ destinationService.ResolveDestination(term, rootFolderID)
│       → Drive folder for term
│
├─ Stage 3: Build Process Inputs
│   └─ Per ogni clip:
│       SourceURL = DownloadLink || ExternalURL
│       ProcessInput{ ID, Name, SourceURL, ClipPageURL, Term, FolderID, ... }
│
├─ Stage 4: Process Batch (parallel, concurrency 3-10)
│   └─ mediaProcessor.Process(input):
│       ├─ downloadStep → scraper → file .ts
│       ├─ processStep → ffmpeg normalize → .mp4
│       ├─ hashStep → file hash
│       └─ Drive upload → uploader.UploadFile → DriveLink
│
├─ Stage 5: Persist Results
│   └─ UpsertClip() con LocalPath, DriveLink, DriveFileID, FileHash
│
├─ Stage 6: Enrich Async
│   └─ semanticEnricher.EnrichAsync() → search_text + embedding
│
└─ Stage 7: Index Async
    └─ clipIndexer.IndexClip() → Qdrant upsert
```

### 5.3 Job System

La pipeline è eseguita come job async (`media.artlist`):

```go
EnqueueRequest{
    Type:       models.JobTypeArtlistRun,
    Payload:    { term, limit, root_folder_id },
    MaxRetries: 3,
    ActiveKey:  RunDedupKey(term, rootFolderID, strategy, dryRun),
}
```

**Retry**: max 3, exponential backoff (2s, 4s, 8s, ... 30s cap)

---

## 6. artlist_phrases Flow (script → Qdrant → Drive link)

### 6.1 Flusso Completo

```
Script Generation (LLM)
│
├─ ExtractEntitiesFromScriptWithModel(script)
│   └→ JSON: { segment_entities: [{ artlist_phrases: [ ... ] }] }
│
├─ buildGeneratedScriptInsights()
│   ├─ Parsing entitiesJSON → ScriptInsights.ArtlistPhrases
│   └─ searchArtlistClips(ctx, title, phrases)
│
├─ searchArtlistClips()
│   ├─ Per ogni frase IT:
│   │   ├─ artlistSearchPhrase(phrase)
│   │   │   └→ Ollama TranslateTextWithModel(phrase, "english")
│   │   │      (modello leggero: cfg.External.OllamaMetadataModel)
│   │   │
│   │   ├─ contextualQuery(title, translatedPhrase)
│   │   │   └→ "keyword1 keyword2 ... translated_words"
│   │   │
│   │   ├─ searchScriptAssets(queries, artlistTarget, limit)
│   │   │   └→ realtimeSvc.SearchClips(query, "artlist", "video", limit, minScore)
│   │   │      └→ Qdrant hybrid search (text + transcript + BM25)
│   │   │
│   │   └─ filterSearchAssets()
│   │       └→ Artlist clips: ESENTATI dal filtro topicRelevant
│   │          (perché nomi in qualsiasi lingua, topic keywords in EN)
│   │
│   └─ Se nessun clip trovato:
│       └─ enqueueArtlistBackgroundJob(translatedPhrase)
│          (job Artlist per scaricare nuovi clip)
│
└─ Risultato: ScriptArtlistClipSuggestion{ Phrase, Clips: [{ Name, DriveLink, Score }] }
```

### 6.2 Traduzione IT → EN

```go
func (h *ScriptFlowHandler) artlistSearchPhrase(ctx context.Context, phrase string) string {
    translated, err := h.generator.TranslateTextWithModel(ctx, phrase, "english", h.metadataModel)
    // Fallback: ritorna frase originale se traduzione fallisce
}
```

- Usa `h.metadataModel` (config: `external.ollama_metadata_model`, default: `gemma4:e4b`)
- ~100ms per traduzione
- Solo traduzione → non altera la risposta all'utente (frase IT originale preservata)

### 6.3 Ricerca Qdrant

```go
realtimeSvc.SearchClips(ctx, query, source, mediaType, limit, minScore)
```

| Parametro | Valore |
|-----------|--------|
| `query` | Frase tradotta EN + contesto tema |
| `source` | `"artlist"` |
| `mediaType` | `"video"` |
| `limit` | 2 |
| `minScore` | 0.7 |

**Filtro `topicRelevant`**: Artlist è esentato perché i nomi dei clip possono essere in qualsiasi lingua mentre le keyword del topic sono in inglese.

### 6.4 Background Enqueue

Se nessun clip trovato in Qdrant, accoda un job Artlist background:

```go
EnqueueRequest{
    Type:       models.JobTypeArtlistRun,
    Payload:    { term: translatedPhrase, limit: 3, root_folder_id },
    MaxRetries: 2,
    ActiveKey:  "artlist:" + phrase + ":" + rootFolderID,
}
```

Timeout contesto: 10 secondi per l'enqueue.

### 6.5 Esempio Output

```json
{
  "artlist_phrases": [
    "rovine romane antiche",
    "colate laviche Vesuvio"
  ],
  "artlist_clip_suggestions": [
    {
      "phrase": "rovine romane antiche",
      "clips": [
        {
          "name": "Basilica, Landscape, Italy, Rome",
          "score": 0.815,
          "drive_link": "https://drive.google.com/file/d/...",
          "source": "artlist"
        }
      ]
    }
  ]
}
```

### 6.6 Qdrant Semantic Search Test (giugno 2026)

Test condotto con embedding multilingual-e5-base (768 dims):

| Frase IT | Top clip match | Score |
|----------|---------------|-------|
| "rovine romane antiche" | Basilica, Landscape, Italy, Rome | 0.815 |
| | Ruins Columns Stone Italy | 0.813 |
| | Ancient Ruins Ocean Tigani | 0.805 |
| "colate laviche vulcano" | Smoking Magma Eruption Volcanic | 0.819 |
| | Pile, Powder, Dust, Blowing | 0.814 |
| | Guatemala, Volcanic, Black, Dust | 0.813 |

Tutti i clip hanno Drive link validi.

---

## 7. Qdrant Vector Spaces

### 7.1 Collection: `media_assets`

| Vector | Dims | Model | Purpose |
|--------|------|-------|---------|
| `text` | 768 | multilingual-e5-base | Semantic meaning (title + summary + topics) |
| `transcript` | 768 | multilingual-e5-base | Whisper transcript content (YouTube clips) |
| `visual` | 512 | CLIP ViT-B-32 | Visual content (images, video frames) |
| `audio` | 512 | CLAP HTSAT | Audio content (SFX, music) |
| `bm25_text` | sparse | Client-side BM25 | Lexical exact-match (keyword search) |

### 7.2 Payload Filters

```json
{
  "source": "artlist",
  "media_type": "video",
  "drive_link": "https://drive.google.com/file/d/..."
}
```

### 7.3 Search Parameters

- **Dense ANN**: cosine similarity su `text` vector
- **Hybrid RRF**: dense text + transcript + BM25 sparse fused via Reciprocal Rank Fusion
- **Reranker** (opzionale): CrossEncoder post-Qdrant reorder (BGE-reranker-v2-m3)
- **Score Blending**: `final = qdrantScore * 0.65 + rerankScore * 0.35`

---

## 8. Configurazione

### 8.1 `config.yaml`

```yaml
external:
  artlist_scraper_server_url: "http://127.0.0.1:9123"
  artlist_live_search_cache_ttl_hours: 24
  ollama_metadata_model: "gemma4:e4b"  # per traduzione artlist_phrases
  pixabay_api_key: ""  # fallback gratuito
  pexels_api_key: ""   # fallback gratuito

drive:
  artlist_root_folder: "1Dj3-BlM9LcJr3dh3I4VxEDuMBbaBwwSE"

scripts:
  max_insight_entities: 12  # max artlist_phrases da LLM
  clip_search_min_score: 0.7

video:
  duration: 15  # durata clip in secondi

vector_search:
  realtime_enabled: true
```

### 8.2 Scraper Server (systemd)

Service: `artlist-scraper` (porta 9123)
```
/etc/systemd/system/artlist-scraper.service
```

Configurabile via:
- `ARTLIST_SCRAPER_PORT` (default: 9123)
- `CHROME_PROFILE_DIR` (cookie persistente)

### 8.3 Clip Download Test

```bash
# Test search
curl -s 'http://127.0.0.1:9123/search' -X POST \
  -H 'Content-Type: application/json' \
  -d '{"term":"volcano","limit":2}'

# Test download
curl -s 'http://127.0.0.1:9123/download' -X POST \
  -H 'Content-Type: application/json' \
  -d '{
    "clip_page_url": "https://artlist.io/stock-footage/clip/...",
    "clip_id": "test123",
    "output_dir": "/tmp/artlist_test"
  }'
```

---

## 9. Test e Diagnostica

### 9.1 Pipeline Manuale

```bash
# 1. Cerca clip live
curl -s 'http://127.0.0.1:8081/api/artlist/search/live?term=ancient+ruins&limit=1' -X POST

# 2. Esegui pipeline
curl -s 'http://127.0.0.1:8081/api/artlist/run' -X POST \
  -H 'Content-Type: application/json' \
  -d '{"term":"ancient ruins","limit":1}'

# 3. Controlla stato job
curl -s 'http://127.0.0.1:8081/api/artlist/runs/<run_id>'
```

### 9.2 Qdrant Direct Query

```bash
# Scroll clip Artlist
curl -s 'http://127.0.0.1:6333/collections/media_assets/points/scroll' \
  -H 'Content-Type: application/json' \
  -d '{
    "filter": {
      "must": [
        {"key": "source", "match": {"value": "artlist"}},
        {"key": "media_type", "match": {"value": "video"}}
      ]
    },
    "limit": 100,
    "with_payload": ["name", "drive_link"]
  }'
```

### 9.3 Diagnostica Sistema

```bash
# Diagnostics endpoint
curl -s 'http://127.0.0.1:8081/api/artlist/diagnostics?term=test' -X POST

# Health scraper
curl -s 'http://127.0.0.1:9123/health'

# Logs
journalctl -u pipelinegen --no-pager -n 100
journalctl -u artlist-scraper --no-pager -n 50
```

---

## 10. Known Issues & Troubleshooting

### 10.1 Scroll+Play Interaction

**Problema** (fixato giugno 2026): Artlist carica lo stream HLS solo dopo interazione utente (scroll + click play). Il downloader originale aspettava passivamente 2s → nessuno stream catturato → errore.

**Fix**: Aggiunto `page.evaluate()` con scrollIntoView + video.play() + attesa 3s prima di catturare URL.

**Test**: Download 22.6 MB confermato funzionante.

### 10.2 .m3u8 Extension Missing

**Problema** (fixato giugno 2026): L'`primary_url` di Artlist a volte manca di estensione `.m3u8`. La condizione `isHLSURL()` (check `.m3u8`) impediva l'uso dello scraper.

**Fix**: Condizione cambiata in `isHLSURL(input.SourceURL) || input.ClipPageURL != ""`.

### 10.3 API Interception Fast Path

**Nota**: Artlist non espone stream URL nelle API di ricerca (GraphQL/XHR). La fast path in `search-page.js` (Phase 1: API Interception) non viene quasi mai presa. Il sistema cade sempre in Phase 2 (detail pages), che richiede 20-30s.

### 10.4 Cache Stale

La `artlist_search_cache` in SQLite ha TTL 48h. Se i clip su Artlist vengono aggiornati, la cache potrebbe restituire risultati vecchi.

**Reset**: La cache viene invalidata al riavvio del server.

### 10.5 Worker Queue Bloccata

**Problema**: Job `media.generate_missing_asset` senza handler registrato bloccano la coda.

**Fix**: Cancellare i job bloccati:
```sql
UPDATE jobs SET status='cancelled' WHERE type='media.generate_missing_asset';
UPDATE jobs SET worker_id='', lease_expiry=NULL WHERE status='queued';
```

### 10.6 Drive Upload Fallito

Se il Drive upload fallisce (es. OAuth scaduto), la pipeline salva comunque il file localmente e logga un warning. Il file può essere caricato manualmente in seguito.

**Rigenera token**:
```bash
python3 scripts/generate_drive_token.py
```

### 10.7 Artlist CDN Cookie Expiry

I cookies di Artlist scadono periodicamente. Lo scraper usa un browser persistente (systemd service), ma se i cookies scadono, il download fallisce.

**Refresh cookies**: Riavviare lo scraper `sudo systemctl restart artlist-scraper`.

