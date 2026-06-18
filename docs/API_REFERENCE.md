# PipelineGen API Reference

This guide contains ready-to-use examples for interacting with the PipelineGen backend from any computer on the network.

## 🛠️ Initial Setup

Tutte le chiamate richiedono l'autenticazione tramite Bearer Token.
**Token attivo:** `<YOUR_ADMIN_TOKEN>`
**Indirizzo Server:** Sostituisci `77.93.152.122` con l'IP effettivo del server se dovesse cambiare.

---

## 🟢 1. Health & System

### 1.1 Controlla se il server è online
```bash
curl -i http://77.93.152.122:8080/api/health \
  -H "Authorization: Bearer <YOUR_ADMIN_TOKEN>"
```

### 1.2 System Doctor (Module status details)
```bash
curl -i http://77.93.152.122:8080/api/system/doctor \
  -H "Authorization: Bearer <YOUR_ADMIN_TOKEN>"
```

### 1.3 Avvia Manutenzione Manuale (Log Pruning & Orphan Cleanup)
```bash
curl -i -X POST "http://77.93.152.122:8080/api/system/cleanup?deep=true" \
  -H "Authorization: Bearer <YOUR_ADMIN_TOKEN>"
```

---

## 🎙️ 2. Voiceover (Sintesi Vocale)

### 2.1 Genera un singolo Voiceover
```bash
curl -i -X POST http://77.93.152.122:8080/api/media/voiceover/generate \
  -H "Authorization: Bearer <YOUR_ADMIN_TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{
    "text": "Questo è un test del modulo voiceover di PipelineGen.",
    "language": "it",
    "filename": "test_audio_01.mp3"
  }'
```

### 2.2 Genera Voiceover Promozionali Multi-Lingua

Traduce il testo sorgente in più lingue tramite Ollama e genera un voiceover per ciascuna. L'operazione parte in **modalità async** (goroutine con timeout 10 minuti) — la risposta viene restituita immediatamente.

```bash
curl -i -X POST http://77.93.152.122:8080/api/media/voiceover/promo \
  -H "Authorization: Bearer <YOUR_ADMIN_TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{
    "text": "Scopri la nostra nuova collezione estiva. Qualità premium, stile senza tempo.",
    "drive_folder_id": "1ABC...",
    "dry_run": false
  }'
```

**Parametri della request:**
| Campo | Tipo | Obbligatorio | Descrizione |
|-------|------|-------------|-------------|
| `text` | string | **Sì** | Testo sorgente da tradurre e sintetizzare |
| `drive_folder_id` | string | No | ID cartella Google Drive (usa la cartella voiceover di default se omesso) |
| `dry_run` | bool | No (default: `false`) | Se `true`, traduce solo senza generare audio — restituisce subito le traduzioni |
| `languages` | []string | No | Override lingue (es. `["en-US", "it-IT"]`). Se omesso, usa le 13 lingue promo di default |

**Lingue promo di default (13):**
`en-US` (English), `es-ES` (Spanish), `fr-FR` (French), `de-DE` (German), `it-IT` (Italian), `pt-BR` (Portuguese), `pl-PL` (Polish), `nl-NL` (Dutch), `ja-JP` (Japanese), `ko-KR` (Korean), `ru-RU` (Russian), `tr-TR` (Turkish), `id-ID` (Indonesian)

**Risposta (async — generazione in background):**
```json
{
  "ok": true,
  "action": "promo_started",
  "message": "Translating to 13 languages and generating voiceovers (async)"
}
```

**Risposta (dry_run — traduzione sola):**
```json
{
  "ok": true,
  "total": 2,
  "success": 2,
  "failed": 0,
  "results": [
    {
      "ok": true,
      "language": "en-US",
      "translated": "Discover our new summer collection. Premium quality, timeless style."
    },
    {
      "ok": true,
      "language": "it-IT",
      "translated": "Scopri la nostra nuova collezione estiva. Qualità premium, stile senza tempo."
    }
  ]
}
```

> **Nota:** In modalità non-dry-run, i voiceover generati vengono caricati automaticamente su Google Drive nella cartella specificata. Monitora i log del server per lo stato di completamento (la generazione è fire-and-forget).

---

## 🎬 3. Artlist (Clip Search)

