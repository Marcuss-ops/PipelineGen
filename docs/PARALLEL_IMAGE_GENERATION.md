# Generazione Immagini Parallela — Google Vids Image Synthesis

> ⚠️ **PARZIALMENTE ARCHIVIATO — Giugno 2026**  
> L'endpoint `/api/script/generate-with-images` **esiste ancora** come route dedicata (`ScriptFlowHandler.GenerateWithImages` in `internal/api/handlers/script/handlers/handler_generate_with_images.go`); **non** è stato unificato/mergiato in `/api/script/generate-from-clips`. Entrambi gli endpoint coesistono e condividono lo stesso job type (`script.generate_from_clips`) con preset di flag diversi (vedi §1.1).  
> Il flow "scene-by-scene AI image generation" (per-scena Google Vids Image Synthesis) è **presente** nel job unificato sotto il flag `generate_scene_images` — è quindi attivo per `/generate-with-images` (forzato a `true`) e opt-in per `/generate-from-clips` (letto dal body).  
> Questo documento è conservato come riferimento storico delle ottimizzazioni di parallelismo del session pool Chrome — i pattern descritti (acquire_page 3 fasi, prewarm, per-slot projects) sono ancora validi **all'interno** del Python `google-accounting` e vengono riusati dalla pipeline unificata `HandleClipScriptGenerateJob` quando `num_clips > 0` o quando `generate_scene_images=true`.
>
> **Per il flow corrente, leggi `docs/SCRIPT_PIPELINE.md` e `docs/PARALLELIZATION.md`.**

---

## Indice

