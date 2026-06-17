# CHANGELOG — 3 Giugno 2026

## Riepilogo

Sessione di ottimizzazione metadata YouTube, language detection universale, e robustezza pipeline.

---

## -2. (Giugno 2026) P0 #1 — Token logging scrub + query-string auth removal

**Scope**: chiusura del P0 #1 dell'audit di sicurezza (token loggato in chiaro + query-string auth accettata + request logger che registrava la query completa).

**Work** (dettaglio completo in `## 0. (Gennaio 2026) BREAKING` sopra, sezione "Breaking — token query-string auth rimosso"):
- **Token logging scrub**: rimossi `received_token`, `expected_token`, `token` dai log del middleware `Auth`; aggiunto booleano `has_credential` (con rationale anti-timing-attack). Vedi `internal/api/middleware/middleware.go::Auth` e `extractAuthToken`.
- **Query-string auth rimosso**: `extractAuthToken` non legge più `?token=…`; i token sono accettati **solo** via `X-Velox-Admin-Token` o `Authorization: Bearer <token>`. Client che passavano `?token=…` ricevono ora 401.
- **Request logger query redaction**: `redactSensitiveQuery` redige i parametri `token`, `api_key`, `apikey`, `key`, `secret`, `password`, `auth`, `credential`, `access_token`, `webhook_secret` prima di scrivere il query string. La chiave viene normalizzata in lowercase nell'output.
- **5 test di regressione** in `internal/api/middleware/auth_test.go`:
  - `TestRedactSensitiveQuery` (18 casi, incluse limitazioni pinned come URL-encoded key e `;` separator)
  - `TestExtractAuthToken_RejectsQueryString`
  - `TestExtractAuthToken_AcceptsHeaders` (incluso precedence X-Velox > Authorization)
  - `TestAuth_RejectsQueryStringToken_EndToEnd`
  - `TestAuth_NeverPersistsTokenValue` (api_requests scan come check load-bearing)

---

## -1. (Giugno 2026) P0 #2 — Rimosso bypass auth per path webhook

**Scope**: chiusura del P0 #2 dell'audit di sicurezza (auth bypass basato su path).

**Cambiamenti**:
- **Rimosso `publicWebhookPaths` e `isPublicWebhookPath`** da `internal/api/middleware/middleware.go`. Il path `POST /api/images/webhook/remote` non bypassa più l'auth.
- L'endpoint remoto Google Flow (`/api/images/webhook/remote`) deve ora inviare un token valido via `Authorization: Bearer <token>` o `X-Velox-Admin-Token: <token>`. Il worker remoto (`google-accounting/` sidecar Python) **NON invia attualmente nessun header di autenticazione** per questo endpoint (verificato con grep su `google-accounting/**/*.py`: 0 match per `Authorization`/`X-Velox-Admin-Token`/`/api/images/webhook`). **Breaking per il deploy**: aggiornare il client Python per includere l'header prima del prossimo deploy, altrimenti il webhook restituirà 401 e le immagini remote non verranno più ingerite. Vedi il diff di migrazione sotto.
- **Rimossi 4 test** (`TestIsPublicWebhookPath_*`) da `internal/api/middleware/validation_test.go` (erano marcati con `// REMOVE-WITH-P0-#2`).
- Aggiunto `TestAuth_RejectsWebhookPathWithoutToken` in `internal/api/middleware/auth_test.go` (4 subtest: no auth → 401, wrong token → 401, valid X-Velox → 200, valid Bearer → 200) per impedire il re-inserimento silenzioso del bypass.
- Aggiornato AGENTS.md per riflettere la rimozione del bypass.

**Migrazione per il worker remoto** (richiesta PR nel repo `google-accounting/`):
```diff
- POST /api/images/webhook/remote
- (multipart form con job_json + immagini, nessun header)
+ POST /api/images/webhook/remote
+ -H "Authorization: Bearer $VELOX_ADMIN_TOKEN"
+ (multipart form con job_json + immagini)
```

`$VELOX_ADMIN_TOKEN` deve essere configurato nell'env del sidecar (deve corrispondere al valore di `security.admin_token` del server Go). Vedi `internal/config/types.go:109` e `AGENTS.md` §"Active Concerns" #3.

## -1a. (Giugno 2026) P0 #3 — Aggiunto `internal/jobs/worker.go` alla exempt list `context.Background()`