### 3.1 Smart Search (Smart Pipeline)
Downloads matching clips using a preset. Returns the Job ID (see Jobs section).
```bash
curl -i -X POST http://77.93.152.122:8080/api/artlist/run-smart \
  -H "Authorization: Bearer <YOUR_ADMIN_TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{
    "term": "cyberpunk city",
    "preset": "youtube_1080p_7s",
    "limit": 3
  }'
```

### 3.2 Live Search (No download)
Returns metadata directly from the website via Node.js scraper.
```bash
curl -i -X POST "http://77.93.152.122:8080/api/artlist/search/live?term=nature&limit=5" \
  -H "Authorization: Bearer <YOUR_ADMIN_TOKEN>" \
  -H "X-Internal: true"
```

---

## 📺 4. YouTube (Estrazione Clip)

### 4.1 Estrai una clip da un video YouTube
Invia un job per scaricare una specifica porzione di un video YouTube.
```bash
curl -i -X POST http://77.93.152.122:8080/api/clips/process \
  -H "Authorization: Bearer <YOUR_ADMIN_TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{
    "url": "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
    "start_time": "00:00:15",
    "duration": 10,
    "subject": "estrazione di prova"
  }'
```

---

## 📝 5. Script Generation (Ollama)

### 5.1 Script Testuale (solo testo) — due endpoint dedicati

**L'endpoint legacy `POST /api/script/generate` è stato rimosso** nel consolidamento di Giugno 2026. Oggi esistono due endpoint separati per generazione text-only:

| Endpoint | Descrizione | Job type |
|----------|-------------|----------|
| `POST /api/script/generate-from-clips` | Text-only con `num_clips=0` **oppure** clip-aware con `clip_ids`/`num_clips>0` | `script.generate_from_clips` |
| `POST /api/script/generate-with-images` | Text-only + **scene images forzate ON**, entity/metadata forzati OFF (dedicato, handler `GenerateWithImages` in `handler_generate_with_images.go`, vedi §5.3) | `script.generate_from_clips` *(condiviso con `/generate-from-clips`)* |

Consulta le sezioni 5.2 e 5.3 per gli schemi e i dettagli.

---

Esempio text-only via `/generate-from-clips` (`num_clips=0`):
```bash
curl -i -X POST http://77.93.152.122:8080/api/script/generate-from-clips \
  -H "Authorization: Bearer <YOUR_ADMIN_TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{
    "topic": "Come vivere una vita semplice e prospera",
    "title": "Vita Semplice",
    "language": "it",
    "tone": "documentary",
    "duration": 90,
    "languages": ["en", "es", "fr", "de"],
    "num_clips": 0,
    "style": "documentary"
  }'
```

