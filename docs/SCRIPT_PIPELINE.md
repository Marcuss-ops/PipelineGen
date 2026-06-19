# Script Pipeline — Documentazione Completa

## Indice

1. [Architettura Generale](#1-architettura-generale)
2. [Struttura dei File Chiave](#2-struttura-dei-file-chiave)
3. [Route e Endpoint API](#3-route-e-endpoint-api)
4. [Batch Generation (generate-batch)](#4-batch-generation-generate-batch)
5. [Text Generation (generate)](#5-text-generation-generate)
6. [Generate With Images](#6-generate-with-images--apiapiscriptgenerate-with-images)
7. [Memory Gate — Gemma Memory System](#7-memory-gate--gemma-memory-system)
8. [Qualità: normalizeChapterLength](#8-qualità-normalizechapterlength)
9. [Voiceover + Traduzioni](#9-voiceover--traduzioni)
10. [Google Docs Integration](#10-google-docs-integration)
11. [Job System (Async)](#11-job-system-async)
12. [Ollama Generator & Prompts](#12-ollama-generator--prompts)
13. [Database Tables](#13-database-tables)
14. [Pulizia Markdown: cleanForVoiceover](#14-pulizia-markdown-cleanforvoiceover)
15. [Diversità Script: CacheHitLimit, Angle-Shift, Temperature](#15-diversità-script-cachehitlimit-angle-shift-temperature)
16. [Timeline System](#16-timeline-system)

---

## 1. Architettura Generale

```
┌─ HTTP Request ─────────────────────────────────────────────────┐
│  POST /api/script/generate-batch                              │
│  POST /api/script/generate                                    │
│  POST /api/script/generate-with-images                        │
│  GET  /api/script/jobs/:job_id                                │
└───────────────────────────┬────────────────────────────────────┘
                            │
┌───────────────────────────▼────────────────────────────────────┐
│  ScriptFlowHandler (handler_flow.go)                          │
│  ┌─────────────────────────────────────────────────────────┐  │
│  │  Dipendenze:                                            │  │
│  │  - generator     → ollama.Generator     (scrittura)     │  │
│  │  - memorySvc     → gemmamemory.Service   (cache+mem)    │  │
│  │  - voService     → voiceover.Service     (TTS)          │  │
│  │  - docClient     → drive.DocClient       (Google Docs)  │  │
│  │  - jobsSvc       → jobservice.Service     (async jobs)  │  │
│  │  - scriptsRepo   → scripts.ScriptRepository (DB)        │  │
│  │  - imgService    → images.Service         (AI images)   │  │
│  │  - cfg           → config.Config                        │  │
│  └─────────────────────────────────────────────────────────┘  │
└───────────────────────────┬────────────────────────────────────┘
                            │
┌───────────────────────────▼────────────────────────────────────┐
│  Ollama Generator (internal/ml/ollama/generate.go)             │
│  ├── GenerateScript()        → chat API con system + user msg │
│  ├── TranslateText()         → traduzione lingua              │
│  ├── GenerateVisualPrompt()  → testo → immagine prompt        │
│  ├── GenerateVideoMetadata() → title → descrizione + tags     │
│  └── RegenerateScript()      → rewrite da script esistente    │
└────────────────────────────────────────────────────────────────┘
```

---

## 2. Struttura dei File Chiave

### Handlers (`internal/api/handlers/script/`)

| File | Ruolo |
|------|-------|
| `handlers/handler_flow.go` | **Entry point**: registra route, definisce `ScriptFlowHandler`, implementa `GenerateText` |
| `handlers/handler_batch.go` | **Batch generation**: `GenerateBatch`, `ExecuteBatchGeneration`, `cleanForVoiceover` |
| `handlers/handler_batch_types.go` | **Tipi**: `GenerateBatchRequest`, `BatchTopic`, `ChapterStructure`, `chapterTiming` |
| `handlers/handler_batch_doc.go` | **Google Docs HTML**: `buildBatchGoogleDocHTML` |
| `handlers/handler_batch_source.go` | **Source resolver**: YouTube URL → transcript + metadata |
| `handlers/handler_batch_split.go` | **Source split**: testi lunghi divisi in chunk |
| `handlers/flow_doc.go` | **Google Doc**: `maybeCreateGoogleDoc`, `buildGeneratedTextDocContent` (unificato text+clips) |
| `handlers/script_history_handler.go` | **Script history**: `ListScripts`, `GetScriptByID` — recupero script archiviati |
| `handlers/handler_quality.go` | **Quality**: `normalizeChapterLength`, `wordCountBounds`, `buildCompressPrompt` |
| `handlers/handler_flow_ops.go` | **Operazioni**: `RegenerateSection`, `EvictCache` |
| `handlers/handler_metadata.go` | **YouTube metadata**: `GenerateVideoMetadata`, `BuildMetadataLanguages` |
| `handlers/job_handler.go` | **Background job handler**: `HandleClipScriptGenerateJob`, `HandleBatchScriptGenerateJob` |
| `handlers/job_handler_archive.go` | **Archive**: salva script generato su DB con metadati |
| `handlers/handler_batch_validation.go` | **Lingue supportate**: `supportedScriptLanguages()` (validazione principale in `handler_batch_types.go`) |
| `timeline_logic.go` | **Timeline**: costruzione piano visivo per scene |
| `timeline_types.go` | **Timeline types**: `TimelinePlan`, `TimelineSegment` |
| `timeline_render.go` | **Timeline render**: produce output strutturato |
| `timeline_cache.go` | **Timeline cache**: caching richieste timeline |
| `timeline_cache_service.go` | **Timeline Cache service**: build key, hash segment, store/load embedding |
| `timeline_asset_matcher.go` | **Asset matcher**: associa segmenti a clip/immagini |
| `timeline_segment_builder.go` | **Segment builder**: costruisce `TimelineSegment` da LLM output + normalizza + associa asset |
| `timeline_llm_client.go` | **LLM client**: chiamata Ollama per pianificazione timeline, parsing JSON |
| `timeline_prompt.go` | **Prompt builder**: `buildTimelinePlanningPrompt` → LLM per segmentazione |
| `timeline_plan_normalizer.go` | **Normalizer**: ordina, allinea timestamp, applica minimo segmenti |
| `timeline_plan_sanitizer.go` | **Sanitizer**: pulisce soggetti LLM (file path, troppo lunghi, vuoti) |
| `timeline_fallback.go` | **Fallback**: piano timeline base se LLM fallisce |
| `timeline_match_validator.go` | **Match validator**: `hasUsefulVisualMatch` — verifica match utili |
| `timeline_subject_helpers.go` | **Subject helpers**: `NormalizeRepeatedSubject`, risoluzione soggetto |
| `timeline_render_segment.go` | **Segment render**: formatta header segmento (timestamp, subject, associazione) |
| `timeline_render_assets.go` | **Asset render**: seleziona e formatta match primari (stock/Artlist) |
| `timeline_utils.go` | **Utilities**: `topicTokens` |
| `visualplanner.go` | **Visual planner**: `VisualPlan`, `VisualSubject` — pianificazione immagini per scena |
| `catalognormalizer.go` | **Catalog normalizer**: normalizza soggetti/keyword/entità contro catalogo stock/artlist/clip |
| `scoring_boosts.go` | **Scoring boosts**: `preferredCandidateBoost` — punteggi preferenze |
| `assoc_util.go` | **Association utils**: helper per segmentAssociationSubject/Keywords/Entities |
| `keywords.go` | **Keywords**: `extractDynamicKeywords` via LLM, fallback euristico |
| `script_docs_types.go` | **ScriptDocs types**: `ScriptDocsRequest`, `ScriptDocument`, `artlistIndex`, `artlistClipItem` |
| `script_docs_artlist_suggestions.go` | **Artlist suggestions**: `suggestArtlistSearchTags` via LLM per timeline |
| `script_doc_metadata.go` | **Doc metadata**: `renderMetadata` per script docs |

### Core Services

| File | Ruolo |
|------|-------|
| `internal/ml/ollama/generate.go` | **Ollama Generator**: `GenerateScript`, `TranslateText`, `GenerateVisualPrompt` |
| `internal/ml/ollama/types/types.go` | **Tipi**: `TextGenerationRequest`, `GenerationResult` |
| `internal/ml/ollama/prompts/prompt_builders.go` | **Prompt builder**: costruisce messaggi chat per Ollama |
| `internal/ml/ollama/prompts/system_prompt.go` | **System prompt**: lingua + stile |
| `internal/ml/ollama/client/client_core.go` | **Ollama client**: HTTP call al server Ollama |
| `internal/ml/ollama/client/websearch.go` | **Web search**: SearXNG integration per RAG |
| `internal/service/gemmamemory/service.go` | **Memory Gate**: `CheckGate`, `SaveAfterGeneration`, `BuildEnrichedPrompt` |
| `internal/service/gemmamemory/types.go` | **Memory types**: `MemoryPolicy`, `MemoryGateRequest`, `MemoryHit` |
| `internal/service/gemmamemory/repository.go` | **Memory repository**: CRUD su tabelle gemma |
| `internal/media/voiceover/service.go` | **Voiceover**: `GenerateWithDestination`, `SemanticTaggerFunc` |
| `internal/upload/drive/doc_client.go` | **Google Docs**: `CreateDoc`, `UpdateDoc`, `ShareDoc` |
| `Removed: scripts/scripts.go` | **Script repository**: CRUD scripts + research cache |
| `Removed: scripts/types.go` | **Script types**: `ScriptRecord`, `ScriptSectionRecord` |
| `internal/jobs/service.go` | **Job service**: `Enqueue`, `Get`, `List`, `Cancel`, `Retry` |
| `internal/jobs/worker.go` | **Job worker**: poll + lease + dispatch + retry backoff + DLQ |
| `internal/jobs/types.go` | **Job types**: `EnqueueRequest`, `HandlerFunc`, `Dispatcher` |

---

## 3. Route e Endpoint API

Tutte le route sono registrate in `handler_flow.go` → `RegisterRoutes()` sotto il gruppo `/api/script`.

| Metodo | Path | Handler | Descrizione |
|--------|------|---------|-------------|
| `POST` | `/api/script/generate-batch` | `GenerateBatch` | **Batch**: multi-item con source text, voiceover, traduzioni — job type `script.generate_batch` |
| `POST` | `/api/script/generate-with-images` | `GenerateWithImages` (dedicato, separato) | **Script + immagini scene‑by‑scene**: text‑only + scene images forzate, senza entity/metadata — job type `script.generate_from_clips` (condiviso con `/generate-from-clips`), handler `HandleClipScriptGenerateJob`. Vedi nota box 1.1 di `docs/PARALLEL_IMAGE_GENERATION.md` per la differenza di preset. |
| `POST` | `/api/script/generate-from-clips` | `GenerateFromClips` | **Script unificato**: text-only / clip-aware / auto-search — job type `script.generate_from_clips` |
| `POST` | `/api/script/generate-from-catalog` | `GenerateFromCatalog` | **Catalog query**: seleziona clip da catalogo — job type `script.generate_from_catalog` |
| `POST` | `/api/script/curate` | `Curate` | **Ricerca NL**: query linguaggio naturale → clip compilation — job type `script.curate` |
| `GET` | `/api/script/jobs/:job_id` | `GetJobStatus` | **Job status**: progresso e risultato job async |
| `POST` | `/api/script/:id/sections/:section_id/regenerate` | `RegenerateSection` | **Rigenera**: riscrive una sezione da DB |
| `POST` | `/api/script/cache/evict` | `EvictCache` | **Evict**: cancella cache memoria per titoli specifici |

---

## 4. Batch Generation (generate-batch)

### 4.1 Request Schema

```json
{
  "doc_title": "Frugal Living Bible",             // required
  "channel_id": "default-batch",                  // required
  "items": [                                       // required (o topics/outline_item)
    { "topic": "Amish Budget", "source_text": "..." }
  ],
  "language": "en",                                // default: "it"
  "tone": "documentary",                           // default: "documentary"
  "target_words_per_item": 1500,                   // default: 1800
  "duration": 600,                                 // default: 600
  "guidelines": "",                                // max 4000 chars
  "no_chapters": true,                             // flowing narrative, no headings
  "voiceover": true,                               // genera TTS
  "languages": ["it", "es"],                       // traduzioni + TTS per lingua
  "force_refresh": false,                          // bypass cache esatta
  "save_to_db": false,                             // persiste su DB
  "async": false,                                  // esegue come job background
  "include_failed_chapters": false,
  "model": "",                                     // default da config
  "item_structure": {                              // struttura opzionale capitolo
    "opening_story": true,
    "principle": true,
    "step_by_step_system": true,
    "exercise": true,
    "word_count_target": 1500
  }
}
```

### 4.2 Flow Completo

```
GenerateBatch (handler_batch.go)
│
├─ 1. Validazione (validateGenerateBatchRequest)
│   ├─ doc_title required
│   ├─ channel_id required
│   ├─ almeno 1 item/topic/outline_item
│   ├─ max 50 items
│   ├─ target_words 800-5000
│   └─ lingua supportata
│
├─ 2. Gestione default
│   ├─ language="it", tone="documentary", duration=600
│   ├─ model da config se vuoto
│   ├─ prompt_version default
│   └─ drive_folder_id da config se vuoto
│
├─ 3. Outline generation (se outline_topic dato)
│   └─ LLM genera N titoli → array JSON → batchTopics[]
│
├─ 4. Parallel Web Search (concurrency 4)
│   └─ SearXNG → webContext per ogni item
│
├─ 5. Source Resolution (resolveBatchSourceText)
│   ├─ inline_text → raw
│   ├─ YouTube URL → yt-dlp metadata + transcript
│   └─ fallback → inline
│
├─ 6. Source Split (buildBatchWorkItems)
│   └─ testi > 2500-4000 parole → split in sotto-item con [SEGMENT] tag
│
├─ 7. Memory Gate Check (per item)
│   ├─ CheckGate → EXACT CACHE HIT → salta LLM
│   ├─ CheckGate → REFERENCE HIT → BuildFreshVariantPrompt (angle-shift)
│   ├─ CheckGate → ENRICHED PROMPT → usa memoria contestuale
│   └─ CacheHitLimit → se > 2 hit → ForceRefresh
│
├─ 8. Ollama Generation (per item, con retry 3x)
│   ├─ buildChapterPrompt(topic, source, index, total, guidelines)
│   ├─ [FLOW] tag per transizione tra item
│   ├─ [SEGMENT] tag per sotto-item splittati
│   ├─ Temperature=0.7, TopP=0.95 su cache hit
│   └─ timeout 5 min per capitolo
│
├─ 9. Quality Check (normalizeChapterLength)
│   ├─ wordCount vs targetWords ± 300
│   ├─ < min → EXPAND (via LLM)
│   └─ > max → COMPRESS (via LLM)
│
├─ 10. Memory Save (SaveAfterGeneration)
│    ├─ salva output esatto (gemma_script_outputs)
│    ├─ salva chunk (gemma_script_chunks)
│    └─ estrae memories (script_structure, hook, CTA)
│
├─ 11. Google Doc Creation (buildBatchGoogleDocHTML)
│    ├─ HTML → paragrafi puliti (cleanForVoiceover)
│    ├─ noChapters → no TOC, no heading
│    └─ con chapters → TOC + sezioni con anchor
│
├─ 12. Voiceover Generation
│    ├─ cleanForVoiceover(mergedScript)
│    ├─ voService.GenerateWithDestination → edge-tts
│    └─ Drive upload → drive_link
│
├─ 13. Translation + Voiceover (per lingua aggiuntiva)
│    ├─ generator.TranslateText → testo tradotto
│    ├─ Google Doc tradotto
│    └─ voiceover per lingua tradotta
│
├─ 14. Save to DB (se save_to_db=true)
│    ├─ NextVersionForTopic → auto-increment
│    ├─ SaveScript con sections
│    └─ metadata: timings, doc_url, voiceover, translations
│
└─ 15. Response
     ├─ script (testo pulito)
     ├─ doc_url (Google Doc link)
     ├─ voiceover_link (Drive link)
     ├─ translations { lang: { script, doc_url, voiceover_link } }
     ├─ timings (per item)
     └─ failed_chapters
```

### 4.3 buildChapterPrompt

```go
func buildChapterPrompt(req, topic, sourceText, index, total, guidelines) string
```

Costruisce il prompt per l'LLM:
```
Write item X of Y.

[GUIDELINES]
...
[/GUIDELINES]

[CHAPTER_STRUCTURE]
{ "opening_story": true, ... }
[/CHAPTER_STRUCTURE]

TOPIC:
...

SOURCE TEXT:
...

TARGET WORDS:
...

STRICT REQUIREMENT: Do NOT write any chapter headings...  (if noChapters=true)
```

### 4.4 Source Split Logic (handler_batch_split.go)

- **Soglia split**: `targetWords * 2`, min 2500, max 4000 parole
- **Strategia**: paragrafi → sentences → word-level chunks
- **Output**: `[SEGMENT]` tag in prompt per informare LLM

---

## 5. Text Generation (generate, generate-with-images, generate-from-clips)

### 5.1 `POST /api/script/generate-from-clips` — endpoint unificato (text-only + clip-aware)

Endpoint principale con due modalità:
- **Text-only**: `topic` o `source_text`, `num_clips=0` — genera solo testo narrativo
- **Clip-aware**: `clip_ids=[...]` o `num_clips>0` — genera testo + scene con clip selezionate

Job type: `script.generate_from_clips`. Usa il tipo `GenerateFromClipsRequest`.

### 5.2 `POST /api/script/generate-with-images` — endpoint dedicato (text-only + scene images forzate)

> **Doc-fix (June 2026, post-consolidamento):** questo endpoint NON è un alias di `/generate-from-clips`. Ha il proprio handler (`ScriptFlowHandler.GenerateWithImages` in `handler_generate_with_images.go`) con il proprio request type (`GenerateWithImagesRequest`), ed **enqueue lo stesso job type** `script.generate_from_clips` di `/generate-from-clips`. La differenza è solo nel **preset del payload**: questo handler **forza** `extract_entities=false`, `generate_scene_images=true`, `generate_metadata=false` (i flag che il client passa sono IGNORATI per queste tre chiavi). Quindi l'effettivo output NON è "estrae entità + genera immagini per i nomi speciali" (le entità non vengono estratte), ma piuttosto "genera script + scene-by-scene AI images" (con `entities_json`/`metadata` vuoti). Per scene-by-scene image generation forzata senza entity extraction; per opt-in entity extraction/metadata usa invece `/generate-from-clips`.

### 5.3 Flusso comune (generazione testo base)

Entrambi gli endpoint condividono questo flusso base per la generazione del testo:

1. Validazione: topic required
2. Check memory gate (CheckGate)
3. Se cache hit esatto → salta Ollama
4. Se reference hit → `BuildFreshVariantPrompt`
5. Se enriched prompt → prompt arricchito con memoria
6. `GenerateScript` → Ollama
7. `SaveAfterGeneration` → memoria
8. `GenerateVideoMetadata` → description + tags per lingua
9. `maybeCreateGoogleDoc` → Google Doc opzionale
10. `SaveScript` → DB opzionale

---

## 6. Generate With Images — `/api/script/generate-with-images`

> **Doc-fix (June 2026 reconciliation):** questa sezione è una delle principali fonti di confusione nel repo — descrive un handler `HandleGenerateWithImagesJob` e un job type `script.generate_with_images` che **non esistono nel codice**. La sezione è stata riscritta di fatto sotto; il contenuto precedente è mantenuto solo dove ancora applicabile, con warning espliciti.

**Come funziona davvero (verificato dal codice, June 2026)**:

| Aspetto | Sezione §6 originale (vecchia) | Realtà del codice (June 2026) |
|---------|--------------------------------|--------------------------------|
| Job type | `script.generate_with_images` | **`script.generate_from_clips`** (enqueueato anche da `/generate-from-clips`) |
| Job handler | `HandleGenerateWithImagesJob` (riferimento inesistente) | **`HandleClipScriptGenerateJob`** (job_handler_clip_source.go; handler_file.go:20) |
| Request type | `GenerateWithImagesRequest` | **`GenerateWithImagesRequest`** ✓ (presente in types_clip_source.go:79) |
| HTTP handler | (non citato) | **`ScriptFlowHandler.GenerateWithImages`** in `handler_generate_with_images.go`, registrato a `handler_flow.go:153` |
| Pipeline | text → entity extraction → entity images → doc | **text → (entity extraction **DISABILITATA**) → scene images (`generateSceneImages`) → Google Doc** |
| Payload override | (descritto) | il handler forza `extract_entities=false`, `generate_scene_images=true`, `generate_metadata=false` e IGNORA i flag stessi se passati dal body |

**Cosa restituisce davvero `HandleClipScriptGenerateJob` per `/generate-with-images`** (sotto-preset forzato):
- `script` (testo completo)
- `word_count`, `language`
- `*ClipScriptJobResult.Scenes` — array di `ScriptSceneImage` (text + images con drive_link)
- `*ClipScriptJobResult.DocURL`, `DocID` (Google Doc creato)
- **NO** `entities_json`, **NO** `entity_images`, **NO** `metadata` (i flag relativi sono forzati a false)

**Job type:** `script.generate_from_clips` (shared)

**Job handler:** `HandleClipScriptGenerateJob` in `job_handler_clip_source.go`

**HTTP handler:** `GenerateWithImages` in `handler_generate_with_images.go`

Se vuoi davvero il legacy behaviour "estrae entità + genera immagini per i nomi speciali", questo **non esiste più** come endpoint dedicato dopo il consolidamento di Giugno 2026. Per opt-in su entity extraction: chiama `/generate-from-clips` con `extract_entities=true`. Per opt-in su metadata: `generate_metadata=true`. Per opt-in su scene images: `generate_scene_images=true`.

---

> **Per l'endpoint `/api/script/generate-from-clips`** — l'altro endpoint di generazione script, con proprio tipo `GenerateFromClipsRequest` e job type `script.generate_from_clips`:
> Vedi `internal/api/handlers/script/handlers/job_handler_clip_source.go` (pipeline `HandleClipScriptGenerateJob`).
>
> Modalità supportate:
> - **Text-only** (default): `topic` o `source_text`, `num_clips=0`
> - **Clip-aware esplicito**: `clip_ids=[...]`
> - **Auto-search**: `num_clips > 0` → `mediaCurator.Curate()`
> - **Con estrazione entità**: `extract_entities: true` → ritorna entities + insights
> - **Con metadata YouTube**: `generate_metadata: true`

---

## 7. Memory Gate — Gemma Memory System

### 7.1 Architettura

```
CheckGate (prima della generazione)
├── Level 1: Exact Cache
│   ├── NormalizeInput(channel, title, prompt) → hash
│   ├── FindExactOutput(channel, mode, hash)
│   └── Se hit → return cached output (skip LLM!)
│
├── Level 2: Memory Retrieval
│   ├── channel_style → regole del canale
│   ├── topic_key memories → structure, hook, research
│   ├── recent chunks → LIKE search
│   └── BuildEnrichedPrompt() → contesto per LLM
│
└── Level 3: Fresh Variant (se cache hit ma vogliamo varietà)
    └── BuildFreshVariantPrompt() → angle-shift + avoid list

SaveAfterGeneration (dopo la generazione)
├── SaveGeneration → gemma_script_outputs
├── SaveChunks → gemma_script_chunks
└── ExtractMemories → gemma_memory_entries
    ├── MemoryTypeScriptStructure (prime 50 parole)
    ├── MemoryTypeSuccessfulHook (primo paragrafo)
    └── MemoryTypeReusableCTA (ultimo paragrafo, se sembra CTA)
```

### 7.2 MemoryPolicy

```go
type MemoryPolicy struct {
    MaxOldOutputs       int     // default: 2 — quante memorie "past scripts" iniettare
    MaxMemoryChars      int     // default: 1800 (≈450 token)
    SimilarityThreshold float64 // default: 0.72 — n-gram Jaccard threshold
    CacheHitLimit       int     // default: 2 — dopo N cache hit → ForceRefresh
}
```

### 7.3 BuildFreshVariantPrompt (Angle-Shift)

Quando rileviamo un cache hit ma vogliamo una variante diversa, invece di restituire il testo cache, costruiamo un prompt che dice all'LLM **come** essere diverso:

1. **Angle-Shift Instructions** (6 strategie creative):
   - Angolo narrativo diverso (es. storico vs personale)
   - Hook di apertura diverso
   - Ritmo diverso (veloce vs atmosferico)
   - Nuovo esempio/aneddoto
   - Riordino sezioni
   - Chiusura diversa (domanda vs riassunto)

2. **Avoid List** (difensiva):
   - Opening estratto dal vecchio output
   - 3 frammenti corti da non usare

### 7.4 CacheHitLimit

- Default: 2 cache hit massimi sullo stesso topic
- Dopo 2 hit → `ForceRefresh=true` → bypassa cache esatta
- Conta solo gli hit nelle ultime 24 ore (finestra temporale)
- Dopo 24h i vecchi entry scadono → cache torna utile

---

## 8. Qualità: normalizeChapterLength

Dopo la generazione, ogni capitolo viene normalizzato:

```
wordCountBounds(targetWords) → [target-300, target+400]
min 800, max: nessun limite esplicito

normalizeChapterLength():
├── wordCount >= min && <= max → APPROVE ✅
├── wordCount < min → EXPAND (via LLM)
└── wordCount > max → COMPRESS (via LLM)
    └── buildCompressPrompt: keep examples, numbers, actions
                            remove repetition, filler, abstract padding
```

---

## 9. Voiceover + Traduzioni

### 9.1 Voiceover Service

- `voiceover.Service` (`internal/media/voiceover/service.go`)
- Usa `edge-tts` (via Python) per generare audio
- `GenerateWithDestination(ctx, text, lang, filename, dest)` → DriveLink
- `DestinationRequest`: `{ FolderID, Group, SubfolderName, CreateSubfolder }`
- Integrato con `audioasset.Processor` per processing audio
- `SemanticTaggerFunc` callback per arricchire metadati (search_text, tags)
- `ClipIndexFunc` callback per indicizzazione embedding + Qdrant
- `LifecycleService` per deduplicazione + upload + persistenza

### 9.2 Batch Voiceover Flow

```
ExecuteBatchGeneration:
├── cleanForVoiceover(mergedScript) → cleanScript
├── voService.GenerateWithDestination(cleanScript, lang, filename, destReq)
│   ├── audioasset.Processor.Generate → edge-tts
│   └── LifecycleService.ProcessAsset → upload Drive + persist DB
│
└── Per ogni lingua in request.Languages:
    ├── generator.TranslateText(cleanScript, lang)
    ├── Google Doc tradotto
    └── voService.GenerateWithDestination(translatedText, lang, ...)
```

### 9.3 Traduzioni

- `generator.TranslateText(ctx, text, targetLanguage)`
- Usa Ollama stesso modello, system prompt traduttore professionale
- Il testo tradotto viene pulito con `cleanForVoiceover()` prima di TTS

---

## 10. Google Docs Integration

### 10.1 DocClient

- `drive.DocClientImpl` (`internal/upload/drive/doc_client.go`)
- Usa Google APIs (Docs + Drive) con OAuth
- `CreateDoc(title, content, folderID)` → supporta HTML
- `UpdateDoc(docID, title, content)` → aggiorna doc esistente
- `ShareDoc(docID, email, role)` → condivisione
- `ListRecentDocs(folderID, limit)` → lista documenti

### 10.2 buildBatchGoogleDocHTML

- `handler_batch_doc.go`
- Genera HTML semantico: `<h1>`, `<h2>`, `<section>`, `<p>`
- `noChapters=true` → senza TOC, senza heading, solo paragrafi
- Ogni paragrafo pulito con `cleanForVoiceover()` prima di `html.EscapeString()`
- Upload via Google Drive API con `MimeType: "application/vnd.google-apps.document"`

---

## 11. Job System (Async)

### 11.1 Architettura

```
jobservice.Service (internal/jobs/service.go)
├── Enqueue(req) → crea job in DB
├── RegisterHandler(type, handler) → registra handler
├── Get(id) → stato job
└── List(filter) → lista job

Dispatcher (internal/jobs/types.go)
├── Register(type, handler)
└── Dispatch(ctx, job, tools) → chiama handler registrato

Worker (internal/jobs/worker.go)
├── Start(ctx) → poll loop
├── ClaimNext → lease
├── runJob → Dispatch + retry + DLQ
├── renewLeaseLoop → ogni 60s
└── defaultJobTimeout → 10min (full), 2min (light)
```

### 11.2 Job Types Registrati

| Job Type | Handler | Descrizione |
|----------|---------|-------------|
| `script.generate_from_clips` | `HandleClipScriptGenerateJob` | Script unificato (text-only/clip-aware/auto-search) |
| `script.generate_batch` | `HandleBatchScriptGenerateJob` | Batch script (multi-capitolo, multi-lingua) |
| `script.generate_from_catalog` | `HandleCatalogScriptGenerateJob` | Catalog query: auto-select clip da catalogo |
| _nessuno_ | _nessuno_ | **NON esiste un job type dedicato `script.generate_with_images`.** L'endpoint `POST /api/script/generate-with-images` enqueue lo stesso job type `script.generate_from_clips` (gestito da `HandleClipScriptGenerateJob`); la differenza è solo il preset del payload. |
| `script.curate` | `HandleCurateJob` | NL query → clip compilation (mediaCurator) |

### 11.3 Retry & Dead Letter

- **Retry**: max 3, con exponential backoff (2s, 4s, 8s, ... 30s cap)
- **Dead Letter**: dopo retry esauriti → `dead_letter_jobs` table
- **Timeout**: per-job context deadline (10 min full, 2 min light)
- **Correlation ID**: propagato dal contesto API al worker, per tracing

---

## 12. Ollama Generator & Prompts

### 12.1 Generator

`internal/ml/ollama/generate.go`

| Metodo | Descrizione |
|--------|-------------|
| `GenerateScript(req)` | Script principale: chat API, web search RAG, seed random |
| `RegenerateScript(req)` | Rewrite script esistente |
| `TranslateText(text, lang)` | Traduzione via sistema |
| `GenerateVisualPrompt(text, topic, style)` | Testo → prompt visivo per AI image |
| `GenerateVideoMetadata(title)` | Descrizione YouTube + tags |
| `GenerateDescription(mediaType, prompt, style)` | Descrizione semantica per asset |

### 12.2 Prompt System

**System Prompt** (`prompts/system_prompt.go`):
- "You are an exceptional storyteller and senior copywriter"
- Lingua: EN/IT/ES/FR con istruzione esplicita
- Tono: professional, casual, enthusiastic, calm, funny, educational, documentary

**User Prompt** (`prompts/prompt_builders.go:BuildChatMessages`):
```
TASK: Write a true NARRATIVE DOCUMENTARY of N seconds.

VIDEO TITLE: ...
NARRATIVE STYLE: ...
REFERENCE INPUT: ...

STRICT REQUIREMENTS:
1. LENGTH: at least X words
2. STYLE: cinematic and immersive
3. FORMAT: continuous prose only
4. NO META-TEXT, NO TIMESTAMPS, NO SPEAKER LABELS, NO STAGE DIRECTIONS
```

**Diversity**: seed random per call, temperature 0.35 default (0.7 su cache hit), TopP 0.9 default (0.95 su cache hit)

### 12.3 Web Search RAG

- `client.WebSearcher()` → SearXNG
- `SearchQueryForScript()` → genera query se sourceText < 500 chars
- Risultati iniettati come prefisso al user message

---

## 13. Database Tables

### 13.1 Script System (media.db.sqlite)

```sql
scripts                 -- Record principale script
├── id, topic, duration, language, template, mode
├── narrative_text, timeline_json, entities_json, metadata_json, full_document
├── model_used, ollama_base_url, version, parent_script_id, is_deleted
└── created_at, updated_at

script_sections         -- Sezioni/capitoli dello script
├── id, script_id, section_type, section_title, content, sort_order
└── FK → scripts(id)

script_stock_matches    -- Match stock footage per segmento
├── id, script_id, segment_index, stock_path, stock_source, score, matched_terms
└── FK → scripts(id)

research_cache          -- Cache ricerche agent_script_writer
├── key, topic, language, max_steps, source_text
└── created_at, last_used (TTL 7 giorni)
```

### 13.2 Memory Gate (media.db.sqlite)

```sql
gemma_script_outputs    -- Cache esatta output generati
├── id, channel_id, mode, language, title, prompt
├── normalized_input, input_hash
├── output_text, output_json, model, job_id, word_count
├── created_at, updated_at
└── UNIQUE(channel_id, mode, input_hash)

gemma_memory_entries    -- Memorie riutilizzabili (struttura, hook, CTA)
├── id, channel_id, memory_type, topic_key, title
├── summary, content_text, content_json
├── source_generation_id, source_job_id
├── usefulness_score (decay 0.9x ogni 7gg), last_used_at
└── created_at

gemma_script_chunks     -- Chunk per ricerca LIKE
├── id, generation_id, channel_id, chunk_index, chunk_type
├── topic_key, title, text, search_text, embedding_json
└── created_at (TTL 60 giorni)
```

### 13.3 Job System (media.db.sqlite)

```sql
jobs                    -- Coda job
├── id, type, status, priority, project, video_name, active_key
├── correlation_id, payload_json, result_json, progress, error
├── retry_count, max_retries, worker_id, lease_expiry
├── created_at, updated_at, started_at, completed_at, cancelled_at

jobs_events             -- Eventi durante esecuzione job
├── id, job_id, type, message, data_json, created_at
└── FK → jobs(id)

dead_letter_jobs        -- Job falliti dopo retry esauriti
├── job_id, job_type, correlation_id, error
├── payload_json, retry_count, failed_at
└── INSERT-only (non referenziata da jobs)
```

---

## 14. Pulizia Markdown: cleanForVoiceover

Definita in `handler_batch.go`. Rimuove artefatti markdown che verrebbero letti dal TTS o visualizzati come caratteri brutti in Google Docs.

```go
func cleanForVoiceover(text string) string
```

| Pattern | Rimuove | Esempio |
|---------|---------|---------|
| `(?m)^[#]+\s*` | Heading markers a inizio linea | `# Titolo` → `Titolo` |
| `(?m)^[\-\_\*\=]{3,}\s*$` | Linee separatori | `---`, `***`, `===` |
| `\[[^\]]*\]` | Brackets e contenuto | `[music]`, `[Applause]` |
| `#` (globale) | Tutti i # rimasti | `Section #1` → `Section 1` |
| `*` (globale) | Tutti gli asterischi | `*italic* text` → `italic text` |
| `(?m)^[>]+\s*` | Blockquote markers | `> quote` → `quote` |
| `\n{3,}` | Multipli blank lines | 3+ newline → 2 |
| ` {2,}` | Multipli spazi | `text  more` → `text more` |

---

## 15. Diversità Script: CacheHitLimit, Angle-Shift, Temperature

### 15.1 Il Problema

Con temperatura default 0.35 e seed random, Gemma produce output molto simili se il prompt è lo stesso. Il sistema di cache esatta restituiva lo stesso testo identico a ogni richiesta.

### 15.2 Le Soluzioni

| Soluzione | Dove | Cosa fa |
|-----------|------|---------|
| **Temperature 0.7** su cache hit | `job_handler.go` | Da 0.35 → 0.7 quando `isCacheHit=true` |
| **TopP 0.95** su cache hit | `job_handler.go` | Da 0.90 → 0.95, sampling più ampio |
| **Angle-Shift Instructions** | `service.go:BuildFreshVariantPrompt` | 6 istruzioni: angolo diverso, hook diverso, ritmo diverso, nuovo esempio, riordino, chiusura diversa |
| **Avoid List** | `service.go:BuildFreshVariantPrompt` | Opening + 3 frammenti da NON usare |
| **CacheHitLimit=2** | `types.go:MemoryPolicy` + `job_handler.go` | Dopo 2 cache hit → ForceRefresh |
| **ScriptStructure 50 parole** | `service.go:ExtractMemories` | Da 200→50 parole per ridurre bias strutturale |
| **Near-duplicate check** | `job_handler.go` | N-gram Jaccard a posteriori, solo log |

### 15.3 Flow Decisionale su Cache Hit

```
CheckGate → EXACT CACHE HIT
│
├─ CountRecentExactOutputs (finestra 24h)
│   ├── < CacheHitLimit (2) → BuildFreshVariantPrompt + Temperature 0.7
│   └── >= CacheHitLimit (2) → ForceRefresh → ignora cache → generazione fresh
│
└─ SaveAfterGeneration → nuovo entry in gemma_script_outputs
   (dopo 24h il vecchio conteggio decade → cache torna utile)
```

---

## 16. Timeline System

### 16.1 Overview

Il Timeline System converte uno script narrativo in un **piano visivo strutturato**: segmenti con timestamp, soggetti, keyword di ricerca, e match con asset reali (stock footage, clip Artlist). È il ponte tra il testo generato dall'LLM e la produzione video.

Chiamato da:
- `generate-with-images` (segmentazione scene per immagini AI)
- `generate-batch` (pianificazione asset per ogni item)
- Flusso `ScriptDocs` (generazione documenti con piano visivo)

### 16.2 Architettura

```
Script narrativo
       │
       ▼
BuildTimelinePlan (timeline_logic.go)
       │
       ├─ 1. Cache check
       │   └─ Cache.LoadPlan → segment_embeddings table
       │
       ├─ 2. LLM Segmentation
       │   ├─ buildTimelinePlanningPrompt (timeline_prompt.go)
       │   ├─ callTimelineLLM (timeline_llm_client.go) — temperature=0.0
       │   └─ fallbackTimelinePlan (timeline_fallback.go) — se LLM fallisce
       │
       ├─ 3. Plan Normalization + Sanitization
       │   ├─ normalizeTimelineLLMPlan (timeline_plan_normalizer.go)
       │   └─ sanitizeTimelineLLMPlan (timeline_plan_sanitizer.go)
       │
       ├─ 4. Per-segment Processing (per ogni segmento)
       │   ├─ buildSegment (timeline_segment_builder.go)
       │   │   ├─ resolveTimelineSegmentSubject (timeline_subject_helpers.go)
       │   │   ├─ catalogNormalizer.NormalizeSegment (catalognormalizer.go)
       │   │   └─ associateSegment (timeline_asset_matcher.go)
       │   │
       │   ├─ Visual Query Generation (batch o individuale)
       │   │   ├─ visualquery.GenerateBatchArtlistVisualQueries (batch, preferito)
       │   │   ├─ visualquery.GenerateArtlistVisualQuery (individuale, fallback)
       │   │   └─ populateVisualFields → VisualSubject, VisualCaption, SearchSuggestions
       │   │
       │   ├─ Artlist Clip Search
       │   │   ├─ searchArtlistWithResolver — ClipResolver (preferito)
       │   │   └─ searchArtlistFromDB — DB direct (fallback)
       │   │
       │   ├─ Live Search (ultima spiaggia, disabilitato)
       │   │   └─ attemptLiveSearchDecision (timeline.go)
       │   │
       │   ├─ Hybrid Scoring (Semantic + Linear)
       │   │   └─ assocService.ScoreMedia → embedding + scoring
       │   │
       │   └─ Match Validation
       │       └─ hasUsefulVisualMatch (timeline_match_validator.go)
       │
       ├─ 5. Cache Store
       │   └─ storeSegmentInCache + Cache.StoreSegment
       │
       └─ 6. Usage Feedback
           └─ clipsRepo.MarkClipsUsed → aggiorna statistiche
```

### 16.3 Tipi Principali (timeline_types.go)

```go
type TimelinePlan struct {
    PrimaryFocus  string            // Topic principale
    SegmentCount  int               // Numero segmenti
    TotalDuration int               // Durata totale in secondi
    Segments      []TimelineSegment // Lista segmenti
}

type TimelineSegment struct {
    Index             int         // 1-based
    StartTime         float64     // Inizio timestamp (sec)
    EndTime           float64     // Fine timestamp (sec)
    Timestamp         string      // "0-18.5" (formattato)
    Subject           string      // Soggetto del segmento (da LLM)
    CanonicalSubject  string      // Soggetto normalizzato (da catalogo)
    NarrativeText     string      // Testo narrativo esatto
    OpeningSentence   string      // Prima frase
    ClosingSentence   string      // Ultima frase
    Keywords          []string    // Keyword estratte
    Entities          []string    // Entità (max 1 per segmento)
    CanonicalKeywords []string    // Keyword normalizzate
    CanonicalEntities []string    // Entità normalizzate
    StockMatches      []ScoredMatch  // Match stock footage
    ArtlistMatches    []ScoredMatch  // Match Artlist clips
    VisualSubject     string      // Soggetto visivo (da LLM query)
    VisualCaption     string      // Didascalia visiva
    SearchSuggestions []string    // Query per ricerca Artlist
    VisualPrompts     []string    // Prompt immagini AI
    EntityQueries     []string    // Query entità
    PreferredStockGroup  string   // Gruppo stock preferito
    PreferredStockPaths []string  // Path stock preferiti
}
```

### 16.4 LLM Segmentation (timeline_prompt.go + timeline_llm_client.go)

Il cuore del sistema è una chiamata LLM con `temperature=0.0` e `num_predict=1024` che segmenta lo script narrativo:

- **Prompt**: `buildTimelinePlanningPrompt(topic, duration, narrative, sourceText)`
- **Temperature**: 0.0 (deterministico — vogliamo consistenza nella segmentazione)
- **Output**: JSON con `primary_focus` + array `segments[]` contententi:
  - `index`, `start_time`, `end_time` (timestamp in secondi)
  - `subject` (soggetto concreto: persona, luogo, cosa)
  - `narrative_text` (testo esatto per questo segmento)
  - `opening_sentence`, `closing_sentence`
  - `keywords`, `entities` (max 1 entità)

**Regole minime segmenti:**
| Durata | Min segmenti |
|--------|-------------|
| ≤ 60s | 2 |
| 60-180s | 4 |
| ≥ 300s | 8 |
| Altre | 1 per 45s |

**Fallback** (`timeline_fallback.go`): se LLM fallisce (JSON non valido, too few segments), distribuisce il testo in modo uniforme: `calculateMinSegments(duration)` + `distributeNarrativeToSegment()`.

### 16.5 Normalization Pipeline

Dopo la segmentazione LLM, due fasi di pulizia:

**1. Sanitizer** (`timeline_plan_sanitizer.go`) — `sanitizeTimelineLLMPlan()`:
- Sostituisce soggetti che sembrano file path (`.mp4`, `.mov`, `/`)
- Sostituisce soggetti troppo lunghi (>8 parole)
- Usa entità preferite come soggetto canonico
- Fallback al topic se il soggetto è vuoto

**2. Normalizer** (`timeline_plan_normalizer.go`) — `normalizeTimelineLLMPlan()`:
- Ordina segmenti per `start_time`
- Allinea il primo segmento a `start_time=0`
- Allinea l'ultimo segmento a `end_time=duration`
- Rimuove segmenti vuoti o sovrapposti
- Arrotonda timestamp a 2 decimali
- Controlla minimo segmenti (se sotto, ritorna nil → fallback)

### 16.6 Catalog Normalizer (catalognormalizer.go)

**Ruolo**: Normalizza soggetti, keyword, ed entità di un segmento contro un indice caricato dai cataloghi stock, Artlist, e clips.

**Caricamento**: Su inizializzazione (`ensureIndex` → `loadEntries`), costruisce un indice di `catalogEntry` da tre fonti:
| Source | Repository | Priorità |
|--------|-----------|----------|
| stock | `stockRepo` | 3 (alta) |
| clips | `clipsRepo` | 2 |
| artlist | `artlistRepo` | 1 (bassa) |

**Matching**: `resolveBestCandidate()` calcola un punteggio per ogni entry basato su:
- **Token overlap**: `tokenOverlapScore(queryTokens, targetTokens)`
- **Exact match**: match esatto su nome/path (+30-35 punti)
- **Path containment**: path contiene query (+18 punti)
- **Source boost**: +5 per stock

Se il candidato ha `score ≥ 50` (o `≥ 35` per stock), il suo nome diventa il `CanonicalSubject` del segmento.

### 16.7 Asset Matching (timeline_asset_matcher.go + timeline_segment_builder.go)

**associateSegment()** chiama `association.Service` per trovare asset reali:
1. **Preferred Stock**: `ResolveAllPreferredStockMatches()` — match preferiti basati su soggetti/entità
2. **General Association**: `Associate(ctx, input)` — motore generale che cerca in tutti i cataloghi
3. I match vengono divisi in `StockMatches` vs `ArtlistMatches` per source

**Artlist Clip Search** — due modalità:
| Metodo | File | Quando |
|--------|------|-------|
| ClipResolver | `searchArtlistWithResolver()` | Preferito — raccomandazioni con deduplicazione |
| DB direct | `searchArtlistFromDB()` | Fallback — query per `SearchSuggestions` |

**Live Search** (`timeline.go:attemptLiveSearchDecision()`): disabilitato (`if false`), ma il codice esiste. Tentava `artlistService.DiscoverAndQueueRun()` come ultima spiaggia.

### 16.8 Visual Query Generation

Dopo la segmentazione, ogni segmento riceve campi visivi:
- **Batch (preferito)**: `visualquery.GenerateBatchArtlistVisualQueries()` — chiamata LLM unica per tutti i segmenti
- **Individuale (fallback)**: `visualquery.GenerateArtlistVisualQuery()` — per segmento se batch non ha prodotto risultati
- `populateVisualFields()` copia i risultati in `VisualSubject`, `VisualCaption`, `SearchSuggestions`, `VisualPrompts`, `EntityQueries`

### 16.9 Hybrid Scoring (Semantic + Linear)

Dopo il matching, `assocService.ScoreMedia()` riordina i match per rilevanza:
1. Genera embedding del `NarrativeText` del segmento
2. Calcola similarità semantica tra narrativa e ogni match
3. Combina con punteggio lineare originale
4. Riordina match per `artlist` e `stock` separatamente

### 16.10 Match Validation (timeline_match_validator.go)

`hasUsefulVisualMatch()` verifica che i match abbiano senso per il segmento:
- Estrae termini visivi da `VisualSubject`, `SearchSuggestions`, `Subject`
- Controlla se almeno un match contiene uno di questi termini
- Se nessun match è rilevante → log warning ma non blocca

### 16.11 Rendering (timeline_render.go + timeline_render_segment.go + timeline_render_assets.go)

`RenderTimeline()` produce l'output testuale del piano:

**Segment grouping**: Segmenti consecutivi con stesso soggetto e stesso asset primario vengono raggruppati (`canGroup()`):
```
[0-37]
   [0] Part 1
   [18.5] Part 2

   Subject: Amish Budget
   Primary Association: 📦 Stock Drive Association https://drive.google.com/...
```

**Singleton segment**: Singoli non raggruppabili:
```
[37-55]
   Subject: Barter System
   Start: In Amish communities, bartering isn't just...
   End: ...and a vital social glue.
   Primary Association: 🚀 Live Artlist Discovery https://...
```

### 16.12 Cache (timeline_cache.go + timeline_cache_service.go)

Il timeline plan è cached su `segment_embeddings` table (media.db.sqlite):

| Componente | Descrizione |
|-----------|-------------|
| `Cache.BuildKey()` | Hash SHA256 di (version, topic, template, duration, sourceText, narrative) |
| `Cache.LoadPlan()` | Carica piano dalla tabella `segment_embeddings` via `script_key` |
| `Cache.StoreSegment()` | Salva segmento + embedding + best match |
| `Cache.ClearKey()` | Cancella cache per una key specifica |
| `CacheVersion` | `v20` in `timeline_logic.go`, `v18` in `timeline_cache_service.go` (cache invalidata al cambio versione) |

**Embedding**: Ogni segmento viene embedded via Ollama al salvataggio, usato per similarità futura.

### 16.13 VisualPlan (visualplanner.go)

Componente parallelo usato per `generate-with-images`:
- `VisualPlan` — unifica fonti di soggetti visivi: `GlobalSubjects` (topic-wide) + `SegmentPlans` (per segmento)
- `SubjectKind`: `person`, `org`, `place`, `concept`, `object`, `fallback`
- `extractGlobalSubjects()` estrae soggetti da: analysis entities → timeline visual subjects → parole importanti → topic
- Priorità: special names (100) > visual subject (90) > important words (60) > topic (50)

### 16.14 Suggerimenti Artlist Tags (script_docs_artlist_suggestions.go)

`script_docs_artlist_suggestions.go`: componente per la timeline (non per batch).
- `suggestArtlistSearchTags()` chiama LLM con `SuggestionTemperature` per generare 3-6 tag di ricerca Artlist
- Usato dal flusso `ScriptDocsRequest` (modulare, non batch)

### 16.15 File Summary

| File | Ruolo |
|------|-------|
| `timeline_logic.go` | Orchestratore: `BuildTimelinePlan()` — entry point principale |
| `timeline_types.go` | Tipi: `TimelinePlan`, `TimelineSegment`, `timelineLLMPlan` |
| `timeline_prompt.go` | Prompt builder per LLM segmentation |
| `timeline_llm_client.go` | Chiamata LLM + parsing JSON risposta |
| `timeline_fallback.go` | Piano fallback se LLM fallisce |
| `timeline_plan_normalizer.go` | Normalizzazione timestamp e segmenti |
| `timeline_plan_sanitizer.go` | Sanitizzazione soggetti LLM |
| `timeline_segment_builder.go` | Costruzione segmento + visual fields + Artlist search |
| `timeline_asset_matcher.go` | Asset association (stock + Artlist) |
| `timeline_match_validator.go` | Validazione match visivi |
| `timeline_subject_helpers.go` | Helper: `NormalizeRepeatedSubject`, `resolveTimelineSegmentSubject` |
| `timeline_cache.go` | Cache store/load per segment embedding |
| `timeline_cache_service.go` | Cache service: BuildKey, HashSegment, Embedding |
| `timeline_render.go` | Render output testuale del piano |
| `timeline_render_segment.go` | Render header segmento |
| `timeline_render_assets.go` | Selezione asset primario per rendering |
| `timeline_utils.go` | `topicTokens()` utility |
| `timeline.go` | Live search fallback (`attemptLiveSearchDecision`) |
| `catalognormalizer.go` | Normalizzazione catalogo (stock/clips/artlist) |
| `visualplanner.go` | `VisualPlan` per immagini AI |
| `scoring_boosts.go` | `preferredCandidateBoost` punteggi |
| `assoc_util.go` | Helper associazione segmenti |
| `keywords.go` | Estrazione keyword via LLM + fallback |
| `script_docs_types.go` | Tipi `ScriptDocsRequest`, `ScriptDocument` |
| `script_docs_artlist_suggestions.go` | Suggerimenti tag Artlist via LLM |
| `script_doc_metadata.go` | Metadati per script docs |