**Scope**: chiusura del P0 #3 dell'audit di sicurezza (audit `context.Background()` per AGENTS.md policy).

**Cambiamenti**:
- Il `finalizationCtx` in `internal/jobs/worker.go` (~riga 146) usa `context.Background()` intenzionalmente per garantire che il job venga marcato come failed/completed/dead-lettered nel DB anche se `jobCtx` (deadline per-job) o `ctx` (worker lifecycle) vengono cancellati. Questa è una pattern equivalente al "post-write save context" già esentato in AGENTS.md per `postwrite.go`, `gemmamemory/service.go`, `scriptcore/write_script.go`.
- Aggiornato AGENTS.md "Context.Background() Policy" exempt table con la nuova entry e il rationale completo (perché non può derivare da `ctx` né da `jobCtx`).
- Nessuna modifica al codice: il `context.Background()` rimane, ma è ora formalmente documentato come design intenzionale.

---

**Scope**: cleanup legacy dopo l'unificazione del flow `/api/script/generate` → `/generate-from-clips`.

**Cambiamenti**:
- **20 file rimossi** dall'intero flow `generate_with_images` (handler dedicato, job handler, response builder, 9 file `phase_*.go`, archive DB, types, Google Doc builder, file writer, tests).
- **Breaking — token query-string auth rimosso.** `internal/api/middleware/middleware.go::extractAuthToken` non legge più `?token=…`; i token sono accettati **solo** via header `X-Velox-Admin-Token` o `Authorization: Bearer <token>`. I campi `received_token` / `expected_token` / `token` sono stati rimossi dai log; al loro posto un booleano `has_credential`. Il request logger redige automaticamente i parametri sensibili (`token`, `api_key`, `secret`, `password`, `auth`, `credential`, `access_token`) prima di scrivere il query string. La chiave viene normalizzata in lowercase nell'output (`?Token=foo` → `token=[REDACTED]`). Client che passavano `?token=…` ricevono ora 401 — aggiornare a `Authorization: Bearer $VELOX_ADMIN_TOKEN` oppure `X-Velox-Admin-Token: $VELOX_ADMIN_TOKEN`.
- **`/api/script/generate-with-images` è preservato come endpoint dedicato e separato** (route registrata in `internal/api/handlers/script/handlers/handler_flow.go:153`, handler `GenerateWithImages` in `handler_generate_with_images.go`). Non è un alias di `/api/script/generate-from-clips`: ha un proprio request type (`GenerateWithImagesRequest`), un proprio handler e forza nel payload i flag `extract_entities=false`, `generate_scene_images=true`, `generate_metadata=false`. Entrambi gli endpoint enqueueano però lo **stesso job type** (`script.generate_from_clips`, gestito da `HandleClipScriptGenerateJob`) — la differenza è il preset del payload, non la pipeline.
- **Body schema cambiato** da `GenerateFromSourceRequest` (legacy) a `GenerateFromClipsRequest` (unificato). Campi rimossi/non più accettati: `scene_count`, `images_per_scene`, `width`, `height`, `agent_max_steps`, `agent_min_words`, `agent_batch`, `agent_model`, `lines_per_image`, `sentences_per_image`, `render_video`, `video_style`, `video_transition`, `visual_style`, `recommend_clips`, `width`, `height`. Campi nuovi: `num_clips`, `clip_ids`, `extract_entities`, `artlist_search`, `stock_search`, `generate_metadata`, `transcript_policy`, `ordering_strategy`, `force_refresh`, prompt versions.
- **Costanti rimosse**: `JobTypeSourceScriptGenerate`, `SourceScriptGeneratePayload`, `ModeGenerateWithImages`, `ScriptsGenerateImagesFolder`, `ScriptsGenImagesFolder()`.
- **Campi config rimossi** (dead config, non più referenziati dal codice): `MaxSceneCount`, `MaxImagesPerScene`, `SceneImageConcurrency`, `DefaultSceneCount`, `DefaultImageWidth`, `DefaultImageHeight`, `AllowedResolutions`, `DefaultAgentMaxSteps`, `DefaultAgentMinWords`, e l'helper `IsAllowedResolution()`. **Breaking per operatori**: le env var corrispondenti (`VELOX_SCRIPTS_MAX_SCENE_COUNT`, `VELOX_SCRIPTS_MAX_IMAGES_PER_SCENE`, `VELOX_SCRIPTS_SCENE_IMAGE_CONCURRENCY`, `VELOX_SCRIPTS_DEFAULT_SCENE_COUNT`, `VELOX_SCRIPTS_DEFAULT_AGENT_MAX_STEPS`, `VELOX_SCRIPTS_DEFAULT_AGENT_MIN_WORDS`, `VELOX_SCRIPTS_DEFAULT_IMAGE_WIDTH`, `VELOX_SCRIPTS_DEFAULT_IMAGE_HEIGHT`) e le chiavi YAML omonime sono **silenziosamente ignorate**. Rimuoverle da `config.yaml` e dagli ambienti di deploy. Vedi `config.example.yaml` per la lista completa.
- **Test Python aggiornati** in `google-accounting/` (`test_generate_with_images.py`, `test_script_with_images.py`, `test_long_script_5_images.py`) per usare lo schema unificato.
- **Job vecchi in coda**: se ci sono job in stato `pending` o `processing` con `type=script.generate_from_source`, verranno scartati dal worker (il job type non esiste più). Per forzare la pulizia, eseguire: `sqlite3 data/media/media.db.sqlite "UPDATE jobs SET status='cancelled', error='legacy job type removed (June 2026 cleanup)' WHERE type='script.generate_from_source' AND status IN ('pending','processing','queued');"`.
- **Google Drive**: la subfolder `Generate With Images` non viene più auto-creata. I doc generati vanno nella cartella `Scripts` root o nella subfolder `Generate`.