1. [Endpoint `/api/script/generate-with-images`](#1-endpoint-apiscriptgenerate-with-images)
2. [Architettura del Parallelismo](#2-architettura-del-parallelismo)
3. [Session Pool — 3 Fasi di Acquisizione](#3-session-pool--3-fasi-di-acquisizione)
4. [Prewarm Pagine](#4-prewarm-pagine)
5. [Per-Slot Projects](#5-per-slot-projects)
6. [Benchmark](#6-benchmark)
7. [Diagnostica e Troubleshooting](#7-diagnostica-e-troubleshooting)
8. [File Coinvolti](#8-file-coinvolti)

---

## 1. Endpoint `/api/script/generate-with-images`

### 1.1 Descrizione (giugno 2026, doc-fix)

> **Doc-fix (June 2026 reconciliation):** questa sezione descriveva
> `/api/script/generate-with-images` come "alias backward-compat di
> /generate-from-clips", cosa che è falsa. Il codice
> (`handler_generate_with_images.go` + `handler_flow.go:153`) mostra
> due handler separati con request type separati. La sezione 1.1
> sottostante è stata corretta di conseguenza; il resto del doc è
> storico e rimane valido per i pattern di parallelismo.

**Stato** (doc-fix June 2026): `/api/script/generate-with-images` è un endpoint **dedicato e separato**, NON un alias backward-compat di `/api/script/generate-from-clips`. La precedente affermazione in questa sezione era imprecisa; il codice (`handler_generate_with_images.go` + `handler_flow.go:153`) mostra due handler separati con request type separati (`GenerateWithImagesRequest` vs `GenerateFromClipsRequest`). La differenza tra i due endpoint è il **preset del payload**: `/generate-with-images` **forza** `extract_entities=false`, `generate_scene_images=true`, `generate_metadata=false`; `/generate-from-clips` rispetta i flag del body. Entrambi enqueueano però lo **stesso job type** `script.generate_from_clips` (worker `HandleClipScriptGenerateJob` in `job_handler_clip_source.go`).

Il body della richiesta usa lo schema `GenerateFromClipsRequest` — NON lo
schema legacy `GenerateFromSourceRequest` (campi `scene_count`,
`images_per_scene`, `width`, `height`, `agent_max_steps`, ecc. rimossi).

### 1.2 Request (schema unificato)

> ⚠️ **Nota importante (doc-fix June 2026):** con `POST /api/script/generate-with-images` i flag `extract_entities` e `generate_metadata` sono **forzati a `false`** dal codice (`handler_generate_with_images.go`), e `generate_scene_images` è **forzato a `true`** — indipendentemente da ciò che metti nel body. L'esempio JSON qui sotto è solo illustrativo dello schema; i valori di `extract_entities`/`generate_metadata` verranno **ignorati** da questo endpoint. Per scegliere davvero i flag usa `POST /api/script/generate-from-clips`. Vedi §1.1.

```bash
POST /api/script/generate-with-images
Authorization: Bearer <token>
Content-Type: application/json

{
  "topic": "Le auto elettriche cinesi nel 2026",
  "language": "it",
  "tone": "documentary",
  "duration": 180,
  "min_words": 400,
  "num_clips": 0,
  "extract_entities": true,
  "generate_metadata": true,
  "languages": ["en", "es", "fr"],
  "output_name": "slug-personalizzato"
}
```

| Campo | Tipo | Default | Descrizione |
|-------|------|---------|-------------|
| `topic` | string | — | Argomento dello script (opzionale se `source_text` è fornito) |
| `source_text` | string | — | Testo sorgente (opzionale; se vuoto e `num_clips=0` si fa text-only) |
| `language` | string | `"en"` | Lingua principale |
| `tone` | string | — | Tono narrativo |
| `style` | string | — | Stile narrativo (alternativa a tone) |
| `model` | string | config | Modello Ollama per testo |
| `duration` | int | `0` | Durata target in secondi (deriva `target_words`) |
| `min_words` | int | `0` | Parole minime |
| `target_words` | int | `0` | Parole target (se non settato, derivato da duration) |
| `num_clips` | int | `0` | **0** = text-only; **>0** = auto-search via `mediaCurator.Curate()` |
| `clip_ids` | []string | — | ID clip esplicite (se presenti, ignorano `num_clips`) |
| `extract_entities` | bool | `false` | Estrae entità + insights + Drive folder + artlist clip suggestions |
| `artlist_search` | bool | `false` | Cerca clip Artlist per le frasi importanti |
| `stock_search` | bool | `false` | Cerca clip stock |
| `generate_metadata` | bool | `false` | Genera YouTube metadata (title, description, tags) per lingua |
| `languages` | []string | — | Lingue aggiuntive per metadata (es. `["en", "es"]`) |
| `transcript_policy` | string | `"auto"` | Policy per gestire transcript mancanti |
| `ordering_strategy` | string | `"auto"` | Strategia di ordinamento scene |
| `force_refresh` | bool | `false` | Bypassa cache esatta |
| `save_to_db` | bool | `false` | Salva lo script su DB |
| `output_name` | string | slug(title) | Nome output slug |
| `prompt_version` | string | — | Override versione del prompt |
| `min_quality_score` | float | — | Soglia qualità minima |
| `min_transcript_words` | int | — | Parole minime transcript |

### 1.3 Response (Job Enqueued)

```json
{
  "ok": true,
  "job_id": "job_1780861279167376172_79335298",
  "status": "queued",
  "clip_count": 0
}
```

### 1.4 Polling del Job

```bash
# Stato base
curl http://77.93.152.122:8081/api/script/jobs/JOB_ID

# Stato completo con eventi e risultati
curl http://77.93.152.122:8081/api/script/jobs/JOB_ID/full
```

### 1.5 Response Finale (text-only, schema unificato)

```json
{
  "ok": true,
  "script": "...",
  "word_count": 375,
  "title": "Le auto elettriche cinesi nel 2026",
  "language": "it",
  "cache_status": "miss",
  "doc_url": "https://docs.google.com/document/d/.../edit",
  "doc_id": "...",
  "timings": { "total_ms": 4500 }
}
```

In modalità clip-aware (`num_clips > 0` o `clip_ids` presenti), il result include
anche `clip_scenes`, `clip_count`, `search_results`, `narrative_plan`.

Flag opt-in del body unificato (vedi `docs/script-generation-pipeline.md` "Engine.WriteScript §" per la tabella completa; validi per `/api/script/generate-from-clips`):

| Flag | Default | Note |
|------|---------|------|
| `extract_entities` | `false` | Popola `entities_json`, `important_words`, `important_phrases`, `special_names`, `artlist_phrases`, `artlist_clip_suggestions`, `recommended_drive_folder` |
| `artlist_search` | `false` | Aggiunge Artlist al curation pool |
| `stock_search` | `false` | Aggiunge stock media al curation pool |
| `generate_metadata` | `false` | Genera YouTube metadata (title, description, tags) per lingua |
| `save_to_db` | `false` | Salva lo script su `scripts` table |
| `generate_timeline` | `false` | Costruisce timeline visiva (subject/keywords/entities per segment) |
| `force_refresh` | `false` | Bypassa cache esatta |
| `use_memory` | `true` | `*bool`; `nil` = enabled, `false` = forza fresh generation |
| `transcript_policy` | `"auto"` | `""` → `"auto"`; validi: `auto`/`full`/`evidence_only`/`summary_only` |
| `ordering_strategy` | `"auto"` | `""` → `"auto"`; validi: `auto`/`chronological`/`thematic` |

> **Giugno 2026:** `create_doc` NON è un flag di `GenerateFromClipsRequest` (esiste solo su `GenerateFromCatalogRequest`, vedi `internal/api/handlers/script/handlers/types_catalog.go:22`). Il Google Doc è **sempre creato** dal flow unificato (`flow_doc.go::maybeCreateGoogleDoc`).

In modalità `extract_entities=true`, include `entities_json`, `important_words`,
`important_phrases`, `special_names`, `artlist_phrases`,
`artlist_clip_suggestions`, `recommended_drive_folder`, `phrase_clip_suggestions`,
`intro_clips`.

---

## 2. Architettura del Parallelismo

### 2.1 I 4 Colli di Bottiglia Originali

Prima delle ottimizzazioni (Giugno 2026), la generazione immagini era **totalmente seriale**:

| # | Collo di Bottiglia | Tempo per 4 immagini | Dopo ottimizzazione |
|---|-------------------|---------------------|-------------------|
| 1 | **Project lock** — tutte le richieste bloccate sullo stesso progetto Vids | 4x attesa | ✅ `isolated=True` bypassa il lock |
| 2 | **Stesso progetto Vids** — Google Vids serializza operazioni sullo stesso documento | 4x sequenziale | ✅ Ogni slot ha il suo progetto dedicato |
| 3 | **Pool lock** — `acquire_page` bloccava tutti durante `page.goto()` 1-3s | 4x seriale | ✅ Navigazione FUORI dal lock |
| 4 | **Pagine Chrome fredde** — create on-demand quando ParallelMap parte | 5-10s attesa | ✅ Prewarm durante Ollama |

### 2.2 Flusso Attuale (Parallelo)

```
Job inizia
  │
  ├─ goroutine: POST /prewarm-pages (crea N pagine navigate ai progetti per-slot)
  │     │
  │     ▼  4 pagine Chrome navigate ai progetti Vids dedicati
  │     ┌──────┬──────┬──────┬──────┐
  │     │slot 0│slot 1│slot 2│slot 3│
  │     │proj A│proj B│proj C│proj D│
  │     └──────┴──────┴──────┴──────┘
  │
  └─ Ollama genera testo (~16-21s) ── IN PARALLELO col prewarm
       │
       ▼  (entrambi finiti più o meno insieme)
ParallelMap 4-6 scene
  ├─ Scene 1 → acquire_page() → prewarm HIT → GenerateSmartImage → Google Vids
  ├─ Scene 2 → acquire_page() → prewarm HIT → GenerateSmartImage → Google Vids
  ├─ Scene 3 → acquire_page() → prewarm HIT → GenerateSmartImage → Google Vids
  ├─ Scene 4 → acquire_page() → prewarm HIT → GenerateSmartImage → Google Vids
  └─ Scene 5 → acquire_page() → new page (naviga FUORI dal lock)
       │         (TUTTE IN PARALLELO, ognuna sul suo progetto Vids)
       ▼
N immagini generate in ~37s/img invece di ~65s
```

### 2.3 Strategia di Fallback

```
GenerateSmartImage:
  1. Cache DB (LIKE '%for prompt: <prompt>') → ~1ms
  2. Google Vids Image Synthesis (via Python google-accounting) → ~30-60s
  3. NVIDIA NIM / FLUX (fallback se Google Vids offline/fallisce) → ~5-15s
```

---

## 3. Session Pool — 3 Fasi di Acquisizione

Il cuore del parallelismo è il refactoring di `acquire_page()` in `session_pool.py`.

### 3.1 Prima (Tutto dentro il lock)

```python
async with self._lock:  # ← BLOCCO TUTTI
    session = find_session()
    page = await session.context.new_page()      # ms
    await page.goto(URL, timeout=60000)           # 1-3s LENTO!
    self._active_pages.add(page)
    return page
```

Tutte le chiamate concorrenti a `acquire_page` venivano **serializzate** — la prima navigava, poi la seconda, poi la terza, ecc.

### 3.2 Dopo (3 fasi, navigazione fuori dal lock)

```python
# Fase 1: LOCK (millisecondi)
async with self._lock:
    page = trova_warm_page_o_crea_nuova()
    needs_navigation = True

# Fase 2: NO LOCK (1-3s, IN PARALLELO con altri task)
if needs_navigation:
    await page.goto(URL)  # ← PIÙ TASK NAVIGANO IN PARALLELO!

# Fase 3: LOCK (millisecondi)
async with self._lock:
    self._active_pages.add(page)
    return page
```

### 3.3 Gestione Errori in Fase 2

Se `page.goto()` fallisce (timeout, errore di rete):
1. La pagina viene chiusa
2. Il session viene rilasciato (`session.in_use = False`) sotto lock
3. L'eccezione viene propagata al chiamante

---

## 4. Prewarm Pagine

### 4.1 Come Funziona

Il prewarm Chrome è ora parte della pipeline unificata (`job_handler_clip_source.go`):

```go
// In parallelo con Ollama — prewarm subito
go func() {
    prewarmURL := fmt.Sprintf("%s/prewarm-pages?account=favamassimo&count=4", gaURL)
    http.Post(prewarmURL, "application/json", nil)
}()
```

Il server Python (`main.py`) risponde chiamando `session_pool.prewarm_pages()` che:

1. Per ogni slot disponibile del pool, crea una nuova `Page`
2. **Fuori dal pool lock**, naviga ogni pagina al progetto Vids dedicato per-slot (`asyncio.gather`)
3. Registra le pagine in `_warm_pages` con la chiave `(account, "isolated:<VIDS_PROJECT_ID>")`
4. Quando `acquire_page` cerca una pagina per quella chiave, la trova già calda e la restituisce immediatamente

### 4.2 Endpoint `/prewarm-pages`

```bash
POST /prewarm-pages?account=favamassimo&count=4
```

### 4.3 Limiti

- Il prewarm può preparare solo pagine per slot già warmati (creati durante l'avvio del server).
- Se il warmup non ha ancora finito tutti gli slot, alcune pagine non verranno prewarmate.
- Le pagine non prewarmate vengono create on-demand in `acquire_page` — ora con navigazione fuori dal lock.

---

## 5. Per-Slot Projects

### 5.1 Il Problema

Con `isolated=True`, le richieste non erano più bloccate dal project lock, ma usavano tutte lo **stesso progetto Vids** (`VIDS_PROJECT_ID`). Google Vids **serializza** le operazioni di Image Synthesis sullo stesso progetto, quindi 4 immagini venivano comunque generate una alla volta.

### 5.2 La Soluzione

Ogni slot del pool ora ha un **progetto Vids dedicato**, creato automaticamente alla prima richiesta:

```python
async def _ensure_slot_project_id(self, account, session):
    key = (account, session.slot_id)
    # Cache L1: in-memory dict
    if key in self._slot_projects:
        return self._slot_projects[key]
    # Cache L2: storage persistente (sqlite key-value)
    stored = get_project_id(self._slot_project_key(key))
    if stored:
        self._slot_projects[key] = stored
        return stored
    # Crea nuovo progetto navigando a /videos/create
    project_id = create_new_vids_project(session.context)
    self._slot_projects[key] = project_id
    save_project_id(key, project_id)
    return project_id
```

Flusso:
1. Prima richiesta per slot `i` → crea nuovo progetto Vids (`/videos/create`) → naviga e cattura `project_id`
2. Salva in storage persistente (sqlite)
3. Richieste successive → cache L1 istantanea
4. Ogni slot ha un progetto diverso → Google Vids genera in parallelo

### 5.3 Lifecycle

- I progetti sono permanenti (salvati su storage persistente)
- Il primo avvio dopo un restart richiede la creazione di N progetti (una tantum)
- I progetti non vengono mai cancellati automaticamente

---

## 6. Benchmark

### 6.1 Test 7 Giugno 2026

| Metrica | PRIMA (4 scene, seriale) | ORA (6 scene, parallelo) | Miglioramento |
|---------|:---:|:---:|:---:|
| Scene | 4 | **6** | +50% |
| Immagini generate | 4 | **6** | +50% |
| **Tempo immagini (assets)** | **259.1s** | **223.4s** | **-14%** |
| Tempo per immagine | 64.8s | **37.2s** | **1.74x più veloce** 🚀 |
| Gen testo | 14.5s | 21.4s | ⚠️ (6 scene richiedono più testo da generare vs 4) |
| **TOTALE** | **327.5s** | **299.8s** | **Più veloce con +50% contenuto** |

### 6.2 Fattori

| Fattore | Prima | Ora |
|---------|-------|-----|
| Concorrenza ParallelMap | 4 workers | 4 workers (invariato) |
| Project lock | bloccava tutto | bypassato (isolated=True) |
| Progetti Vids | 1 condiviso | 1 per slot (8 max) |
| Pool lock | serializzava navigazione | navigazione fuori lock |
| Prewarm | nessuno | durante Ollama |
| Warm Chrome pages | cool | da prewarm |

---

## 7. Diagnostica e Troubleshooting

### 7.1 Verificare che il Parallelismo Funzioni

```bash
# 1. Lista job Python recenti
curl http://127.0.0.1:8000/jobs?limit=10

# 2. Controlla che i job Vids siano partiti in parallelo
# (cerca timestamp ravvicinati nei log)
journalctl -u pipelinegen --since '5 min ago' --no-pager -l | grep 'vids.*image'

# 3. Verifica prewarm
journalctl -u pipelinegen --since '10 min ago' --no-pager -l | grep -i prewarm

# 4. Check progetto per-slot nei log Python
cat google-accounting/logs/sync.log | grep 'Dedicated Vids project'
```

### 7.2 Problemi Comuni

| Sintomo | Causa | Soluzione |
|---------|-------|-----------|
| Immagini ancora sequenziali | Lock serializza su stesso progetto Vids | Verifica che `isolated=True` sia inviato e `_ensure_slot_project_id` crei progetti diversi |
| Prewarm non funziona | Server Python non ha slot warmati | Aspetta che `warmup_account` finisca (8 Chrome contexts ~2-3 minuti) |
| Prewarm registra pagine ma `acquire_page` non le trova | Key mismatch: l'ID progetto Vids Python (`config.py:VIDS_PROJECT_ID`) non corrisponde a quello inviato da Go | Verifica che `VIDS_PROJECT_ID` in `google-accounting/config.py` corrisponda a `vids_project_id` in `config.yaml` (Go) |
| Errore "no warm session" | Pool non inizializzato | Verifica `session_pool_instance.start()` nel lifespan |
| `acquire_page` timeout session | Tutti gli slot occupati | Aumenta `MAX_WARM_CONTEXTS` in config.py o `VIDS_IMAGE_CONCURRENCY` |

---

## 8. File Coinvolti

### Go (PipelineGen)

| File | Ruolo |
|------|-------|
| `pkg/googleaccounting/models.go` | `VidsImageRequest` con campo `Isolated *bool` |
| `docs/images/google_generate.go` | `GenerateSmartImage()` — `Isolated: true` |
| `docs/images/google_vids.go` | `GenerateVidsImage()` — `Isolated: true` |
| `internal/api/handlers/script/handlers/job_handler_clip_source.go` | `HandleClipScriptGenerateJob` (pipeline unificata) |

### Python (google-accounting)

| File | Ruolo |
|------|-------|
| `google-accounting/models.py` | `VidsImageRequest` con campo `isolated` |
| `google-accounting/main.py` | Endpoint `/generate-vids-images` con parallel_batch, endpoint `/prewarm-pages` |
| `google-accounting/session_pool.py` | `acquire_page()` 3 fasi, `prewarm_pages()`, `_ensure_slot_project_id()` |
| `google-accounting/automation/vids.py` | `generate_vids_image_v1_pooled()` con flag isolated/lock_project |

### Configurazione

| File | Chiave | Ruolo |
|------|--------|-------|
| `config.yaml` | `google_accounting.server_url` | URL del server Python |
| `config.py` | `VIDS_PROJECT_ID` | Progetto Vids condiviso (per fallback non-isolated) |
| `config.py` | `MAX_WARM_CONTEXTS` | Numero massimo slot Chrome (default 8) |