> **Nota (Giugno 2026):** questo body è sincrono solo se `num_clips=0` **e** `artlist_search=false` **e** `stock_search=false`. Negli altri casi (incluso il valore di default `num_clips>0` per l'auto-search) la chiamata diventa **async** e ritorna un `job_id` come in 5.2.

### 5.2 Genera Script Unificato (async) — `/api/script/generate-from-clips`

Genera script riscritto + (opzionalmente) scene con clip/artlist/stock + voiceover unificato + traduzioni. Questo è il **canonical endpoint** dopo il consolidamento di Giugno 2026. È anche l'endpoint responsabile per la **generazione di immagini da zero** (Text-to-Image) quando `num_clips=0` e viene fornito uno `style`.

**Documentazione Dettagliata:** [docs/api/ENDPOINT_GENERATE_FROM_CLIPS.md](./api/ENDPOINT_GENERATE_FROM_CLIPS.md)

**Pipeline:** `Text gen → (opzionale) clip curation (mediaCurator) / Image generation → scene segmentation → voiceover base + translations in parallelo → assets upload → done`. Per il diagramma canonico vedi `ARCHITECTURE.md` §3 e `docs/SCRIPT_PIPELINE.md` §3.

```bash
curl -i -X POST http://77.93.152.122:8080/api/script/generate-from-clips \
  -H "Authorization: Bearer <YOUR_ADMIN_TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{
    "topic": "Testo o articolo da trasformare in video explicativo...",
    "title": "Vita Semplice",
    "language": "en",
    "languages": ["it", "es", "fr"],
    "style": "documentary",
    "num_clips": 0,
    "extract_entities": true,
    "artlist_search": true,
    "stock_search": false,
    "generate_metadata": true,
    "ordering_strategy": "scene"
  }'
```

**Job type:** `script.generate_from_clips`

### 5.3 Genera Script + Scene Images — `/api/script/generate-with-images`

> **Doc-fix (June 2026 reconciliation):** la descrizione "estrae entità + genera immagini per le entità via Google Slides" è **imprecisa**. Il codice (`handler_generate_with_images.go`) forza `extract_entities=false` e `generate_metadata=false` nel payload enqueueato: quindi **entity extraction NON viene eseguita** per questo endpoint. Quello che effettivamente fa è: script text-only + scene-by-scene AI images forzate (con fallback NVIDIA/Google Vids) + Google Doc sempre creato. La richiesta di immagini per entità / nomi speciali (la vecchia semantica di questo endpoint) non esiste più — per opt-in su entity extraction chiama `/generate-from-clips` con `extract_entities=true`.

Endpoint dedicato (handler `ScriptFlowHandler.GenerateWithImages` in `handler_generate_with_images.go`). **NON è un alias** di `/generate-from-clips`: ha un proprio request type (`GenerateWithImagesRequest`) e forza un preset di flag nel payload. Entrambi gli endpoint però **enqueueano lo stesso job type** `script.generate_from_clips` (worker `HandleClipScriptGenerateJob` in `job_handler_clip_source.go`) — la differenza tra i due è solo il **preset del payload**, non la pipeline.

**Pipeline:** `Text gen (engine.WriteScript) → Entity extraction → Google Slides/Google Vids image generation → Google Doc → done`.

```bash
curl -i -X POST http://77.93.152.122:8080/api/script/generate-with-images \
  -H "Authorization: Bearer <YOUR_ADMIN_TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{
    "topic": "Leonardo da Vinci e il Rinascimento italiano",
    "language": "it",
    "tone": "documentary",
    "duration": 300,
    "target_words": 600
  }'
```

**Job type:** `script.generate_from_clips` *(condiviso con `/generate-from-clips`; il worker è `HandleClipScriptGenerateJob` in `job_handler_clip_source.go`)*

**Risultato (via `/api/jobs/:job_id/full`):** *(doc-fix June 2026: questo schema è **impreciso** perché il preset forzato di `GenerateWithImages` mette `extract_entities=false`, quindi `entities_json`/`entity_images`/`entity_image_count` NON vengono mai popolati — quelli esistono solo per chiamate a `/generate-from-clips` con `extract_entities=true`. Lo schema corretto per `/generate-with-images` è quello mostrato qui sotto)*
```json
{
  "ok": true,
  "job_id": "job_xxx",
  "status": "completed",
  "progress": 100,
  "result": {
    "script": "...",
    "word_count": 600,
    "title": "Leonardo da Vinci...",
    "language": "it",
    "scenes": [
      {
        "text": "Primo paragrafo della scena...",
        "image": "https://drive.google.com/file/d/.../view",
        "images": ["https://drive.google.com/file/d/.../view"]
      }
    ],
    "doc_url": "https://docs.google.com/document/d/.../edit",
    "doc_id": "...",
    "sound_effects": [],
    "voiceovers": []
  }
}
```

**Risposta iniziale (job enqueued):**
```json
{
  "ok": true,
  "job_id": "job_xxx",
  "status": "queued"
}
```

> **Nota (doc-fix June 2026):** un secondo blocco di "Risultato finale" precedentemente presente in questa sezione, e che mostrava `output_dir`, `scenes_count`, `metadata[]`, è stato rimosso perché quei campi non sono emessi da `HandleClipScriptGenerateJob` sotto il preset forzato di `/generate-with-images` (`extract_entities=false, generate_metadata=false`). Per lo schema completo del result vedi il blocco unico sopra e §5.2 per il caso opt-in.

> **Nota:** I campi `archive_*`, `files_*`, `cache_hits_*`, `images_generated`, `*_duration_ms` documentati nel pre-consolidamento `GenerateFromSource` flow non sono popolati dagli endpoint attuali. Vedi `docs/CHANGELOG_2026-06-03.md` §0.

### 5.4 Status Job Script Generation
```bash
curl -i http://77.93.152.122:8080/api/script/jobs/JOB_ID \
  -H "Authorization: Bearer <YOUR_ADMIN_TOKEN>"
```

> **Nota (doc-fix June 2026):** questa nota è parzialmente corretta. L'endpoint è dedicato e separato (ha `GenerateWithImagesRequest`, handler `GenerateWithImages` in `handler_generate_with_images.go`) e non è un alias di `/generate-from-clips` — ma il job type **NON è `script.generate_with_images`** (quel job type non esiste), bensì `script.generate_from_clips` (condiviso con `/generate-from-clips`); la pipeline è la stessa. La descrizione "testo → entità → immagini → Google Doc" è altresì imprecisa: entity extraction + metadata sono **forzate a false** da `handler_generate_with_images.go:93-95`; la pipeline reale è **testo → scene images forzate → Google Doc**, senza estrazione entità né metadata. I campi clip (`clip_ids`, `num_clips`, `artlist_search`, `stock_search`, ecc.) restano correttamente NON accettati (vedi `GenerateWithImagesRequest` in `types_clip_source.go:79`).

> **Parallelismo Immagini:** La generazione immagini ora avviene in parallelo grazie a 4 ottimizzazioni (isolated mode, per-slot projects, pool lock refactoring, prewarm).
> Vedi [docs/PARALLEL_IMAGE_GENERATION.md](./PARALLEL_IMAGE_GENERATION.md) per i dettagli.

---

## 🎞️ 6. Media Assets (Clips CRUD + Ricerca Semantica)

### 6.1 Crea/Inserisci un Clip con metadata (CreateClip)

Inserisce un nuovo media asset nel database. Dopo il salvataggio, parte automaticamente una pipeline di **arricchimento asincrono** (LLM tagger + clip indexer + Qdrant vector store) che rende il clip immediatamente **ricercabile tramite semantic search**.

```bash
curl -i -X POST http://77.93.152.122:8080/api/artlist/clips \
  -H "Authorization: Bearer <YOUR_ADMIN_TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{
    "id": "mia_clip_001",
    "name": "Tramonto sulla spiaggia",
    "category": "nature",
    "tags": ["tramonto", "spiaggia", "mare"],
    "media_type": "video",
    "source": "artlist",
    "local_path": "/data/media/tramonto.mp4",
    "drive_link": "https://drive.google.com/file/d/...",
    "folder_id": "1ABC...",
    "folder_path": "Nature/Tramonti"
  }'
```

**Campi supportati:**
| Campo | Tipo | Obbligatorio | Descrizione |
|-------|------|-------------|-------------|
| `id` | string | No (auto-generato) | ID univoco del clip |
| `name` | string | Sì | Titolo descrittivo (usato per LLM enrichment) |
| `source` | string | No (default: source dall'URL) | `artlist`, `youtube`, `stock`, ecc. |
| `category` | string | No | Categoria per filtraggio e arricchimento |
| `tags` | []string | No | Tag iniziali (verranno arricchiti dal tagger) |
| `media_type` | string | No | `video`, `image`, `audio` |
| `search_text` | string | No | Testo per ricerca full-text (se omesso, generato dal tagger) |
| `local_path` | string | No | Path locale del file |
| `drive_link` | string | No | Google Drive URL |
| `folder_id` | string | No | ID cartella Drive |
| `folder_path` | string | No | Path della cartella |
| `metadata` | object | No | Metadati aggiuntivi (json) |

**Source supportati nella URL:** Sostituisci `artlist` con `youtube`, `stock`, `voiceover`, ecc.

**Pipeline di arricchimento automatico (asincrono):**
1. **LLM Semantic Tagger** — genera `search_text`, `tags`, `semantic_description` dal `name` + `category`
2. **Clip Indexer** — calcola embedding vettoriali (`search_text`, `embedding_json`)
3. **Qdrant Vector Store** — upsert per ricerca ANN + BM25 ibrida

**Risposta:**
```json
{
  "ok": true,
  "source": "artlist",
  "clip_id": "mia_clip_001",
  "clip": {
    "id": "mia_clip_001",
    "name": "Tramonto sulla spiaggia",
    "source": "artlist",
    "category": "nature",
    "tags": ["tramonto", "spiaggia", "mare"],
    "media_type": "video",
    ...
  },
  "indexed": true
}
```

> **Nota:** `indexed: true` indica che il sistema ha almeno un indexer o vector store configurato. L'arricchimento effettivo avviene in background (goroutine con timeout 3 minuti).

### 6.2 Reindicizza un Clip esistente (ReindexClip)

Triggera la reindicizzazione di un clip già presente nel database. Utile dopo aggiornamenti manuali (`PATCH`) o per clip esistenti che non hanno ancora metadata semantici.

```bash
curl -i -X POST http://77.93.152.122:8080/api/artlist/clips/mia_clip_001/reindex \
  -H "Authorization: Bearer <YOUR_ADMIN_TOKEN>"
```

**Comportamento:**
- Se il clip **non ha `search_text`** ma ha un `name`, parte l'enrichment asincrono completo (LLM → indexer → vector store) — risposta immediata `"enqueued"`
- Se il clip **ha già `search_text`**, l'indexer viene eseguito **sincrono** e la risposta arriva al completamento
- Se `clipIndexer` non è configurato ma `vectorStore` sì, usa il fallback diretto su Qdrant `UpsertAsset`
- Se nessun servizio è configurato, ritorna `"skipped"` con la motivazione

**Risposte possibili:**

*Enrichment + indexing asincrono (quando `search_text` è vuoto):*
```json
{
  "ok": true,
  "action": "enqueued",
  "clip_id": "mia_clip_001",
  "method": "async_enrich+index",
  "message": "enrichment + indexing started in background"
}
```

*Indexing sincrono via clipIndexer:*
```json
{
  "ok": true,
  "action": "reindexed",
  "clip_id": "mia_clip_001",
  "method": "clip_indexer"
}
```

*Fallback upsert diretto su vector store:*
```json
{
  "ok": true,
  "action": "reindexed",
  "clip_id": "mia_clip_001",
  "method": "direct_vector_upsert"
}
```

*Saltato (nessun servizio disponibile):*
```json
{
  "ok": true,
  "action": "skipped",
  "clip_id": "mia_clip_001",
  "reason": "no indexer or vector store configured, and no search_text available"
}
```

### 6.3 Ricerca Semantica (SemanticSearch)

Cerca clip e media asset nel **vector store Qdrant** usando similarità semantica ANN (Approximate Nearest Neighbors). I risultati includono uno score di similarità coseno (0–1) e i campi principali di ogni match.

**Prerequisito:** Il clip deve essere stato indicizzato (tramite CreateClip, ReindexClip, o dalla pipeline automatica Artlist/Stock).

```bash
# Ricerca semantica di base (testuale)
curl -i "http://77.93.152.122:8080/api/semantic-search?q=tramonto+spiaggia+mare&limit=5&min_score=0.6" \
  -H "Authorization: Bearer <YOUR_ADMIN_TOKEN>"
```

```bash
# Ricerca con filtri per source e media_type
curl -i "http://77.93.152.122:8080/api/semantic-search?q=cyberpunk+city+night&source=artlist&media_type=video&limit=10&min_score=0.7" \
  -H "Authorization: Bearer <YOUR_ADMIN_TOKEN>"
```

```bash
# Ricerca su spazio vettoriale visuale (CLIP)
curl -i "http://77.93.152.122:8080/api/semantic-search?q=mountain+landscape&vector=visual&limit=5&min_score=0.5" \
  -H "Authorization: Bearer <YOUR_ADMIN_TOKEN>"
```

**Parametri query (GET):**
| Parametro | Tipo | Default | Descrizione |
|-----------|------|---------|-------------|
| `q` | string | **obbligatorio** | Testo della query di ricerca |
| `vector` | string | `text` | Spazio vettoriale: `text` (E5-768d), `visual` (CLIP-512d), `audio` (CLAP-512d) |
| `limit` | int | `10` | Numero massimo di risultati |
| `min_score` | float | `0.85` | Soglia minima di similarità coseno (0–1). Valori più bassi = più risultati ma meno precisi |
| `source` | string | — | Filtra per sistema sorgente: `artlist`, `youtube`, `stock`, `voiceover` |
| `media_type` | string | — | Filtra per tipo media: `video`, `image`, `audio` |

**Esempio di risposta:**
```json
{
  "query": "tramonto spiaggia mare",
  "vector": "text",
  "min_score": 0.6,
  "count": 3,
  "results": [
    {
      "asset_id": "mia_clip_001",
      "score": 0.87,
      "source": "artlist",
      "name": "Tramonto sulla spiaggia",
      "local_path": "/data/media/tramonto.mp4",
      "drive_link": "https://drive.google.com/file/d/...",
      "category": "nature",
      "media_type": "video",
      "tags": ["tramonto", "spiaggia", "mare"],
      "search_text": "sunset beach ocean waves golden hour nature coastal landscape sunrise horizon"
    },
    {
      "asset_id": "clip_456",
      "score": 0.72,
      "source": "youtube",
      "name": "Beach Walk at Sunset",
      "category": "travel",
      "media_type": "video",
      "tags": ["beach", "sunset", "walk"],
      "search_text": "beach walking sunset ocean waves relaxation travel"
    }
  ]
}
```

> **Nota:** I risultati arrivano da Qdrant, che esegue ANN sul vettore E5 del testo (768d) o CLIP visuale (512d). Il campo `search_text` contiene il testo arricchito generato dal semantic tagger, utile per CrossEncoder reranking. Lo score è la similarità coseno: più alto = più semanticamente simile.

---

## 🖼️ 7. AI Images (NVIDIA NIM & Gestione Immagini)

### 7.1 Genera un'immagine AI (Richiede NVIDIA_API_KEY nel config)
```bash
curl -i -X POST http://77.93.152.122:8080/api/images/generate/nvidia \
  -H "Authorization: Bearer <YOUR_ADMIN_TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{
    "prompt": "A futuristic server room glowing with neon lights, highly detailed, 8k",
    "width": 1024,
    "height": 1024
  }'
```

### 7.2 Anima un'immagine (Zoom out di 7 secondi)
*Sostituisci `HASH_IMMAGINE` con l'hash restituito dalla generazione o ricerca.*
```bash
curl -i -X POST http://77.93.152.122:8080/api/images/animate \
  -H "Authorization: Bearer <YOUR_ADMIN_TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{
    "image_hash": "HASH_IMMAGINE_QUI",
    "duration": 7
  }'
```

---

## ⚙️ 8. Jobs (Monitoraggio Attività Asincrone)

### 8.1 Lista tutti i Jobs recenti
```bash
curl -i http://77.93.152.122:8080/api/jobs \
  -H "Authorization: Bearer <YOUR_ADMIN_TOKEN>"
```

### 8.2 Dettagli di un Job specifico
*Sostituisci `JOB_ID` con l'ID reale (es. `job-12345`).*
```bash
curl -i http://77.93.152.122:8080/api/jobs/JOB_ID/full \
  -H "Authorization: Bearer <YOUR_ADMIN_TOKEN>"
```

---

## 📖 9. Lessons (Generazione Lezioni Strutturate)

Genera lezioni web strutturate da un testo sorgente. Divide automaticamente il testo in capitoli, genera il contenuto di ogni capitolo in parallelo (via Ollama Chat API), e opzionalmente crea immagini AI per capitolo + PDF unico.

### 9.1 Genera Lezione (sincrona)
Elabora il testo sorgente e restituisce la lezione completa con capitoli, immagini e PDF.
```bash
curl -i -X POST http://77.93.152.122:8080/api/lessons/generate \
  -H "Authorization: Bearer <YOUR_ADMIN_TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{
    "source_text": "L'articolo completo o testo da trasformare in una lezione strutturata. Il sistema dividerà automaticamente il contenuto in capitoli basati sulla struttura del testo.",
    "title": "Introduzione alla Programmazione",
    "language": "it",
    "tone": "educational",
    "max_chapters": 5,
    "generate_images": true,
    "image_style": "illustration",
    "image_model": "flux-1-dev",
    "generate_pdf": true
  }'
```

**Parametri della request:**
| Campo | Tipo | Default | Descrizione |
|-------|------|---------|-------------|
| `source_text` | string | **obbligatorio** | Testo sorgente completo da elaborare |
| `title` | string | auto-estratto | Titolo della lezione |
| `language` | string | `"it"` | Lingua di output |
| `tone` | string | `"educational"` | Tono narrativo (`educational`, `documentary`, `storytelling`, ecc.) |
| `model` | string | `"gemma4:e4b"` | Modello Ollama per la generazione testo |
| `max_chapters` | int | auto-calcolato | Numero massimo di capitoli (0 = automatico in base alla lunghezza) |
| `generate_images` | bool | `false` | Genera immagini AI per ogni capitolo |
| `image_style` | string | — | Stile delle immagini (`illustration`, `cinematic`, `anime`, ecc.) |
| `image_model` | string | `"flux-1-dev"` | Modello per generazione immagini |
| `image_width` | int | 1280 | Larghezza immagini generate |
| `image_height` | int | 720 | Altezza immagini generate |
| `generate_pdf` | bool | `false` | Genera file PDF unico con tutti i capitoli |
| `ollama_url` | string | config | URL Ollama override |

**Risposta (sync):**
```json
{
  "ok": true,
  "success": true,
  "title": "Introduzione alla Programmazione",
  "language": "it",
  "chapters": [
    {
      "index": 1,
      "title": "Cosa è la Programmazione?",
      "content": "Testo completo del capitolo generato...",
      "word_count": 450,
      "image": {
        "hash": "abc123",
        "path_rel": "images/abc123.png",
        "url": "http://...",
        "drive_link": "https://drive.google.com/file/d/...",
        "drive_file_id": "file_id_123",
        "prompt": "Prompt usato per generare l'immagine"
      }
    },
    {
      "index": 2,
      "title": "Variabili e Tipi di Dato",
      "content": "...",
      "word_count": 520,
      "error": ""
    }
  ],
  "total_words": 2450,
  "markdown_path": "/data/lessons/introduzione-programmazione.md",
  "pdf_path": "/data/lessons/introduzione-programmazione.pdf",
  "generated_at": "2026-06-03T15:30:00Z"
}
```

> **Nota:** Il campo `chapters[].image` è presente solo se `generate_images: true`. Ogni capitolo viene generato in parallelo (fino a 5 contemporaneamente). Se un capitolo fallisce, `error` conterrà la descrizione e gli altri capitoli continuano (graceful degradation).

### 9.2 Genera Lezione (asincrona)
Per testi lunghi o quando `generate_images: true`, usa la modalità async per non aspettare la risposta.
```bash
curl -i -X POST http://77.93.152.122:8080/api/lessons/generate \
  -H "Authorization: Bearer <YOUR_ADMIN_TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{
    "source_text": "Testo lungo da elaborare...",
    "title": "Lezione Completa",
    "generate_images": true,
    "generate_pdf": true,
    "async": true
  }'
```

**Risposta (job enqueued):**
```json
{
  "ok": true,
  "async": true,
  "job_id": "job_xxx",
  "status": "queued",
  "message": "Lesson generation enqueued. Poll /api/jobs/job_xxx/full for status.",
  "status_url": "/api/jobs/job_xxx/full"
}
```

**Monitora il progresso** tramite l'endpoint generico `/api/jobs/:job_id/full` (sezione 8.2). Il campo `progress` va da 0 a 100.

### 9.3 Lista Job di Generazione Lezioni
```bash
curl -i "http://77.93.152.122:8080/api/lessons/jobs?status=completed&limit=10" \
  -H "Authorization: Bearer <YOUR_ADMIN_TOKEN>"
```

**Parametri query (GET):**
| Parametro | Tipo | Default | Descrizione |
|-----------|------|---------|-------------|
| `status` | string | — | Filtra per stato: `queued`, `running`, `completed`, `failed` |
| `limit` | int | 20 | Numero massimo di job da restituire |
| `offset` | int | 0 | Offset per paginazione |

**Risposta:**
```json
{
  "ok": true,
  "count": 2,
  "jobs": [
    {
      "id": "job_xxx",
      "type": "lessons.process",
      "status": "completed",
      "progress": 100,
      "result": {
        "success": true,
        "title": "Lezione Completa",
        "chapters": [
          {"index": 1, "title": "...", "word_count": 450, "error": ""},
          {"index": 2, "title": "...", "word_count": 520, "error": ""}
        ],
        "total_words": 2450,
        "markdown_path": "/data/lessons/...",
        "pdf_path": "/data/lessons/...",
        "generated_at": "2026-06-03T15:30:00Z"
      },
      "created_at": "2026-06-03T15:00:00Z",
      "updated_at": "2026-06-03T15:30:00Z",
      "completed_at": "2026-06-03T15:30:00Z"
    }
  ]
}
```

> **Nota:** Il campo `result.chapters` nei job completati contiene solo riepilogo (`index`, `title`, `word_count`, `error`) senza il contenuto testuale completo, per mantenere la risposta leggera.