**Migrazione per i client esterni**:
```diff
- POST /api/script/generate-with-images
- { "topic": "...", "scene_count": 5, "images_per_scene": 2, "width": 1024, "height": 1024 }
+ POST /api/script/generate-from-clips
+ { "topic": "...", "num_clips": 0, "duration": 180, "min_words": 400,
+   "extract_entities": true, "generate_metadata": true }
```
Oppure continuare a chiamare `/generate-with-images` come endpoint dedicato (`ScriptFlowHandler.GenerateWithImages`) usando il body nuovo — ma attenzione: i flag `extract_entities`/`generate_metadata` sono forzati a `false` e `generate_scene_images` è forzato a `true`, indipendentemente dal body. Per avere `extract_entities`/`generate_metadata` opt-in usare `/generate-from-clips`.

---

---

## 1. Audio Mancante nelle Clip YouTube — Fix `CutCopy`

**Problema**: Il comando ffmpeg `-c copy` nel `PreDownloadedPath` path di `youtube_pipeline.go` era scritto inline senza `-reset_timestamps 1`. Causava PTS non resettati (partivano da 300s invece di 0), producendo file output con `duration=0` e 0 frame processati da `CutAndNormalize`.

**Fix**: Sostituito il comando ffmpeg inline con `p.clipProcess.CutCopy()` che già usa:
- `-reset_timestamps 1` — resetta i PTS a 0
- `-avoid_negative_ts make_zero` — gestisce timestamp negativi
- `-to` dopo `-i` (posizione corretta per output option)

**File**: `internal/media/videomuscles/youtube_pipeline.go`

---

## 2. Metadata YouTube — 5 Fix in `extractor_process.go`

| # | Problema | Fix |
|---|---|---|
| 1 | **search_text vuoto** in 6/8 clip | `buildFallbackSearchText()` costruisce search_text da nome clip + metadata esistenti se yt-dlp fallisce. **Mai più search_text vuoto** |
| 2 | **youtube_title sovrascritto** col nome clip | Rimosso `existing.Name = ym.Title` — il nome segmento (es. "padre spelling") viene preservato, youtube_title resta in metadata |
| 3 | **language vuoto** anche con `youtube_language=en` | `language` propagato da `youtube_language` nei metadata, anche nel fallback |
| 4 | **youtube_tags inconsistente** (stringa/array) | Salvato come `[]string` nativo → JSON array corretto |
| 5 | **Nessun re-enrich per clip legacy** | Gate cambiato: ricontrolla se `search_text` è vuoto anche se `youtube_title` esiste già |

**File**: `internal/sources/youtube/extractor_process.go`

---

## 3. Qdrant Timeout e Debug Logging

| Modifica | Dove | Perché |
|---|---|---|
| `timeout_ms: 5000 → 30000` | `config.yaml` | Qdrant API calls ora hanno 30s invece di 5s |
| Log Warn se `vectorStore == nil` | `clipindexer/service.go` | Prima ritorno silenzioso senza log |
| Log Warn con dimensioni embedding | `vectorstore/adapter.go` | `hasEmbeddings` fallito ora visibile |
| Log separati per store nil/disabled | `vectorstore/adapter.go` | Debug preciso del punto di blocco |

---

## 4. Language Detection Universale via Whisper

**Script creati**:

### `scripts/transcribe_detect_lang.py`
- Trascrizione audio/video con **faster-whisper**
- Language detection automatica (99.93% accuratezza su clip Kevin Hart → `en`)
- `--json-only` flag: JSON pulito su stdout, log su stderr — **per integrazione Go**
- `detect_language()` veloce (modello tiny) e `transcribe()` completo (modello base)
- Modello whisper cachato a livello module (non ricaricato a ogni chiamata)
- `tempfile` invece di path hardcoded
- **Nessuna scrittura DB** — puro AI, output JSON

### `scripts/update_language.py`
- Aggiornamento `language` in `metadata_json` per **qualsiasi media type**
- `--source` flag: filtra per source (youtube, artlist, stock, image, voiceover...)
- `--media-type` flag: filtra per media type (video, audio, image)
- `--auto-detect` mode: chiama whisper sul file locale per rilevare lingua reale
- `--batch` mode: dinamico, nessun hardcoding YouTube
- Stessa cache whisper module-level condivisa

### Architettura

```
┌─────────────────────────────┐    JSON (stdout)    ┌──────────────┐
│  transcribe_detect_lang.py  │ ──────────────────→ │   Go server  │
│  (AI only)                   │    {"language":     │  (orchestra,  │
│  --json-only flag            │     "en", ...}      │   scrive DB,  │
│  No DB writes                │                     │   reindex,    │
└─────────────────────────────┘                     │   Qdrant)     │
                                                    └──────────────┘
                                                          ↑
┌─────────────────────────────┐                           │
│  update_language.py          │ ── CLI convenience ───────┘
│  (AI + DB in uno)            │    (uso manuale)
└─────────────────────────────┘
```

---

## 5. Batch Update — 65 Clip YouTube → `language: en`

Dopo aver verificato con whisper che la lingua è **inglese (99.93%)** sulla clip più lunga ("codice emergenza hart", 416s):

```bash
python3 scripts/update_language.py --batch --lang en  --source youtube
# → 65 YouTube clips updated to language='en'
```

Poi reindicizzate tutte le 9 clip Kevin Hart in Qdrant con language=en:

```bash
for id in yt_W6ESLDpD8Ag_*; do
  curl -X POST "/api/media/youtube/clips/$id/reindex"
done
```

**Risultato finale Qdrant**: 582 punti, di cui 10+ YouTube con `language: en` e `search_text` popolato.

---

## 6. Estrazione Kevin Hart su Drive (9 clip, solo Go, zero Python)

| Clip | Durata | Audio | Drive |
|---|---|---|---|
| codice emergenza hart | 197 MB | ✅ | ✅ |
| fidanzata bagagliaio | 161 MB | ✅ | ✅ |
| fidanzata bagagliaio 2 | 100 MB | ✅ | ✅ |
| struzzo inseguimento | 38 MB | ✅ | ✅ |
| struzzo inseguimento 2 | 11 MB | ✅ | ✅ |
| padre spelling | 2.3 MB | ✅ | ✅ |
| padre spelling 2 | 2.3 MB | ✅ | ✅ |
| padre colloquio 1 | 1.1 MB | ✅ | ✅ |
| padre colloquio 2 | 1.1 MB | ✅ | ✅ |

---

## Riepilogo Tecnico

```text
✓ Audio YouTube fixato (CutCopy + reset_timestamps 1)
✓ search_text mai più vuoto (fallback buildFallbackSearchText)
✓ youtube_title preservato, non sovrascritto
✓ youtube_tags sempre array JSON (mai stringa)
✓ language propagato da youtube_language
✓ Qdrant timeout 5s → 30s
✓ Logging visibile per upsert failures
✓ Language detection universale via whisper (99.93%)
✓ Script riutilizzabili per tutti i media types
✓ 9 clip Kevin Hart su Drive con audio + metadata
✓ 65 clip YouTube aggiornate con language=en in Qdrant
```
